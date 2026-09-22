package service

import (
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/compiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/validator"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// CaseService 负责用例与步骤的增删改查、YAML 预览与静态校验。
type CaseService struct{ Deps }

// ---------------------------------------------------------------------------
// 列表与树
// ---------------------------------------------------------------------------

// CaseListQuery 是用例列表的过滤条件。
type CaseListQuery struct {
	Module   string
	Priority string
	Status   string
	Keyword  string
}

// CaseListItem 是列表项。
//
// 刻意比 model.TestCase 薄：列表接口不返回 config / description 这类
// 只有编辑器才需要的内容，避免用例多起来之后列表接口变成一个几 MB 的响应。
type CaseListItem struct {
	ID         uint64    `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Module     string    `json:"module"`
	Priority   string    `json:"priority"`
	Tags       string    `json:"tags"`
	Status     string    `json:"status"`
	StepCount  int64     `json:"step_count"`
	LastRunID  uint64    `json:"last_run_id"`
	LastStatus string    `json:"last_status"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ModuleCount 是左侧树的节点。
type ModuleCount struct {
	Module string `json:"module"`
	Count  int64  `json:"count"`
}

// List 返回用例分页列表，支持 module / priority / status / keyword 过滤。
func (s *CaseService) List(projectID uint64, q CaseListQuery, page Page) ([]CaseListItem, int64, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, 0, err
	}

	tx := s.DB.Model(&model.TestCase{}).Where("project_id = ?", projectID)
	if v := strings.TrimSpace(q.Module); v != "" {
		tx = tx.Where("module = ?", v)
	}
	if v := strings.TrimSpace(q.Priority); v != "" {
		tx = tx.Where("priority = ?", v)
	}
	if v := strings.TrimSpace(q.Status); v != "" {
		tx = tx.Where("status = ?", v)
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		like := "%" + kw + "%"
		tx = tx.Where("code LIKE ? OR name LIKE ? OR tags LIKE ?", like, like, like)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计用例数失败", err)
	}

	page = page.Normalize()
	var cases []model.TestCase
	if err := tx.Order("id desc").Offset(page.Offset()).Limit(page.Limit()).Find(&cases).Error; err != nil {
		return nil, 0, errInternal("查询用例列表失败", err)
	}
	if len(cases) == 0 {
		return []CaseListItem{}, total, nil
	}

	ids := make([]uint64, 0, len(cases))
	for i := range cases {
		ids = append(ids, cases[i].ID)
	}
	counts, err := enabledStepCounts(s.DB, ids)
	if err != nil {
		return nil, 0, err
	}

	list := make([]CaseListItem, 0, len(cases))
	for i := range cases {
		c := &cases[i]
		list = append(list, CaseListItem{
			ID:         c.ID,
			Code:       c.Code,
			Name:       c.Name,
			Module:     c.Module,
			Priority:   c.Priority,
			Tags:       c.Tags,
			Status:     c.Status,
			StepCount:  counts[c.ID],
			LastRunID:  c.LastRunID,
			LastStatus: c.LastStatus,
			UpdatedAt:  c.UpdatedAt,
		})
	}
	return list, total, nil
}

// enabledStepCounts 统计一批用例的**启用**步骤数。
//
// 为什么是"启用"而不是"全部"：列表上的步骤数是用户判断
// "这条用例到底会跑多少东西"的依据。如果一条用例的步骤全被禁用，
// 显示 3 会让用户以为它有 3 步会执行，而实际一步都不会走 ——
// 那和"假绿"是同一种误导。
func enabledStepCounts(db *gorm.DB, caseIDs []uint64) (map[uint64]int64, error) {
	if len(caseIDs) == 0 {
		return map[uint64]int64{}, nil
	}
	var rows []struct {
		CaseID uint64
		N      int64
	}
	err := db.Model(&model.TestStep{}).
		Select("case_id, COUNT(*) AS n").
		Where("case_id IN ? AND enabled = ?", caseIDs, true).
		Group("case_id").Scan(&rows).Error
	if err != nil {
		return nil, errInternal("统计步骤数失败", err)
	}
	out := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		out[r.CaseID] = r.N
	}
	return out, nil
}

// Tree 按 module 分组统计，供左侧树使用。
func (s *CaseService) Tree(projectID uint64) ([]ModuleCount, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	var rows []struct {
		Module string
		N      int64
	}
	err := s.DB.Model(&model.TestCase{}).
		Select("module, COUNT(*) AS n").
		Where("project_id = ?", projectID).
		Group("module").Order("module asc").Scan(&rows).Error
	if err != nil {
		return nil, errInternal("统计用例模块失败", err)
	}
	out := make([]ModuleCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModuleCount{Module: r.Module, Count: r.N})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 详情与写入
// ---------------------------------------------------------------------------

// CaseDetail 是用例详情（含步骤）。嵌入指针让 JSON 扁平展开，与契约一致。
type CaseDetail struct {
	*model.TestCase
	Steps []model.TestStep `json:"steps"`
}

// StepReq 是步骤的请求体。
//
// 刻意**不复用 model.TestStep**：模型里带着 ID / CreatedAt 等落库字段，
// 直接绑定会让客户端能够指定主键去覆盖别人的行。
// 独立结构同时也让 `enabled` 可以用指针表达"未传 = 默认 true"。
type StepReq struct {
	Seq       int                            `json:"seq"`
	StepType  string                         `json:"step_type"`
	Name      string                         `json:"name"`
	RefAPIID  uint64                         `json:"ref_api_id"`
	RefCaseID uint64                         `json:"ref_case_id"`
	Request   jsonx.Any                      `json:"request"`
	Variables jsonx.Map                      `json:"variables"`
	Extract   jsonx.Slice[model.ExtractItem] `json:"extract"`
	Validate  jsonx.Slice[model.AssertItem]  `json:"validate"`
	Hooks     jsonx.Any                      `json:"hooks"`
	Enabled   *bool                          `json:"enabled"`
	WsOpType  string                         `json:"ws_op_type"`
	WsPayload jsonx.Any                      `json:"ws_payload"`
}

// CaseReq 是创建/更新用例的请求（PUT 为全量覆盖）。
type CaseReq struct {
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Module         string    `json:"module"`
	Priority       string    `json:"priority"`
	Tags           string    `json:"tags"`
	Status         string    `json:"status"`
	Description    string    `json:"description"`
	Config         jsonx.Any `json:"config"`
	RequestTimeout int       `json:"request_timeout"`
	CaseTimeout    int       `json:"case_timeout"`
	Steps          []StepReq `json:"steps"`
}

// Get 返回用例详情。
func (s *CaseService) Get(id uint64) (*CaseDetail, error) {
	tc, err := loadCase(s.DB, id)
	if err != nil {
		return nil, err
	}
	steps, err := caseSteps(s.DB, tc.ID)
	if err != nil {
		return nil, err
	}
	if steps == nil {
		steps = []model.TestStep{}
	}
	return &CaseDetail{TestCase: tc, Steps: steps}, nil
}

// Create 创建用例。
//
// 保存侧只拦"结构性"错误（标识符、重名、步骤类型、字段形状），
// **不拦语义问题**（变量未定义、缺 url、无断言）。
// 编辑器必须允许用户把写了一半的用例存下来 —— 那是草稿状态的正常形态，
// 语义问题交给 POST /cases/{id}/validate 与执行前的检查去暴露。
func (s *CaseService) Create(projectID uint64, req CaseReq, ownerID uint64) (*CaseDetail, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}

	tc, err := s.buildCase(projectID, req, 0)
	if err != nil {
		return nil, err
	}
	tc.OwnerID = ownerID

	steps, err := s.buildSteps(req.Steps)
	if err != nil {
		return nil, err
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(tc).Error; err != nil {
			return err
		}
		for i := range steps {
			steps[i].CaseID = tc.ID
		}
		if len(steps) == 0 {
			return nil
		}
		return tx.Create(&steps).Error
	})
	if err != nil {
		if isDuplicated(err) {
			return nil, errConflict("用例标识 %q 已存在", tc.Code)
		}
		return nil, errInternal("创建用例失败", err)
	}

	return &CaseDetail{TestCase: tc, Steps: steps}, nil
}

// Update 全量覆盖用例（含步骤）。
//
// **code 不可修改**：它是编译后的文件名（testcases/{code}.yaml），
// 也是历史结果里 case_code 的取值，改了会让新旧结果对不上。
func (s *CaseService) Update(id uint64, req CaseReq) (*CaseDetail, error) {
	old, err := loadCase(s.DB, id)
	if err != nil {
		return nil, err
	}

	if req.Code != "" && strings.TrimSpace(req.Code) != old.Code {
		return nil, errBadParam(
			"用例 code 不可修改（当前 %q，收到 %q）：它决定编译后的文件名与历史结果的可追溯性",
			old.Code, strings.TrimSpace(req.Code))
	}

	tc, err := s.buildCase(old.ProjectID, req, old.ID)
	if err != nil {
		return nil, err
	}
	// code 与归属字段沿用原值
	tc.ID = old.ID
	tc.Code = old.Code
	tc.ProjectID = old.ProjectID
	tc.OwnerID = old.OwnerID
	tc.LastRunID = old.LastRunID
	tc.LastStatus = old.LastStatus

	steps, err := s.buildSteps(req.Steps)
	if err != nil {
		return nil, err
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(tc).Error; err != nil {
			return err
		}
		// 步骤是纯派生数据（DB 是唯一真源，YAML 每次重新编译），
		// 因此用**硬删除**替换，避免软删除的行随时间无限堆积。
		if err := tx.Unscoped().Where("case_id = ?", tc.ID).Delete(&model.TestStep{}).Error; err != nil {
			return err
		}
		for i := range steps {
			steps[i].CaseID = tc.ID
		}
		if len(steps) == 0 {
			return nil
		}
		return tx.Create(&steps).Error
	})
	if err != nil {
		return nil, errInternal("更新用例失败", err)
	}

	if steps == nil {
		steps = []model.TestStep{}
	}
	return &CaseDetail{TestCase: tc, Steps: steps}, nil
}

// Delete 删除用例。
func (s *CaseService) Delete(id uint64) error {
	tc, err := loadCase(s.DB, id)
	if err != nil {
		return err
	}

	var refs int64
	err = s.DB.Model(&model.SuiteCase{}).Where("case_id = ?", id).Count(&refs).Error
	if err != nil {
		return errInternal("检查用例引用失败", err)
	}
	if refs > 0 {
		return errInUse("用例 %q 被 %d 个用例集引用，请先移出用例集", tc.Name, refs)
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.TestCase{}, id).Error; err != nil {
			return err
		}
		return tx.Unscoped().Where("case_id = ?", id).Delete(&model.TestStep{}).Error
	})
	if err != nil {
		return errInternal("删除用例失败", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// YAML 预览与校验
// ---------------------------------------------------------------------------

// YAMLPreview 是编译后的用例 YAML。
//
// 契约要求（docs/接口契约.md 4 节）：这里的 yaml 必须与执行时写到盘上的
// 内容**逐字节一致**，因此它与执行路径共用 compiler.Render 这一条渲染链路，
// 不存在"预览用的简化版渲染器"。
type YAMLPreview struct {
	FileName string `json:"filename"`
	YAML     string `json:"yaml"`
	Env      string `json:"env"`
}

// RenderYAML 渲染指定用例的 YAML。
func (s *CaseService) RenderYAML(id, envID uint64) (*YAMLPreview, error) {
	tc, steps, env, err := s.loadForCompile(id, envID)
	if err != nil {
		return nil, err
	}
	project, err := loadProject(s.DB, tc.ProjectID)
	if err != nil {
		return nil, err
	}
	out, err := compiler.Render(&compiler.Input{
		Project: project,
		Env:     env,
		Cases:   []compiler.CaseSpec{{Case: tc, Steps: steps}},
	})
	if err != nil {
		return nil, response.Wrap(response.CodeCompileFail, "编译失败："+err.Error(), err)
	}
	cc := out.Cases[0]
	return &YAMLPreview{FileName: cc.FileName, YAML: cc.YAML, Env: out.EnvText}, nil
}

// ValidateOutcome 是静态校验的结果。
type ValidateOutcome struct {
	OK     bool              `json:"ok"`
	Issues []validator.Issue `json:"issues"`
}

// Validate 执行静态校验：先做语义校验，再做**渲染后 YAML 的字段白名单**。
//
// 两组校验的分工见 validator 包注释：语义校验看 DB 模型，
// 字段白名单看最终产物。第二组守的是实测 A7 那一类风险 ——
// 引擎对未知字段完全静默，写错的字段不是报错而是**不生效**。
func (s *CaseService) Validate(id, envID uint64) (*ValidateOutcome, error) {
	tc, steps, env, err := s.loadForCompile(id, envID)
	if err != nil {
		return nil, err
	}
	project, err := loadProject(s.DB, tc.ProjectID)
	if err != nil {
		return nil, err
	}

	siblings, err := siblingCaseNames(s.DB, tc.ProjectID, tc.ID)
	if err != nil {
		return nil, err
	}

	vr := validator.Validate(&validator.Input{
		Env:              env,
		Cases:            []validator.CaseSpec{{Case: tc, Steps: steps}},
		SiblingCaseNames: siblings,
	})
	out := &ValidateOutcome{Issues: vr.Issues}

	// 语义层已有 error 时不再渲染：编译必然也会失败，
	// 把同一个原因报两遍只会让用户在一堆重复提示里找不到重点。
	if !vr.OK() {
		out.OK = false
		return out, nil
	}

	comp, err := compiler.Render(&compiler.Input{
		Project: project,
		Env:     env,
		Cases:   []compiler.CaseSpec{{Case: tc, Steps: steps}},
	})
	if err != nil {
		return nil, response.Wrap(response.CodeCompileFail, "编译失败："+err.Error(), err)
	}
	cc := comp.Cases[0]
	out.Issues = append(out.Issues, validator.ValidateYAMLFields(cc.FileName, cc.YAML)...)
	out.OK = !hasErrorIssue(out.Issues)
	return out, nil
}

func hasErrorIssue(issues []validator.Issue) bool {
	for _, is := range issues {
		if is.Level == validator.LevelError {
			return true
		}
	}
	return false
}

// loadForCompile 加载渲染所需的三件东西：用例、步骤、环境。
func (s *CaseService) loadForCompile(id, envID uint64) (*model.TestCase, []model.TestStep, *model.Environment, error) {
	tc, err := loadCase(s.DB, id)
	if err != nil {
		return nil, nil, nil, err
	}
	steps, err := caseSteps(s.DB, tc.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	env, err := resolveEnv(s.DB, tc.ProjectID, envID)
	if err != nil {
		return nil, nil, nil, err
	}
	return tc, steps, env, nil
}

// ---------------------------------------------------------------------------
// 构造与归一化
// ---------------------------------------------------------------------------

// buildCase 由请求构造用例模型，并做保存侧的结构性校验。
func (s *CaseService) buildCase(projectID uint64, req CaseReq, selfID uint64) (*model.TestCase, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("用例名称不能为空")
	}

	var code string
	if selfID == 0 {
		var ok bool
		code, ok = normalizeIdent(req.Code)
		if !ok {
			return nil, errInvalidIdent("用例 code", req.Code)
		}

		var dup int64
		err := s.DB.Model(&model.TestCase{}).
			Where("project_id = ? AND code = ?", projectID, code).Count(&dup).Error
		if err != nil {
			return nil, errInternal("检查用例 code 是否重复失败", err)
		}
		if dup > 0 {
			return nil, errConflict("用例标识 %q 已存在", code)
		}
	}

	// 用例名唯一性（实测 F11）：引擎的 summary.json 以 config.name 作唯一标识。
	// 这里用同一张表自查，重名时给出可操作的提示。
	var dupName int64
	q := s.DB.Model(&model.TestCase{}).Where("project_id = ? AND name = ?", projectID, name)
	if selfID > 0 {
		q = q.Where("id <> ?", selfID)
	}
	if err := q.Count(&dupName).Error; err != nil {
		return nil, errInternal("检查用例名是否重复失败", err)
	}
	if dupName > 0 {
		return nil, errConflict(
			"用例名称 %q 已存在。引擎以用例名作为 summary.json 的唯一标识，重名会导致执行结果无法区分", name)
	}

	return &model.TestCase{
		ProjectID:      projectID,
		Code:           code,
		Name:           name,
		Module:         strings.TrimSpace(req.Module),
		Priority:       normalizePriority(req.Priority),
		Tags:           strings.TrimSpace(req.Tags),
		Status:         normalizeCaseStatus(req.Status),
		Description:    strings.TrimSpace(req.Description),
		Config:         req.Config,
		RequestTimeout: maxInt(req.RequestTimeout, 0),
		CaseTimeout:    maxInt(req.CaseTimeout, 0),
	}, nil
}

// buildSteps 把请求步骤转成模型步骤，并归一化 seq。
//
// seq 归一化策略：按请求给出的 seq 稳定排序后**重编号为 1..n**。
// 理由：引擎按 seq 升序执行，而前端拖拽排序后很容易产生
// 10/20/30 这类间隔值或顺序错乱；由服务端统一重排可以让
// "数组顺序 = 执行顺序" 这个直觉永远成立，也让 step_result.seq 干净可读。
func (s *CaseService) buildSteps(in []StepReq) ([]model.TestStep, error) {
	if len(in) == 0 {
		return nil, nil
	}

	// 稳定排序：seq 相同或为 0 的按原数组顺序排在后面。
	// 用 SliceStable 而不是 Slice：前端拖拽后偶发的重复 seq 不应让
	// 步骤顺序在两次保存之间随机抖动。
	ordered := make([]StepReq, len(in))
	copy(ordered, in)
	sort.SliceStable(ordered, func(i, j int) bool {
		return stepSortKey(ordered[i].Seq) < stepSortKey(ordered[j].Seq)
	})

	out := make([]model.TestStep, 0, len(ordered))
	for i := range ordered {
		req := ordered[i]
		out = append(out, toTestStep(req, i+1))
	}
	return out, nil
}

func stepSortKey(seq int) int {
	if seq <= 0 {
		// 未指定 seq 的步骤排到最后，保持它们在数组里的相对顺序
		return 1 << 30
	}
	return seq
}

func toTestStep(req StepReq, seq int) model.TestStep {
	st := model.TestStep{
		Seq:       seq,
		StepType:  normalizeStepType(req.StepType),
		Name:      strings.TrimSpace(req.Name),
		RefAPIID:  req.RefAPIID,
		RefCaseID: req.RefCaseID,
		Request:   req.Request,
		Variables: req.Variables,
		Extract:   req.Extract,
		Validate:  req.Validate,
		Hooks:     req.Hooks,
		Enabled:   true,
		WsOpType:  strings.TrimSpace(req.WsOpType),
		WsPayload: req.WsPayload,
	}
	if req.Enabled != nil {
		st.Enabled = *req.Enabled
	}
	if st.Extract == nil {
		st.Extract = jsonx.Slice[model.ExtractItem]{}
	}
	if st.Validate == nil {
		st.Validate = jsonx.Slice[model.AssertItem]{}
	}
	return st
}

// normalizeStepType 把空值归一化为 request。
//
// M1 只开放 request；其他类型不在保存侧拒绝，而是留给校验器报
// UNSUPPORTED_STEP_TYPE —— 理由是用户可能通过 API 导入一条
// 引用了 testcase 的用例并希望先存下来再改，直接拒收更不友好。
func normalizeStepType(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return model.StepRequest
	}
	return v
}

func normalizePriority(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case model.PriorityP0:
		return model.PriorityP0
	case model.PriorityP2:
		return model.PriorityP2
	case model.PriorityP3:
		return model.PriorityP3
	default:
		return model.PriorityP1
	}
}

func normalizeCaseStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case model.CaseStatusDraft:
		return model.CaseStatusDraft
	case model.CaseStatusDisabled:
		return model.CaseStatusDisabled
	default:
		return model.CaseStatusActive
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
