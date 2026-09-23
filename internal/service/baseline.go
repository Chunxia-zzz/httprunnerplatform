package service

import (
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// BaselineService 提供「用例基线对比」（M4-d）的查询。
//
// 语义：选定一条用例，拉取它最近 N 次执行结果并排对比，帮助回答
// 「这条用例是从哪一次开始变坏的」。全部只读，基于现有 run_record +
// case_result + step_result 历史，不新增表、不写库。
//
// 对比维度：
//   - 每次执行的状态（pass / fail / error）与归因
//   - 每次执行的耗时（duration_ms）
//   - 每次执行的步骤通过数 / 失败数（从 step_result 聚合）
//   - 相邻两次之间的「差异信号」（是否由好变坏、耗时是否显著上升）
type BaselineService struct {
	DB *gorm.DB
}

func NewBaselineService(d Deps) *BaselineService {
	return &BaselineService{DB: d.DB}
}

// BaselineQuery 是基线对比的查询参数。
type BaselineQuery struct {
	ProjectID uint64
	CaseID    uint64
	Limit     int // 最近 N 次（含本次）。<=0 默认 5。
}

func (q BaselineQuery) limit() int {
	if q.Limit <= 0 {
		return 5
	}
	if q.Limit > 30 {
		return 30
	}
	return q.Limit
}

// BaselineRun 是某一次执行里该用例的结果快照。
type BaselineRun struct {
	RunID       uint64 `json:"run_id"`
	RunStatus   string `json:"run_status"` // 整次执行的终态（success/failed/error/canceled）
	TriggerType string `json:"trigger_type"`
	FinishedAt  string `json:"finished_at"` // RFC3339；为空表示还在跑
	Status      string `json:"status"`      // 该用例的结果状态
	Attribution string `json:"attribution"`
	DurationMs  int64  `json:"duration_ms"`
	StepTotal   int    `json:"step_total"`
	StepPassed  int    `json:"step_passed"`
	StepFailed  int    `json:"step_failed"`
	StepError   int    `json:"step_error"`
	ErrorMsg    string `json:"error_msg"`
	// Delta 描述与「前一次」的差异信号，第 0 项（最早）为 nil。
	Delta *BaselineDelta `json:"delta,omitempty"`
}

// BaselineDelta 是相邻两次执行之间的差异信号。
//
// 只做「事实陈述」而非自动下结论：是否「变坏」由前端结合状态与
// 归因共同呈现，避免后端替用户拍板。
type BaselineDelta struct {
	// Broke 为 true 表示「前一次通过、这一次失败/出错」—— 典型的最先变坏点。
	Broke bool `json:"broke"`
	// Recovered 为 true 表示「前一次失败/出错、这一次通过」。
	Recovered bool `json:"recovered"`
	// DurDeltaMs 是本次耗时 - 上次耗时（正数=变慢）。
	DurDeltaMs int64 `json:"dur_delta_ms"`
	// StepPassDelta 是本次通过步骤数 - 上次通过步骤数。
	StepPassDelta int `json:"step_pass_delta"`
}

// BaselineResult 是一次基线对比的完整返回。
type BaselineResult struct {
	CaseID     uint64        `json:"case_id"`
	CaseCode   string        `json:"case_code"`
	ConfigName string        `json:"config_name"`
	TotalRuns  int           `json:"total_runs"` // 该项目下该用例的历史执行总次数
	Runs       []BaselineRun `json:"runs"`       // 最近 N 次，按时间从旧到新
}

// Baseline 返回指定用例的最近 N 次执行对比。
//
// 排序约定：返回的 Runs 按时间从旧到新（最早在前），前端直接按序渲染，
// 并用 Delta 标记相邻变化。
func (s *BaselineService) Baseline(q BaselineQuery) (*BaselineResult, error) {
	if q.CaseID == 0 {
		return nil, errBadParam("case_id 不能为空")
	}
	tc, err := loadCase(s.DB, q.CaseID)
	if err != nil {
		return nil, err
	}
	if tc.ProjectID != q.ProjectID {
		return nil, errBadParam("用例 %q 不属于项目 %d", tc.Code, q.ProjectID)
	}

	limit := q.limit()

	// 1) 该用例的全部 case_result（按 run_id 降序即时间从新到旧），
	//    一次性取完再在内存里截取，避免「先取 run 再逐条查 case」的 N+1。
	//    同时拿到 run_id 集合去补执行级信息（终态、触发来源、结束时间）。
	var all []model.CaseResult
	err = s.DB.Model(&model.CaseResult{}).
		Where("case_id = ?", q.CaseID).
		Order("id desc").
		Find(&all).Error
	if err != nil {
		return nil, errInternal("查询用例历史结果失败", err)
	}

	total := len(all)
	res := &BaselineResult{
		CaseID:     q.CaseID,
		CaseCode:   tc.Code,
		ConfigName: tc.Name,
		TotalRuns:  total,
		Runs:       []BaselineRun{},
	}
	if total == 0 {
		return res, nil
	}

	// 截取最近 N 次。
	recent := all
	if len(recent) > limit {
		recent = all[:limit]
	}

	// 2) 批量补执行级信息。
	runIDs := make([]uint64, 0, len(recent))
	for _, cr := range recent {
		runIDs = append(runIDs, cr.RunID)
	}
	runMap := map[uint64]model.RunRecord{}
	var runs []model.RunRecord
	if err := s.DB.Where("id IN ?", runIDs).Find(&runs).Error; err != nil {
		return nil, errInternal("查询执行记录失败", err)
	}
	for i := range runs {
		runMap[runs[i].ID] = runs[i]
	}

	// 3) 批量补步骤失败/出错数（一次 GROUP BY，避免 N+1）。
	caseResultIDs := make([]uint64, 0, len(recent))
	for _, cr := range recent {
		caseResultIDs = append(caseResultIDs, cr.ID)
	}
	stepCounts, err := s.baselineStepCounts(caseResultIDs)
	if err != nil {
		return nil, err
	}

	// 4) 组装（recent 是从新到旧，需反转成从旧到新再算 Delta）。
	out := make([]BaselineRun, 0, len(recent))
	for i := len(recent) - 1; i >= 0; i-- {
		cr := recent[i]
		run := runMap[cr.RunID]
		c := stepCounts[cr.ID]
		out = append(out, BaselineRun{
			RunID:       cr.RunID,
			RunStatus:   run.Status,
			TriggerType: run.TriggerType,
			FinishedAt:  formatNullableTime(run.FinishedAt),
			Status:      cr.Status,
			Attribution: cr.Attribution,
			DurationMs:  cr.DurationMs,
			StepTotal:   cr.StepTotal,
			StepPassed:  cr.StepPassed,
			StepFailed:  c.Failed,
			StepError:   c.Error,
			ErrorMsg:    cr.ErrorMsg,
		})
	}

	// 5) 相邻对比。
	for i := 1; i < len(out); i++ {
		prev, cur := out[i-1], out[i]
		d := &BaselineDelta{
			Broke:         passLike(prev.Status) && !passLike(cur.Status),
			Recovered:     !passLike(prev.Status) && passLike(cur.Status),
			DurDeltaMs:    cur.DurationMs - prev.DurationMs,
			StepPassDelta: cur.StepPassed - prev.StepPassed,
		}
		out[i].Delta = d
	}

	res.Runs = out
	return res, nil
}

// baselineStepCounts 统计一批用例结果里各自 fail / error 的步骤数。
func (s *BaselineService) baselineStepCounts(caseResultIDs []uint64) (map[uint64]stepCounts, error) {
	out := map[uint64]stepCounts{}
	if len(caseResultIDs) == 0 {
		return out, nil
	}
	type row struct {
		CaseResultID uint64 `gorm:"column:case_result_id"`
		Status       string `gorm:"column:status"`
		N            int    `gorm:"column:n"`
	}
	var rows []row
	err := s.DB.Model(&model.StepResult{}).
		Select("case_result_id, status, count(*) AS n").
		Where("case_result_id IN ?", caseResultIDs).
		Group("case_result_id, status").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("统计步骤状态失败", err)
	}
	for _, r := range rows {
		c := out[r.CaseResultID]
		switch r.Status {
		case model.StatusFail:
			c.Failed += r.N
		case model.StatusError:
			c.Error += r.N
		}
		out[r.CaseResultID] = c
	}
	return out, nil
}

// passLike 判断一个结果状态是否属于「通过」。
//
// 只用 status 判断（而不是 run 终态）：一条用例结果本身只有 pass/fail/error/
// skipped 四档，pass 才叫通过。skipped 不算通过也不算失败，避免误标「变坏」。
func passLike(status string) bool {
	return status == model.StatusPass
}

// formatNullableTime 把可空时间转成 RFC3339 字符串，nil 返回空串。
func formatNullableTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
