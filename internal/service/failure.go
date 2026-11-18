package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// FailureClusterService 把一次执行（或一段时间）里的失败按根因特征聚类（F13）。
//
// 设计要点：一次 200 条失败往往只有 3 个根因。聚类的价值不在于"分组好看"，
// 而在于把「先修哪个」变成显然的：计数最多的那簇就是优先级最高的那簇。
type FailureClusterService struct {
	Deps
}

// FailureScope 是查询范围。
type FailureScope struct {
	RunID uint64 `json:"run_id"`
	Days  int    `json:"days"`
}

// FailureCaseItem 是簇内的一条失败用例。
type FailureCaseItem struct {
	CaseID      uint64 `json:"case_id"`
	RunID       uint64 `json:"run_id"`
	CaseCode    string `json:"case_code"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Attribution string `json:"attribution"`
}

// FailureCluster 是一簇同因失败。
type FailureCluster struct {
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	Attribution string            `json:"attribution"`
	Hint        string            `json:"hint"`
	Count       int               `json:"count"`
	Cases       []FailureCaseItem `json:"cases"`
	Truncated   bool              `json:"truncated"`
}

// FailureClusterResult 是接口返回。
type FailureClusterResult struct {
	ProjectID        uint64           `json:"project_id"`
	Scope            FailureScope     `json:"scope"`
	GeneratedAt      string           `json:"generated_at"`
	TotalFailedCases int              `json:"total_failed_cases"`
	Clusters         []FailureCluster `json:"clusters"`
}

const failureCasesPerCluster = 50

// Clusters 做失败聚类。
func (s *FailureClusterService) Clusters(projectID uint64, runID uint64, days, limit int) (*FailureClusterResult, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	runIDs, err := s.scopeRunIDs(projectID, runID, days)
	if err != nil {
		return nil, err
	}
	res := &FailureClusterResult{
		ProjectID:   projectID,
		Scope:       FailureScope{RunID: runID, Days: days},
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Clusters:    []FailureCluster{},
	}
	if len(runIDs) == 0 {
		return res, nil
	}

	var failedCases []model.CaseResult
	if err := s.DB.Where("run_id IN ?", runIDs).
		Where("status IN ?", []string{model.StatusFail, model.StatusError}).
		Order("id asc").Find(&failedCases).Error; err != nil {
		return nil, errInternal("查询失败用例失败", err)
	}
	res.TotalFailedCases = len(failedCases)
	if len(failedCases) == 0 {
		return res, nil
	}

	var steps []model.StepResult
	if err := s.DB.Where("run_id IN ?", runIDs).
		Where("status IN ?", []string{model.StatusFail, model.StatusError}).
		Order("case_result_id asc, seq asc").Find(&steps).Error; err != nil {
		return nil, errInternal("查询失败步骤失败", err)
	}
	var asserts []model.AssertionResult
	if err := s.DB.Where("run_id IN ?", runIDs).Where("passed = ?", false).
		Order("step_result_id asc, seq asc").Find(&asserts).Error; err != nil {
		return nil, errInternal("查询失败断言失败", err)
	}

	stepByID := make(map[uint64]model.StepResult, len(steps))
	firstFailedStep := map[uint64]model.StepResult{} // case_result_id → 最早失败步骤
	for _, st := range steps {
		stepByID[st.ID] = st
		if _, ok := firstFailedStep[st.CaseResultID]; !ok {
			firstFailedStep[st.CaseResultID] = st
		}
	}
	firstFailedAssert := map[uint64]model.AssertionResult{} // case_result_id → 最早失败断言
	for _, a := range asserts {
		st, ok := stepByID[a.StepResultID]
		if !ok {
			continue
		}
		if _, ok := firstFailedAssert[st.CaseResultID]; !ok {
			firstFailedAssert[st.CaseResultID] = a
		}
	}

	grouped := map[string]*FailureCluster{}
	for _, c := range failedCases {
		sig := classifyFailure(c, firstFailedStep[c.ID], firstFailedAssert[c.ID])
		cl := grouped[sig.Key]
		if cl == nil {
			cl = &FailureCluster{Key: sig.Key, Title: sig.Title, Attribution: c.Attribution,
				Hint: sig.Hint, Cases: []FailureCaseItem{}}
			grouped[sig.Key] = cl
		}
		cl.Count++
		if len(cl.Cases) < failureCasesPerCluster {
			cl.Cases = append(cl.Cases, FailureCaseItem{
				CaseID: c.CaseID, RunID: c.RunID, CaseCode: c.CaseCode,
				Name: c.ConfigName, Status: c.Status, Detail: sig.Detail, Attribution: c.Attribution,
			})
		} else {
			cl.Truncated = true
		}
	}

	for _, cl := range grouped {
		res.Clusters = append(res.Clusters, *cl)
	}
	sort.SliceStable(res.Clusters, func(i, j int) bool {
		if res.Clusters[i].Count != res.Clusters[j].Count {
			return res.Clusters[i].Count > res.Clusters[j].Count
		}
		return res.Clusters[i].Title < res.Clusters[j].Title
	})
	if len(res.Clusters) > limit {
		res.Clusters = res.Clusters[:limit]
	}
	return res, nil
}

// scopeRunIDs 解析查询范围：单次执行，或时间窗口内某项目的执行。
func (s *FailureClusterService) scopeRunIDs(projectID, runID uint64, days int) ([]uint64, error) {
	if runID > 0 {
		var run struct {
			ID        uint64
			ProjectID uint64
		}
		if err := s.DB.Table("run_record").Select("id, project_id").Where("id = ?", runID).Scan(&run).Error; err != nil {
			return nil, errInternal("查询执行记录失败", err)
		}
		if run.ID == 0 {
			return nil, errNotFound("执行 %d 不存在", runID)
		}
		if run.ProjectID != projectID {
			return nil, errBadParam("执行 %d 不属于该项目", runID)
		}
		return []uint64{runID}, nil
	}
	since := time.Now().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	var ids []uint64
	if err := s.DB.Model(&model.RunRecord{}).
		Where("project_id = ? AND created_at >= ?", projectID, since).
		Pluck("id", &ids).Error; err != nil {
		return nil, errInternal("查询执行记录失败", err)
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// 分类（纯函数，单测重点）
// ---------------------------------------------------------------------------

type failureSignature struct {
	Key    string
	Title  string
	Hint   string
	Detail string
}

// classifyFailure 按四级特征给失败分类。
//
// 优先级：失败断言 → HTTP 状态码（≥400）→ 步骤错误前缀 → 归因。
// 顺序有意义：断言失败最能说明「哪条契约不满足」，而状态码/错误前缀是退而求其次。
func classifyFailure(c model.CaseResult, step model.StepResult, assert model.AssertionResult) failureSignature {
	if assert.CheckExpr != "" {
		expect := clip(assert.ExpectValue, 40)
		return failureSignature{
			Key:    fmt.Sprintf("assert|%s|%s|%s", assert.CheckExpr, assert.AssertMethod, expect),
			Title:  fmt.Sprintf("断言失败：%s %s %s", assert.CheckExpr, assert.AssertMethod, expect),
			Hint:   "同一条断言在多条用例上同时失败，通常是被测接口契约变了或测试数据不对；先比对实际值与期望值",
			Detail: fmt.Sprintf("期望 %s，实际 %s", expect, clip(assert.CheckValue, 40)),
		}
	}

	if code := responseStatusCode(step.ResponseSnapshot); code >= 400 {
		ep := endpointLabel(step)
		return failureSignature{
			Key:    fmt.Sprintf("http|%d|%s", code, ep),
			Title:  fmt.Sprintf("HTTP %d · %s", code, ep),
			Hint:   httpHint(code),
			Detail: fmt.Sprintf("HTTP %d", code),
		}
	}

	if msg := strings.TrimSpace(step.ErrorMsg); msg != "" {
		cls := normalizeErrorClass(msg)
		return failureSignature{
			Key:    "step|" + cls,
			Title:  "步骤失败：" + clip(msg, 80),
			Hint:   "请求没有完成（连接/超时/DNS），先确认被测服务在跑、地址与网络可达",
			Detail: clip(msg, 120),
		}
	}

	// 退到归因
	attr := c.Attribution
	if attr == "" {
		attr = model.AttrUnknown
	}
	return failureSignature{
		Key:    "attr|" + attr,
		Title:  "归因：" + model.AttributionLabel(attr),
		Hint:   model.AttributionReasonWith(attr, -1),
		Detail: clip(c.ErrorMsg, 120),
	}
}

func httpHint(code int) string {
	switch {
	case code >= 500:
		return "服务端错误集中出现：先看被测服务是否健康、依赖是否可用，再逐条查用例"
	case code == 401 || code == 403:
		return "鉴权失败集中出现：检查环境里的认证配置（token 是否过期）与请求头是否带上"
	case code == 404:
		return "路径不存在集中出现：检查接口路径是否变更、base_url 是否指错环境"
	default:
		return "请求被拒集中出现：优先检查参数/请求体/业务前置数据是否一致"
	}
}

// responseStatusCode 从报文快照里读状态码；读不到返回 0（不猜）。
func responseStatusCode(snapshot jsonx.Any) int {
	if snapshot.Val == nil {
		return 0
	}
	b, err := json.Marshal(snapshot.Val)
	if err != nil {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return 0
	}
	switch v := m["status_code"].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// endpointLabel 生成「METHOD /path」标签（路径口径与性能页一致）。
func endpointLabel(step model.StepResult) string {
	path := normalizePerfPath(step.FinalURL)
	if path == "" {
		return "(未知路径)"
	}
	method := ""
	if step.RequestSnapshot.Val != nil {
		if b, err := json.Marshal(step.RequestSnapshot.Val); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if s, ok := m["method"].(string); ok {
					method = strings.ToUpper(s)
				}
			}
		}
	}
	return strings.TrimSpace(method + " " + path)
}

var numberRe = regexp.MustCompile(`\d+`)

// normalizeErrorClass 把错误信息归一成"类型"：去掉数字与长串，避免每个请求都成新簇。
func normalizeErrorClass(msg string) string {
	s := strings.ToLower(strings.TrimSpace(msg))
	s = strings.TrimPrefix(s, "step failed: ")
	s = numberRe.ReplaceAllString(s, "N")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
