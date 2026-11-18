package service

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PerfService 做接口级性能回归分析（F11）。
//
// 三条设计约定：
//   - **只读聚合**，不新增表：数据来自已有的 step_result（elapsed_ms + final_url + 快照）；
//   - 聚合口径是「方法 + 归一化路径」，纯数字/UUID 段归一成 `:id`，
//     否则每个主键值都会变成一个新"接口"，统计彻底失效；
//   - 结论必须可解释：样本不足就说样本不足，不给"看起来没事"的假结论。
type PerfService struct {
	Deps
}

// PerfOptions 是查询参数（全部有默认值与范围夹取）。
type PerfOptions struct {
	RunID        uint64  `json:"run_id"`
	Days         int     `json:"days"`
	BaselineRuns int     `json:"baseline_runs"`
	MinSamples   int     `json:"min_samples"`
	ThresholdPct float64 `json:"threshold_pct"`
	MinDeltaMs   int64   `json:"min_delta_ms"`
	Verdict      string  `json:"verdict"`
}

// 回归结论。
const (
	PerfRegressed    = "regressed"
	PerfImproved     = "improved"
	PerfStable       = "stable"
	PerfInsufficient = "insufficient"
	PerfNoBaseline   = "no_baseline"
)

// PerfPoint 是趋势上的一个点（一次执行）。
type PerfPoint struct {
	RunID uint64 `json:"run_id"`
	At    string `json:"at"`
	Ms    int64  `json:"ms"`
}

// PerfItem 是某个接口的对比结果。
type PerfItem struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Endpoint string `json:"endpoint"`

	LatestRunID uint64 `json:"latest_run_id"`
	LatestAt    string `json:"latest_at"`
	LatestMs    int64  `json:"latest_ms"`

	BaselineMs int64   `json:"baseline_ms"`
	Samples    int     `json:"samples"`
	DeltaMs    int64   `json:"delta_ms"`
	DeltaPct   float64 `json:"delta_pct"`
	Verdict    string  `json:"verdict"`

	Trend []PerfPoint `json:"trend"`
}

// PerfSummary 是概览计数。
type PerfSummary struct {
	Endpoints    int `json:"endpoints"`
	Regressed    int `json:"regressed"`
	Improved     int `json:"improved"`
	Stable       int `json:"stable"`
	Insufficient int `json:"insufficient"`
}

// PerfResult 是接口返回。
type PerfResult struct {
	Items   []PerfItem  `json:"items"`
	Summary PerfSummary `json:"summary"`
	// Applied 回显实际生效的参数（夹取后），避免"我传了但没生效"的猜疑。
	Applied PerfOptions `json:"applied"`
}

// clamp 归一参数范围。
func (o *PerfOptions) clamp() {
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Days > 365 {
		o.Days = 365
	}
	if o.BaselineRuns <= 0 {
		o.BaselineRuns = 10
	}
	if o.BaselineRuns > 100 {
		o.BaselineRuns = 100
	}
	if o.MinSamples <= 0 {
		o.MinSamples = 3
	}
	if o.MinSamples > 50 {
		o.MinSamples = 50
	}
	if o.ThresholdPct <= 0 {
		o.ThresholdPct = 30
	}
	if o.ThresholdPct > 1000 {
		o.ThresholdPct = 1000
	}
	if o.MinDeltaMs < 0 {
		o.MinDeltaMs = 0
	}
	if o.MinDeltaMs > 60000 {
		o.MinDeltaMs = 60000
	}
	o.Verdict = strings.TrimSpace(strings.ToLower(o.Verdict))
}

// Analyze 做接口级回归分析。
func (s *PerfService) Analyze(projectID uint64, opts PerfOptions) (*PerfResult, error) {
	opts.clamp()
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	if opts.RunID > 0 {
		var run struct{ ProjectID uint64 }
		if err := s.DB.Table("run_record").Select("project_id").Where("id = ?", opts.RunID).
			Scan(&run).Error; err != nil {
			return nil, errInternal("查询执行记录失败", err)
		}
		if run.ProjectID != projectID {
			return nil, errBadParam("执行 %d 不属于该项目", opts.RunID)
		}
	}

	samples, err := s.loadSamples(projectID, opts.Days)
	if err != nil {
		return nil, err
	}

	// endpoint → run_id → 该次执行里这个接口的平均耗时
	type agg struct {
		sum, count int64
		at         time.Time
	}
	byEndpoint := map[string]struct {
		method string
		path   string
		runs   map[uint64]*agg
	}{}
	for _, sm := range samples {
		ep := sm.Method + " " + sm.Path
		e, ok := byEndpoint[ep]
		if !ok {
			e.method, e.path = sm.Method, sm.Path
			e.runs = map[uint64]*agg{}
			byEndpoint[ep] = e
		}
		a := e.runs[sm.RunID]
		if a == nil {
			a = &agg{at: sm.CreatedAt}
			e.runs[sm.RunID] = a
		}
		a.sum += sm.ElapsedMs
		a.count++
	}

	result := &PerfResult{Items: []PerfItem{}, Applied: opts}
	for ep, e := range byEndpoint {
		points := make([]PerfPoint, 0, len(e.runs))
		for runID, a := range e.runs {
			ms := a.sum / a.count
			points = append(points, PerfPoint{RunID: runID, At: a.at.Format("2006-01-02 15:04:05"), Ms: ms})
		}
		sort.Slice(points, func(i, j int) bool { return points[i].At < points[j].At })

		// 指定 run_id 时：只保留该次执行出现过的接口，并以那一次为"当前"
		latestIdx := len(points) - 1
		if opts.RunID > 0 {
			found := -1
			for i := range points {
				if points[i].RunID == opts.RunID {
					found = i
					break
				}
			}
			if found < 0 {
				continue
			}
			latestIdx = found
		}
		latest := points[latestIdx]
		base := points[:latestIdx]
		if len(base) > opts.BaselineRuns {
			base = base[len(base)-opts.BaselineRuns:]
		}
		vals := make([]int64, 0, len(base))
		for _, p := range base {
			vals = append(vals, p.Ms)
		}
		baseline := median(vals)
		verdict, deltaMs, deltaPct := judgePerf(latest.Ms, baseline, len(vals), opts)

		trend := points
		if len(trend) > 10 {
			trend = trend[len(trend)-10:]
		}
		item := PerfItem{
			Method: e.method, Path: e.path, Endpoint: strings.TrimSpace(ep),
			LatestRunID: latest.RunID, LatestAt: latest.At, LatestMs: latest.Ms,
			BaselineMs: baseline, Samples: len(vals),
			DeltaMs: deltaMs, DeltaPct: deltaPct, Verdict: verdict,
			Trend: trend,
		}
		if opts.Verdict != "" && opts.Verdict != verdict {
			continue
		}
		result.Items = append(result.Items, item)
	}

	// 排序：回归优先 → 增幅大优先 → 最新耗时大优先
	sort.SliceStable(result.Items, func(i, j int) bool {
		ri, rj := result.Items[i].Verdict == PerfRegressed, result.Items[j].Verdict == PerfRegressed
		if ri != rj {
			return ri
		}
		if result.Items[i].DeltaPct != result.Items[j].DeltaPct {
			return result.Items[i].DeltaPct > result.Items[j].DeltaPct
		}
		return result.Items[i].LatestMs > result.Items[j].LatestMs
	})

	result.Summary.Endpoints = len(result.Items)
	for _, it := range result.Items {
		switch it.Verdict {
		case PerfRegressed:
			result.Summary.Regressed++
		case PerfImproved:
			result.Summary.Improved++
		case PerfStable:
			result.Summary.Stable++
		default:
			result.Summary.Insufficient++
		}
	}
	return result, nil
}

// perfSample 是一条步骤样本（已归一化）。
type perfSample struct {
	RunID     uint64
	Method    string
	Path      string
	ElapsedMs int64
	CreatedAt time.Time
}

// loadSamples 拉窗口内样本并按接口归一化。
func (s *PerfService) loadSamples(projectID uint64, days int) ([]perfSample, error) {
	since := time.Now().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	type row struct {
		RunID           uint64
		FinalURL        string
		RequestSnapshot string
		ElapsedMs       int64
		CreatedAt       time.Time
	}
	var rows []row
	err := s.DB.Table("step_result").
		Select("step_result.run_id AS run_id, step_result.final_url AS final_url, "+
			"step_result.request_snapshot AS request_snapshot, step_result.elapsed_ms AS elapsed_ms, "+
			"run_record.created_at AS created_at").
		Joins("JOIN run_record ON run_record.id = step_result.run_id").
		Where("run_record.project_id = ?", projectID).
		Where("run_record.created_at >= ?", since).
		Where("step_result.elapsed_ms > 0").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("查询性能样本失败", err)
	}

	out := make([]perfSample, 0, len(rows))
	for _, r := range rows {
		path := normalizePerfPath(r.FinalURL)
		if path == "" || path == "/" {
			continue // 没有可识别路径的步骤（如 WebSocket / 平台侧失败）不参与接口级统计
		}
		out = append(out, perfSample{
			RunID:     r.RunID,
			Method:    methodFromSnapshot(r.RequestSnapshot),
			Path:      path,
			ElapsedMs: r.ElapsedMs,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 纯函数（单测重点）
// ---------------------------------------------------------------------------

var perfUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// normalizePerfPath 把最终 URL 归一成接口路径：
// 去协议/host/query/尾斜杠；纯数字段与 UUID 段替换成 `:id`。
//
// 不做归一的话，`/users/1` 与 `/users/2` 会被当成两个接口，
// 统计结果就是「每个接口各跑了一次」，毫无意义。
func normalizePerfPath(raw string) string {
	p := normalizeMockPath(raw) // 复用同一套去协议/host/query 的逻辑
	if p == "/" {
		return p
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, seg := range segs {
		if isAllDigits(seg) || perfUUIDRe.MatchString(seg) {
			segs[i] = ":id"
		}
	}
	return "/" + strings.Join(segs, "/")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// methodFromSnapshot 从报文快照里取请求方法；取不到返回空（不猜）。
func methodFromSnapshot(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	if v, ok := m["method"].(string); ok {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

// median 计算中位数（偶数取中间两个的平均，四舍五入）。
func median(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]int64, len(vals))
	copy(cp, vals)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2] + 1) / 2
}

// judgePerf 给出结论与变化量。
//
// 双阈值是误报控制的关键：只看百分比时 2ms→3ms（+50%）会被判成回归；
// 只看绝对值时 800ms→1100ms（+37.5%）反而漏报。两者都达标才算回归。
func judgePerf(latest, baseline int64, samples int, opts PerfOptions) (string, int64, float64) {
	if samples == 0 || baseline <= 0 {
		return PerfNoBaseline, 0, 0
	}
	delta := latest - baseline
	pct := float64(delta) / float64(baseline) * 100
	if samples < opts.MinSamples {
		return PerfInsufficient, delta, round1(pct)
	}
	if pct >= opts.ThresholdPct && delta >= opts.MinDeltaMs {
		return PerfRegressed, delta, round1(pct)
	}
	if pct <= -opts.ThresholdPct && -delta >= opts.MinDeltaMs {
		return PerfImproved, delta, round1(pct)
	}
	return PerfStable, delta, round1(pct)
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
