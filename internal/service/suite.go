package service

import (
	"strings"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
)

// SuiteService 负责用例集。
//
// 用例集是**扁平**的：一组用例的有序列表，不支持"用例集里放用例集"。
// 理由与代价见 docs/用例集与测试计划设计.md 1.1 —— 组织层级保持
// 「计划 → 用例集 → 用例」三层已经够用，而真正会痛的需求是"复用"
// （用例引用用例），那是另一件事，另排一步。
//
// 这个文件里真正需要注意的有三处：
//
//  1. 成员唯一（suite_id, case_id）：重复加入会破坏"通过率的分母"；
//  2. 成员里出现已删除 / 已禁用的用例时**报出来，不静默跳过** ——
//     否则用户只会看到"预期 10 条实际 8 条"，却不知道少了哪两条；
//  3. `parallel` 明确拒绝而不是悄悄串行执行 —— 悄悄降级会让用户以为
//     并行已经生效，而实际耗时并没有变化。
type SuiteService struct{ Deps }

// ---------------------------------------------------------------------------
// 视图与请求
// ---------------------------------------------------------------------------

// SuiteView 是用例集列表项。
type SuiteView struct {
	ID          uint64 `json:"id"`
	ProjectID   uint64 `json:"project_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ExecuteMode string `json:"execute_mode"`
	OnFailure   string `json:"on_failure"`
	Timeout     int    `json:"timeout"`
	CaseCount   int    `json:"case_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// SuiteMemberView 是用例集的成员。
//
// Runnable / SkipReason 是为了让"这条成员现在能不能跑"在界面上可见：
// 用例被禁用或被删除时，执行会跳过它并让用例数对账不一致 ——
// 如果列表里看不出来，用户只能对着数字猜。
type SuiteMemberView struct {
	Seq        int    `json:"seq"`
	CaseID     uint64 `json:"case_id"`
	CaseCode   string `json:"case_code"`
	CaseName   string `json:"case_name"`
	Module     string `json:"module"`
	Priority   string `json:"priority"`
	Status     string `json:"status"`
	Runnable   bool   `json:"runnable"`
	SkipReason string `json:"skip_reason"`
}

// CreateSuiteReq 是新建用例集的请求。
type CreateSuiteReq struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ExecuteMode string `json:"execute_mode"`
	OnFailure   string `json:"on_failure"`
	Timeout     int    `json:"timeout"`
}

// UpdateSuiteReq 是修改用例集的请求。
//
// **没有 Code 字段**，与项目、账号同理：code 会进入路径与执行记录的历史
// （RunRecord.target_name 之外，编译产物文件名也由用例 code 决定），
// 改名会让历史记录悄悄对不上。
type UpdateSuiteReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ExecuteMode string `json:"execute_mode"`
	OnFailure   string `json:"on_failure"`
	Timeout     int    `json:"timeout"`
}

// SetMembersReq 是重排成员的请求。
//
// 语义是**全量替换**：前端勾选后一次性提交有序 ID 列表。
// 不提供"单独增删某个成员"的接口 —— 那种接口无法表达顺序，
// 最终一定会演化成"先删光再加回来"，不如一开始就这么定义。
type SetMembersReq struct {
	CaseIDs []uint64 `json:"case_ids"`
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

type SuiteListQuery struct {
	Keyword string
}

func (s *SuiteService) List(projectID uint64, q SuiteListQuery, page Page) ([]SuiteView, int64, error) {
	tx := s.DB.Model(&model.TestSuite{}).Where("project_id = ?", projectID)
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		like := "%" + strings.ToLower(kw) + "%"
		tx = tx.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ?", like, like)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计用例集数量失败", err)
	}

	page = page.Normalize()
	var suites []model.TestSuite
	if err := tx.Order("id desc").Offset(page.Offset()).Limit(page.Limit()).Find(&suites).Error; err != nil {
		return nil, 0, errInternal("查询用例集列表失败", err)
	}

	counts, err := s.caseCounts(suites)
	if err != nil {
		return nil, 0, err
	}
	views := make([]SuiteView, 0, len(suites))
	for _, su := range suites {
		views = append(views, suiteView(&su, counts[su.ID]))
	}
	return views, total, nil
}

// caseCounts 批量统计各用例集的成员数。
//
// 不逐条 Count：列表页 N+1 查询在 10 人团队规模下就已经能看出来，
// 而这条 SQL 只多一次往返。
func (s *SuiteService) caseCounts(suites []model.TestSuite) (map[uint64]int, error) {
	out := make(map[uint64]int, len(suites))
	if len(suites) == 0 {
		return out, nil
	}
	ids := make([]uint64, 0, len(suites))
	for _, su := range suites {
		ids = append(ids, su.ID)
	}
	var rows []struct {
		SuiteID uint64
		Cnt     int64
	}
	if err := s.DB.Model(&model.SuiteCase{}).
		Select("suite_id, count(*) as cnt").
		Where("suite_id IN ?", ids).
		Group("suite_id").Scan(&rows).Error; err != nil {
		return nil, errInternal("统计用例集成员数失败", err)
	}
	for _, r := range rows {
		out[r.SuiteID] = int(r.Cnt)
	}
	return out, nil
}

func suiteView(su *model.TestSuite, caseCount int) SuiteView {
	return SuiteView{
		ID:          su.ID,
		ProjectID:   su.ProjectID,
		Code:        su.Code,
		Name:        su.Name,
		Description: su.Description,
		ExecuteMode: su.ExecuteMode,
		OnFailure:   su.OnFailure,
		Timeout:     su.Timeout,
		CaseCount:   caseCount,
		CreatedAt:   su.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:   su.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}

// Get 读取单个用例集。
func (s *SuiteService) Get(id uint64) (*model.TestSuite, error) { return loadSuite(s.DB, id) }

// Members 按 seq 返回成员，并标出哪些现在不能跑。
func (s *SuiteService) Members(suiteID uint64) ([]SuiteMemberView, error) {
	// 先确认用例集存在：不校验的话，GET /suites/9999/cases 会返回一个空数组，
	// 于是"用例集不存在"和"用例集没有成员"在响应上无法区分。
	if _, err := loadSuite(s.DB, suiteID); err != nil {
		return nil, err
	}

	var links []model.SuiteCase
	if err := s.DB.Where("suite_id = ?", suiteID).
		Order("seq asc, id asc").Find(&links).Error; err != nil {
		return nil, errInternal("查询用例集成员失败", err)
	}
	if len(links) == 0 {
		return []SuiteMemberView{}, nil
	}

	ids := make([]uint64, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.CaseID)
	}
	// 走 TestCase 模型查询，GORM 会自动带上软删除条件 ——
	// 因此查不到的那些就是"用例已被删除"。
	var cases []model.TestCase
	if err := s.DB.Where("id IN ?", ids).Find(&cases).Error; err != nil {
		return nil, errInternal("查询成员用例失败", err)
	}
	byID := make(map[uint64]*model.TestCase, len(cases))
	for i := range cases {
		byID[cases[i].ID] = &cases[i]
	}

	views := make([]SuiteMemberView, 0, len(links))
	for _, l := range links {
		v := SuiteMemberView{Seq: l.Seq, CaseID: l.CaseID}
		c, ok := byID[l.CaseID]
		if !ok {
			// 用例被软删了：成员行还在，但已经跑不了。
			v.CaseCode = "(已删除)"
			v.CaseName = "用例已被删除"
			v.Status = model.CaseStatusDisabled
			v.Runnable = false
			v.SkipReason = "用例已被删除，执行时会记为 error（不会静默跳过）"
			views = append(views, v)
			continue
		}
		v.CaseCode = c.Code
		v.CaseName = c.Name
		v.Module = c.Module
		v.Priority = c.Priority
		v.Status = c.Status
		v.Runnable = c.Status != model.CaseStatusDisabled
		if !v.Runnable {
			v.SkipReason = "用例已禁用，执行时记为 skipped"
		}
		views = append(views, v)
	}
	return views, nil
}

// ---------------------------------------------------------------------------
// 写操作
// ---------------------------------------------------------------------------

func (s *SuiteService) Create(projectID uint64, req CreateSuiteReq) (*model.TestSuite, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	code, ok := normalizeIdent(req.Code)
	if !ok {
		return nil, errInvalidIdent("用例集标识", req.Code)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("用例集名称不能为空")
	}
	execMode, err := normalizeExecuteMode(req.ExecuteMode)
	if err != nil {
		return nil, err
	}
	onFailure, err := normalizeOnFailure(req.OnFailure)
	if err != nil {
		return nil, err
	}

	var exists int64
	if err := s.DB.Model(&model.TestSuite{}).
		Where("project_id = ? AND code = ?", projectID, code).Count(&exists).Error; err != nil {
		return nil, errInternal("检查用例集标识是否重复失败", err)
	}
	if exists > 0 {
		return nil, errConflict("项目下已存在标识为 %q 的用例集", code)
	}

	su := &model.TestSuite{
		ProjectID:   projectID,
		Code:        code,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		ExecuteMode: execMode,
		OnFailure:   onFailure,
		Timeout:     req.Timeout,
	}
	if err := s.DB.Create(su).Error; err != nil {
		if isDuplicated(err) {
			return nil, errConflict("项目下已存在标识为 %q 的用例集", code)
		}
		return nil, errInternal("创建用例集失败", err)
	}
	logx.L().Info().Uint64("suite_id", su.ID).Str("code", su.Code).Msg("创建用例集")
	return su, nil
}

func (s *SuiteService) Update(id uint64, req UpdateSuiteReq) (*model.TestSuite, error) {
	su, err := loadSuite(s.DB, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("用例集名称不能为空")
	}
	execMode, err := normalizeExecuteMode(req.ExecuteMode)
	if err != nil {
		return nil, err
	}
	onFailure, err := normalizeOnFailure(req.OnFailure)
	if err != nil {
		return nil, err
	}
	if req.Timeout < 0 {
		return nil, errBadParam("超时时间不能为负数")
	}

	if err := s.DB.Model(&model.TestSuite{}).Where("id = ?", id).Updates(map[string]any{
		"name":         name,
		"description":  strings.TrimSpace(req.Description),
		"execute_mode": execMode,
		"on_failure":   onFailure,
		"timeout":      req.Timeout,
	}).Error; err != nil {
		return nil, errInternal("更新用例集失败", err)
	}
	su.Name = name
	su.Description = strings.TrimSpace(req.Description)
	su.ExecuteMode = execMode
	su.OnFailure = onFailure
	su.Timeout = req.Timeout
	return su, nil
}

// Delete 删除用例集（软删除）。被计划引用时拒绝。
func (s *SuiteService) Delete(id uint64) error {
	if _, err := loadSuite(s.DB, id); err != nil {
		return err
	}
	var used int64
	if err := s.DB.Model(&model.PlanSuite{}).Where("suite_id = ?", id).Count(&used).Error; err != nil {
		return errInternal("检查用例集是否被计划引用失败", err)
	}
	if used > 0 {
		return errInUse("用例集已被 %d 个测试计划引用，请先从计划中移除", used)
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("suite_id = ?", id).Delete(&model.SuiteCase{}).Error; err != nil {
			return errInternal("删除用例集成员失败", err)
		}
		if err := tx.Delete(&model.TestSuite{}, id).Error; err != nil {
			return errInternal("删除用例集失败", err)
		}
		return nil
	})
}

// SetMembers 全量替换成员，数组顺序即执行顺序。
func (s *SuiteService) SetMembers(suiteID uint64, caseIDs []uint64) ([]SuiteMemberView, error) {
	su, err := loadSuite(s.DB, suiteID)
	if err != nil {
		return nil, err
	}

	// 去重检查放在事务之前：唯一索引也能兜住，但索引报出来的错是
	// "UNIQUE constraint failed"，用户看不懂；而"重复勾选了同一个用例"
	// 是前端最常见的一类误操作，值得一条明确的提示。
	seen := make(map[uint64]struct{}, len(caseIDs))
	for _, cid := range caseIDs {
		if cid == 0 {
			return nil, errBadParam("成员列表里不能有空 ID")
		}
		if _, dup := seen[cid]; dup {
			return nil, errConflict("用例 %d 被重复加入用例集，同一个用例不能出现两次", cid)
		}
		seen[cid] = struct{}{}
	}

	// 成员必须属于同一个项目。放到执行时才发现的话，
	// 用户会看到"执行了 8 条，预期 10 条"却不知道原因。
	if len(caseIDs) > 0 {
		var n int64
		if err := s.DB.Model(&model.TestCase{}).
			Where("id IN ? AND project_id = ?", caseIDs, su.ProjectID).Count(&n).Error; err != nil {
			return nil, errInternal("校验成员用例失败", err)
		}
		if int(n) != len(caseIDs) {
			return nil, errBadParam("成员里有 %d 个用例不存在或不属于本项目（共提交 %d 个）",
				len(caseIDs)-int(n), len(caseIDs))
		}
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("suite_id = ?", suiteID).Delete(&model.SuiteCase{}).Error; err != nil {
			return errInternal("清空用例集成员失败", err)
		}
		if len(caseIDs) == 0 {
			return nil
		}
		links := make([]model.SuiteCase, 0, len(caseIDs))
		for i, cid := range caseIDs {
			links = append(links, model.SuiteCase{SuiteID: suiteID, CaseID: cid, Seq: i + 1})
		}
		if err := tx.Create(&links).Error; err != nil {
			if isDuplicated(err) {
				return errConflict("用例被重复加入用例集")
			}
			return errInternal("写入用例集成员失败", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Members(suiteID)
}

// ---------------------------------------------------------------------------
// 校验与加载
// ---------------------------------------------------------------------------

// normalizeExecuteMode 校验执行模式。
//
// parallel 明确拒绝而不是悄悄串行执行：悄悄降级会让用户以为并行已经生效，
// 而实际耗时一点没变 —— 这种"静默生效一半"的功能比"明确不支持"更难发现。
func normalizeExecuteMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", model.ExecuteSequential:
		return model.ExecuteSequential, nil
	case model.ExecuteParallel:
		return "", errBadParam("并行执行尚未实现（M2 只支持 sequential）")
	default:
		return "", errBadParam("执行模式只能是 sequential，收到 %q", mode)
	}
}

// normalizeOnFailure 校验遇错行为，默认 continue（设计文档决策 2）。
func normalizeOnFailure(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", model.OnFailureDefault:
		return model.OnFailureDefault, nil
	case model.OnFailureAbort:
		return model.OnFailureAbort, nil
	default:
		return "", errBadParam("遇错行为只能是 abort 或 continue，收到 %q", v)
	}
}

func loadSuite(db *gorm.DB, id uint64) (*model.TestSuite, error) {
	var su model.TestSuite
	if err := db.First(&su, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("用例集不存在（id=%d）", id)
		}
		return nil, errInternal("查询用例集失败", err)
	}
	return &su, nil
}
