package service

import (
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// StatsService 提供统计看板（M4）的聚合查询。
//
// 全部只读：基于 run_record + case_result 现有数据做聚合，不新增表、不写库。
// 聚合刻意放在 Go 内存里做（而非裸 SQL 的日期/分组函数），
// 这样 mysql 与 sqlite 两种驱动下行为一致，且看板查询本身有时间窗口限制、数据量可控。
type StatsService struct {
	DB *gorm.DB
}

func NewStatsService(d Deps) *StatsService {
	return &StatsService{DB: d.DB}
}

// TrendPoint 是趋势图上的一个点（按天聚合）。
type TrendPoint struct {
	Day     string  `json:"day"` // 形如 2006-01-02
	Total   int     `json:"total"`
	Passed  int     `json:"passed"`
	Failed  int     `json:"failed"`
	Error   int     `json:"error"`
	Rate    float64 `json:"rate"`    // 通过率（passed/total，total=0 时为 0）
	AvgMs   int64   `json:"avg_ms"`  // 平均耗时
}

// FlakyCase 是不稳定/常败用例排行里的一项（按 case_code 聚合）。
type FlakyCase struct {
	CaseID     uint64  `json:"case_id"`
	CaseCode   string  `json:"case_code"`
	ConfigName string  `json:"config_name"`
	Runs       int     `json:"runs"`
	Passed     int     `json:"passed"`
	Rate       float64 `json:"rate"` // 通过率（passed/runs）
}

// SlowCase 是慢用例排行里的一项（按 case_code 聚合）。
type SlowCase struct {
	CaseID     uint64  `json:"case_id"`
	CaseCode   string  `json:"case_code"`
	ConfigName string  `json:"config_name"`
	Runs       int     `json:"runs"`
	AvgMs      int64   `json:"avg_ms"`
	MaxMs      int64   `json:"max_ms"`
}

// StatsQuery 是统计查询的公共参数。
type StatsQuery struct {
	ProjectID uint64
	Days      int // 最近 N 天（含今天）。<=0 默认 30。
}

func (q StatsQuery) days() int {
	if q.Days <= 0 {
		return 30
	}
	if q.Days > 365 {
		return 365
	}
	return q.Days
}

// Trend 返回最近 N 天的通过率趋势（按天聚合，含没有执行的空白天补零）。
func (s *StatsService) Trend(q StatsQuery) ([]TrendPoint, error) {
	days := q.days()
	since := time.Now().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	var runs []model.RunRecord
	err := s.DB.Model(&model.RunRecord{}).
		Where("project_id = ?", q.ProjectID).
		Where("created_at >= ?", since).
		Where("status IN ?", []string{"success", "failed", "error"}).
		Find(&runs).Error
	if err != nil {
		return nil, errInternal("查询趋势数据失败", err)
	}

	// 按天聚合。map key 用 "2006-01-02"。
	type agg struct {
		total, passed, failed, errc int
		msum                        int64
	}
	byDay := map[string]*agg{}
	for _, r := range runs {
		key := r.CreatedAt.Format("2006-01-02")
		a := byDay[key]
		if a == nil {
			a = &agg{}
			byDay[key] = a
		}
		a.total++
		a.passed += r.Passed
		a.failed += r.Failed
		a.errc += r.Error
		a.msum += r.DurationMs
	}

	// 生成连续日期序列（含无执行的空白天），保证折线图横轴连续。
	points := make([]TrendPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		a := byDay[day]
		p := TrendPoint{Day: day}
		if a != nil {
			p.Total = a.total
			p.Passed = a.passed
			p.Failed = a.failed
			p.Error = a.errc
			if a.total > 0 {
				p.Rate = float64(a.passed) / float64(a.total)
				p.AvgMs = a.msum / int64(a.total)
			}
		}
		points = append(points, p)
	}
	return points, nil
}

// Flaky 返回「不稳定用例」排行：按 case_code 聚合，通过率落在 (0,1) 区间内的
// 用例按通过率升序（越不稳定越靠前）。通过率 = 0（全败）的用例也归入，但
// 与「偶尔失败」区分开——排序键是「通过率升序 + 执行次数降序」。
//
// 所谓「不稳定」（flaky）：不是每次都失败，而是时好时坏。这里用通过率衡量，
// 通过率越低越不稳定；纯 100% 通过或纯 0 次失败的用例不算「不稳定」，
// 但全败用例仍值得单列，故一并返回、由前端按 rate 分档着色。
func (s *StatsService) Flaky(q StatsQuery, limit int) ([]FlakyCase, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	runIDs, err := s.projectRunIDs(q)
	if err != nil {
		return nil, err
	}
	if len(runIDs) == 0 {
		return []FlakyCase{}, nil
	}

	type row struct {
		CaseID     uint64
		CaseCode   string
		ConfigName string
		Runs       int64
		Passed     int64
	}
	var rows []row
	err = s.DB.Model(&model.CaseResult{}).
		Select("case_id, case_code, config_name, COUNT(*) AS runs, SUM(CASE WHEN status = 'pass' THEN 1 ELSE 0 END) AS passed").
		Where("run_id IN ?", runIDs).
		Group("case_code").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("查询不稳定用例失败", err)
	}

	out := make([]FlakyCase, 0, len(rows))
	for _, r := range rows {
		if r.Runs == 0 {
			continue
		}
		rate := float64(r.Passed) / float64(r.Runs)
		// 全过（rate=1）的用例稳定且健康，不进「需要关注」的榜单；
		// 全败（rate=0）与时好时坏（0<rate<1）都留下，由前端按 rate 分档着色。
		if rate >= 1 {
			continue
		}
		out = append(out, FlakyCase{
			CaseID:     r.CaseID,
			CaseCode:   r.CaseCode,
			ConfigName: r.ConfigName,
			Runs:       int(r.Runs),
			Passed:     int(r.Passed),
			Rate:       rate,
		})
	}
	// 排序：通过率升序（越低越靠前），同通过率按执行次数降序。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rate != out[j].Rate {
			return out[i].Rate < out[j].Rate
		}
		return out[i].Runs > out[j].Runs
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Slowest 返回「慢用例」排行：按 case_code 聚合，平均耗时降序。
func (s *StatsService) Slowest(q StatsQuery, limit int) ([]SlowCase, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	runIDs, err := s.projectRunIDs(q)
	if err != nil {
		return nil, err
	}
	if len(runIDs) == 0 {
		return []SlowCase{}, nil
	}

	type row struct {
		CaseID     uint64
		CaseCode   string
		ConfigName string
		Runs       int64
		AvgMs      float64
		MaxMs      int64
	}
	var rows []row
	err = s.DB.Model(&model.CaseResult{}).
		Select("case_id, case_code, config_name, COUNT(*) AS runs, AVG(duration_ms) AS avg_ms, MAX(duration_ms) AS max_ms").
		Where("run_id IN ?", runIDs).
		Group("case_code").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("查询慢用例失败", err)
	}

	out := make([]SlowCase, 0, len(rows))
	for _, r := range rows {
		out = append(out, SlowCase{
			CaseID:     r.CaseID,
			CaseCode:   r.CaseCode,
			ConfigName: r.ConfigName,
			Runs:       int(r.Runs),
			AvgMs:      int64(r.AvgMs),
			MaxMs:      r.MaxMs,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AvgMs != out[j].AvgMs {
			return out[i].AvgMs > out[j].AvgMs
		}
		return out[i].MaxMs > out[j].MaxMs
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// projectRunIDs 返回项目在时间窗口内、已结束的执行记录 ID 集合。
func (s *StatsService) projectRunIDs(q StatsQuery) ([]uint64, error) {
	days := q.days()
	since := time.Now().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	var ids []uint64
	err := s.DB.Model(&model.RunRecord{}).
		Where("project_id = ?", q.ProjectID).
		Where("created_at >= ?", since).
		Where("status IN ?", []string{"success", "failed", "error"}).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, errInternal("查询项目执行记录失败", err)
	}
	return ids, nil
}
