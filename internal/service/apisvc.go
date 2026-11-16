package service

import (
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// APIService 管理接口定义库（F1：项目 → 服务（module）→ 接口）。
//
// 两条设计约定：
//   - 「服务」用 api_def.module 字符串表达，分组由 GROUP BY 得出，不建服务表
//     （与 TestCase.Module 的既有惯例一致）；
//   - UsedBy（被引用次数）是**派生值**，每次查询 GROUP BY ref_api_id 算出，
//     绝不落库——派生值一旦落库就有机会和明细不一致（项目「派生统计不落库」原则）。
type APIService struct {
	Deps
}

// APIReq 是创建/更新接口定义的请求（PUT 为全量覆盖）。
type APIReq struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Module      string    `json:"module"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Headers     jsonx.Map `json:"headers"`
	Params      jsonx.Map `json:"params"`
	Body        jsonx.Any `json:"body"`
	BodyType    string    `json:"body_type"`
	Description string    `json:"description"`
}

// APIView 是接口定义的展示视图：实体 + 派生字段。
type APIView struct {
	model.APIDef
	// UsedBy 是被多少个用例步骤引用（派生，不落库）。
	UsedBy int `json:"used_by"`
}

// APIGroup 是按服务（module）聚合的一组接口。
type APIGroup struct {
	Module string    `json:"module"`
	Count  int       `json:"count"`
	APIs   []APIView `json:"apis"`
}

// 未分类服务的展示名（module 为空串的接口归入此组）。
const apiModuleUnclassified = "未分类"

// List 返回项目下全部接口（可按 module 过滤），带引用计数。
func (s *APIService) List(projectID uint64, module string) ([]APIView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	q := s.DB.Where("project_id = ?", projectID)
	if strings.TrimSpace(module) != "" {
		q = q.Where("module = ?", strings.TrimSpace(module))
	}
	var list []model.APIDef
	if err := q.Order("module asc, code asc").Find(&list).Error; err != nil {
		return nil, errInternal("查询接口列表失败", err)
	}
	used, err := s.usedByMap(projectID)
	if err != nil {
		return nil, err
	}
	out := make([]APIView, 0, len(list))
	for i := range list {
		out = append(out, APIView{APIDef: list[i], UsedBy: used[list[i].ID]})
	}
	return out, nil
}

// Tree 按服务（module）聚合成分组，供前端左树展示。
func (s *APIService) Tree(projectID uint64) ([]APIGroup, error) {
	all, err := s.List(projectID, "")
	if err != nil {
		return nil, err
	}
	byModule := map[string][]APIView{}
	for _, a := range all {
		key := strings.TrimSpace(a.Module)
		if key == "" {
			key = apiModuleUnclassified
		}
		byModule[key] = append(byModule[key], a)
	}
	modules := make([]string, 0, len(byModule))
	for m := range byModule {
		modules = append(modules, m)
	}
	sort.Strings(modules)
	out := make([]APIGroup, 0, len(modules))
	for _, m := range modules {
		out = append(out, APIGroup{Module: m, Count: len(byModule[m]), APIs: byModule[m]})
	}
	return out, nil
}

// Get 返回单个接口定义（带引用计数）。
func (s *APIService) Get(id uint64) (*APIView, error) {
	a, err := s.load(id)
	if err != nil {
		return nil, err
	}
	used, err := s.usedByMap(a.ProjectID)
	if err != nil {
		return nil, err
	}
	v := APIView{APIDef: *a, UsedBy: used[a.ID]}
	return &v, nil
}

// Create 新建接口定义。
func (s *APIService) Create(projectID uint64, req APIReq) (*APIView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	req, err := normalizeAPIReq(req)
	if err != nil {
		return nil, err
	}

	var same int64
	if err := s.DB.Model(&model.APIDef{}).
		Where("project_id = ? AND code = ?", projectID, req.Code).
		Count(&same).Error; err != nil {
		return nil, errInternal("检查接口标识是否重复失败", err)
	}
	if same > 0 {
		return nil, errConflict("接口标识 %q 已存在", req.Code)
	}

	a := &model.APIDef{
		ProjectID:   projectID,
		Module:      req.Module,
		Code:        req.Code,
		Name:        req.Name,
		Method:      req.Method,
		Path:        req.Path,
		Headers:     req.Headers,
		Params:      req.Params,
		Body:        req.Body,
		BodyType:    req.BodyType,
		Description: req.Description,
		Source:      model.APISourceManual,
	}
	if err := s.DB.Create(a).Error; err != nil {
		return nil, errInternal("创建接口定义失败", err)
	}
	v := APIView{APIDef: *a, UsedBy: 0}
	return &v, nil
}

// Update 全量更新接口定义。
//
// ⭐ 引用它的用例**不需要任何改动**：编译期内联展开每次都会重新读取接口定义，
// 这是「改一处、全部生效」的实现基础。
func (s *APIService) Update(id uint64, req APIReq) (*APIView, error) {
	a, err := s.load(id)
	if err != nil {
		return nil, err
	}
	req, err = normalizeAPIReq(req)
	if err != nil {
		return nil, err
	}

	var same int64
	if err := s.DB.Model(&model.APIDef{}).
		Where("project_id = ? AND code = ? AND id <> ?", a.ProjectID, req.Code, a.ID).
		Count(&same).Error; err != nil {
		return nil, errInternal("检查接口标识是否重复失败", err)
	}
	if same > 0 {
		return nil, errConflict("接口标识 %q 已存在", req.Code)
	}

	a.Module = req.Module
	a.Code = req.Code
	a.Name = req.Name
	a.Method = req.Method
	a.Path = req.Path
	a.Headers = req.Headers
	a.Params = req.Params
	a.Body = req.Body
	a.BodyType = req.BodyType
	a.Description = req.Description
	if err := s.DB.Save(a).Error; err != nil {
		return nil, errInternal("更新接口定义失败", err)
	}
	used, err := s.usedByMap(a.ProjectID)
	if err != nil {
		return nil, err
	}
	v := APIView{APIDef: *a, UsedBy: used[a.ID]}
	return &v, nil
}

// Delete 删除接口定义；被步骤引用时拒绝（40003）。
func (s *APIService) Delete(id uint64) error {
	a, err := s.load(id)
	if err != nil {
		return err
	}
	used, err := s.usedByMap(a.ProjectID)
	if err != nil {
		return err
	}
	if n := used[a.ID]; n > 0 {
		return errInUse("接口 %q 正被 %d 个用例步骤引用，请先解除引用", a.Name, n)
	}
	if err := s.DB.Delete(a).Error; err != nil {
		return errInternal("删除接口定义失败", err)
	}
	return nil
}

// load 取接口定义，不存在映射成 40002。
func (s *APIService) load(id uint64) (*model.APIDef, error) {
	var a model.APIDef
	if err := s.DB.First(&a, id).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("接口定义不存在（id=%d）", id)
		}
		return nil, errInternal("查询接口定义失败", err)
	}
	return &a, nil
}

// usedByMap 返回 api_id → 引用它的步骤数（一次 GROUP BY，不落库）。
func (s *APIService) usedByMap(projectID uint64) (map[uint64]int, error) {
	type row struct {
		RefAPIID uint64
		N        int
	}
	var rows []row
	err := s.DB.Model(&model.TestStep{}).
		Select("ref_api_id, count(*) as n").
		Where("step_type = ? AND ref_api_id > 0", model.StepAPI).
		Where("case_id IN (?)", s.DB.Model(&model.TestCase{}).Select("id").Where("project_id = ?", projectID)).
		Group("ref_api_id").
		Scan(&rows).Error
	if err != nil {
		return nil, errInternal("统计接口引用次数失败", err)
	}
	out := make(map[uint64]int, len(rows))
	for _, r := range rows {
		out[r.RefAPIID] = r.N
	}
	return out, nil
}

// normalizeAPIReq 校验并归一请求。
func normalizeAPIReq(req APIReq) (APIReq, error) {
	req.Code = strings.TrimSpace(req.Code)
	req.Name = strings.TrimSpace(req.Name)
	req.Module = strings.TrimSpace(req.Module)
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	req.Path = strings.TrimSpace(req.Path)
	req.BodyType = strings.ToLower(strings.TrimSpace(req.BodyType))

	if req.Code == "" {
		return req, errBadParam("接口标识不能为空")
	}
	if !validIdent(req.Code) {
		return req, errBadParam("接口标识 %q 不合法：只允许字母、数字、下划线与连字符（1-64 位）", req.Code)
	}
	if req.Name == "" {
		req.Name = req.Code
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	if !containsMethod(req.Method) {
		return req, errBadParam("请求方法 %q 不受支持", req.Method)
	}
	if req.Path == "" {
		return req, errBadParam("接口路径不能为空（如 $base_url/users 或 /users）")
	}
	if req.BodyType == "" {
		req.BodyType = model.BodyTypeJSON
	}
	switch req.BodyType {
	case model.BodyTypeNone, model.BodyTypeJSON, model.BodyTypeForm, model.BodyTypeRaw:
	default:
		return req, errBadParam("请求体类型只支持 json / form / raw / none，收到 %q", req.BodyType)
	}
	return req, nil
}

func containsMethod(m string) bool {
	for _, v := range model.HTTPMethods {
		if v == m {
			return true
		}
	}
	return false
}

// loadAPIs 查项目全部接口定义并按 ID 索引，供 compiler.Input 使用。
//
// 与 loadDatasets 同一模式：编译器是纯函数包，不查库。
func loadAPIs(db *gorm.DB, projectID uint64) (map[uint64]*model.APIDef, error) {
	var list []model.APIDef
	if err := db.Where("project_id = ?", projectID).Find(&list).Error; err != nil {
		return nil, errInternal("查询接口定义失败", err)
	}
	out := make(map[uint64]*model.APIDef, len(list))
	for i := range list {
		out[list[i].ID] = &list[i]
	}
	return out, nil
}
