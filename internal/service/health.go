package service

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// HealthService 做测试资产健康度体检（F12）。
//
// 三条设计约定：
//   - **只读、按需计算**：不建调度任务、不缓存——30 人团队的项目数据量下现算最省事，
//     也避免"报告是三天前的"这种信任问题；
//   - **只给清单与建议，不自动删任何东西**：删用例/接口的风险远大于收益；
//   - 评分必须可解释：每个扣分项都能说出「哪几条、为什么」。
type HealthService struct {
	Deps
}

// 资产问题类型。
const (
	HealthNeverRun      = "never_run"
	HealthStale         = "stale"
	HealthNoAssert      = "no_assert"
	HealthEmptyCase     = "empty_case"
	HealthUnusedAPI     = "unused_api"
	HealthDuplicateCase = "duplicate_case"
)

// 严重度与扣分。
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

var severityPenalty = map[string]float64{
	SeverityHigh:   3,
	SeverityMedium: 1.5,
	SeverityLow:    0.5,
}

// HealthItem 是清单里的一条。
type HealthItem struct {
	EntityType string `json:"entity_type"` // case / api
	EntityID   uint64 `json:"entity_id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Detail     string `json:"detail"`
}

// HealthCategory 是一类问题。
type HealthCategory struct {
	Key      string       `json:"key"`
	Label    string       `json:"label"`
	Severity string       `json:"severity"`
	Hint     string       `json:"hint"`
	Count    int          `json:"count"`
	Items    []HealthItem `json:"items"`
}

// HealthDeduction 是一条扣分明细。
type HealthDeduction struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Count    int     `json:"count"`
	Penalty  float64 `json:"penalty"`
	Severity string  `json:"severity"`
}

// HealthReport 是体检报告。
type HealthReport struct {
	ProjectID   uint64            `json:"project_id"`
	Score       float64           `json:"score"`
	GeneratedAt string            `json:"generated_at"`
	Applied     HealthApplied     `json:"applied"`
	Deductions  []HealthDeduction `json:"deductions"`
	Categories  []HealthCategory  `json:"categories"`
	Summary     map[string]int    `json:"summary"`
}

// HealthApplied 回显实际生效的参数。
type HealthApplied struct {
	Days int `json:"days"`
}

// HealthOptions 是查询参数。
type HealthOptions struct {
	Days int
}

func (o *HealthOptions) clamp() {
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Days > 3650 {
		o.Days = 3650
	}
}

// Analyze 生成健康度报告。
func (s *HealthService) Analyze(projectID uint64, opts HealthOptions) (*HealthReport, error) {
	opts.clamp()
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}

	var cases []model.TestCase
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&cases).Error; err != nil {
		return nil, errInternal("查询用例失败", err)
	}
	var steps []model.TestStep
	if err := s.DB.Where("case_id IN (?)", s.DB.Model(&model.TestCase{}).Select("id").Where("project_id = ?", projectID)).
		Order("case_id asc, seq asc").Find(&steps).Error; err != nil {
		return nil, errInternal("查询步骤失败", err)
	}
	var apis []model.APIDef
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&apis).Error; err != nil {
		return nil, errInternal("查询接口失败", err)
	}

	// 执行时间：只取用例的 last_run_id 对应的记录
	runAt := map[uint64]time.Time{}
	var runIDs []uint64
	for _, c := range cases {
		if c.LastRunID > 0 {
			runIDs = append(runIDs, c.LastRunID)
		}
	}
	if len(runIDs) > 0 {
		type row struct {
			ID        uint64
			CreatedAt time.Time
		}
		var rows []row
		if err := s.DB.Table("run_record").Select("id, created_at").Where("id IN ?", runIDs).Scan(&rows).Error; err != nil {
			return nil, errInternal("查询执行记录失败", err)
		}
		for _, r := range rows {
			runAt[r.ID] = r.CreatedAt
		}
	}

	// 步骤按用例分组
	stepsByCase := map[uint64][]model.TestStep{}
	for _, st := range steps {
		stepsByCase[st.CaseID] = append(stepsByCase[st.CaseID], st)
	}

	since := time.Now().AddDate(0, 0, -opts.Days)
	referencedAPIs := map[uint64]bool{}
	var neverRun, stale, noAssert, emptyCase []HealthItem
	fingerprints := map[string][]HealthItem{}

	for _, c := range cases {
		cs := stepsByCase[c.ID]
		enabled := make([]model.TestStep, 0, len(cs))
		assertCount := 0
		for _, st := range cs {
			if !st.Enabled {
				continue
			}
			enabled = append(enabled, st)
			assertCount += len(st.Validate)
			if st.StepType == model.StepAPI && st.RefAPIID > 0 {
				referencedAPIs[st.RefAPIID] = true
			}
		}

		item := HealthItem{EntityType: "case", EntityID: c.ID, Key: c.Code, Name: c.Name}

		// 空壳：没有启用步骤
		if len(enabled) == 0 {
			item.Detail = "没有任何启用的步骤（编译产物里不会有它）"
			emptyCase = append(emptyCase, item)
			continue
		}
		// 无断言
		if assertCount == 0 {
			it := item
			it.Detail = fmt.Sprintf("%d 个启用步骤都没有断言，跑通也不代表结果正确", len(enabled))
			noAssert = append(noAssert, it)
		}
		// 执行情况：从未执行 / 长期未执行（互斥）
		if c.LastRunID == 0 {
			it := item
			it.Detail = "从未执行过"
			neverRun = append(neverRun, it)
		} else if at, ok := runAt[c.LastRunID]; ok && at.Before(since) {
			it := item
			it.Detail = "最后执行于 " + at.Format("2006-01-02")
			stale = append(stale, it)
		}

		// 重复用例：结构指纹
		fp := caseFingerprint(enabled)
		if fp != "" {
			fingerprints[fp] = append(fingerprints[fp], item)
		}
	}

	// 未被引用的接口
	var unusedAPI []HealthItem
	for _, a := range apis {
		if referencedAPIs[a.ID] {
			continue
		}
		unusedAPI = append(unusedAPI, HealthItem{
			EntityType: "api", EntityID: a.ID, Key: a.Code, Name: a.Name,
			Detail: "没有被任何用例步骤引用",
		})
	}

	// 重复用例分组：每组内每条都算一项（点击可跳转）
	var duplicate []HealthItem
	for _, group := range fingerprints {
		if len(group) < 2 {
			continue
		}
		others := make([]string, 0, len(group))
		for _, it := range group {
			others = append(others, it.Key)
		}
		for _, it := range group {
			var rest []string
			for _, o := range others {
				if o != it.Key {
					rest = append(rest, o)
				}
			}
			it.Detail = fmt.Sprintf("与 %d 条用例的步骤/断言结构完全相同：%s",
				len(rest), strings.Join(rest, "、"))
			duplicate = append(duplicate, it)
		}
	}

	cats := []HealthCategory{
		{Key: HealthEmptyCase, Label: "空壳用例", Severity: SeverityHigh,
			Hint: "没有启用的步骤，执行时根本不会产生请求；建议补步骤或删除", Items: emptyCase},
		{Key: HealthNoAssert, Label: "没有断言的用例", Severity: SeverityHigh,
			Hint: "只能证明接口通，不能证明结果对；这是「假绿」的主要来源", Items: noAssert},
		{Key: HealthDuplicateCase, Label: "重复用例", Severity: SeverityMedium,
			Hint: "步骤与断言结构完全一致，维护时容易只改一处；建议合并或参数化", Items: duplicate},
		{Key: HealthStale, Label: "长期未执行的用例", Severity: SeverityMedium,
			Hint: "接口可能已经变了，用例却还在原地；建议回归一次或归档", Items: stale},
		{Key: HealthNeverRun, Label: "从未执行的用例", Severity: SeverityLow,
			Hint: "写完没跑过就没被验证过；建议先手动执行一次", Items: neverRun},
		{Key: HealthUnusedAPI, Label: "未被引用的接口定义", Severity: SeverityLow,
			Hint: "接口库里有、用例里没有；可能是闲置资产，也可能是测试缺口", Items: unusedAPI},
	}

	report := &HealthReport{
		ProjectID:   projectID,
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Summary:     map[string]int{},
	}
	report.Applied.Days = opts.Days

	score := 100.0
	for i := range cats {
		c := &cats[i]
		sort.Slice(c.Items, func(a, b int) bool { return c.Items[a].Key < c.Items[b].Key })
		c.Count = len(c.Items)
		report.Summary[c.Key] = c.Count
		if c.Count > 0 {
			penalty := severityPenalty[c.Severity] * float64(c.Count)
			score -= penalty
			report.Deductions = append(report.Deductions, HealthDeduction{
				Key: c.Key, Label: c.Label, Count: c.Count,
				Penalty: round1(penalty), Severity: c.Severity,
			})
		}
	}
	if score < 0 {
		score = 0
	}
	report.Score = round1(score)
	report.Categories = cats
	return report, nil
}

// decodeRequestSpecForHealth 把步骤的 request 列解析成 RequestSpec。
//
// service 包不依赖 compiler/validator（那两个包反过来依赖 model），
// 所以这里用 JSON 中转，保持分层方向不变。
func decodeRequestSpecForHealth(a jsonx.Any) (model.RequestSpec, error) {
	var spec model.RequestSpec
	if a.Val == nil {
		return spec, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return spec, err
	}
	err = json.Unmarshal(b, &spec)
	return spec, err
}

// caseFingerprint 生成用例的结构指纹。
//
// 只看「启用步骤的方法/URL/引用接口 + 断言检查表达式」：
// 名称、注释、变量值都不参与——改了名字不代表逻辑变了，而改了断言才是。
func caseFingerprint(steps []model.TestStep) string {
	if len(steps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, st := range steps {
		b.WriteString(st.StepType)
		b.WriteString("|")
		if st.StepType == model.StepAPI {
			fmt.Fprintf(&b, "ref:%d", st.RefAPIID)
		} else if req, err := decodeRequestSpecForHealth(st.Request); err == nil {
			b.WriteString(strings.ToUpper(req.Method))
			b.WriteString(" ")
			b.WriteString(req.URL)
		} else {
			b.WriteString(st.WsOpType + " " + st.WsURL)
		}
		for _, a := range st.Validate {
			b.WriteString("|")
			b.WriteString(a.Check)
			b.WriteString(" ")
			b.WriteString(a.Assert)
		}
		b.WriteString("\n")
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:12]
}
