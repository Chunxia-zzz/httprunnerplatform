package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// planHandler 是测试计划的 HTTP 入口。
//
// 与 suiteHandler 同一套路：只做参数绑定与响应翻译，规则
// （cron 保存时校验、时区合法、成员跨项目拒绝）全在 service.PlanService。
type planHandler struct{ svc *service.PlanService }

func newPlanHandler(deps *Deps) *planHandler {
	return &planHandler{svc: deps.Services.Plan}
}

// List 列举项目下的测试计划。
func (h *planHandler) List(c *gin.Context) {
	pid, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var q struct {
		pageQuery
		Keyword string `form:"keyword"`
		Enabled string `form:"enabled"`
	}
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	var enabled *bool
	switch q.Enabled {
	case "true", "1":
		v := true
		enabled = &v
	case "false", "0":
		v := false
		enabled = &v
	case "":
	default:
		Fail(c, response.CodeBadParam, "enabled 只能是 true / false")
		return
	}
	list, total, err := h.svc.List(pid, service.PlanListQuery{
		Keyword: q.Keyword,
		Enabled: enabled,
	}, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}
	OKPage(c, list, total, Pagination{Page: q.Page, PageSize: q.PageSize})
}

// Create 新建测试计划。可在 body 里带 suite_ids 一次挂好成员。
func (h *planHandler) Create(c *gin.Context) {
	pid, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		service.CreatePlanReq
		SuiteIDs []uint64 `json:"suite_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	req.CreatePlanReq.SuiteIDs = req.SuiteIDs
	p, err := h.svc.Create(pid, req.CreatePlanReq)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, p)
}

// Get 读取单个计划的视图（含下次执行时间预览）。
//
// 返回 View 而不是裸模型：cron 表达式配上时区才有意义，
// 而 next_fire_at 只有算出来给用户看，他才能确认自己配对了。
func (h *planHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.View(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, v)
}

// Update 修改计划（全量覆盖）。
func (h *planHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdatePlanReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	p, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, p)
}

// Delete 删除计划。
func (h *planHandler) Delete(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}

// SetEnabled 单独开关计划（不动其它字段）。
func (h *planHandler) SetEnabled(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	p, err := h.svc.SetEnabled(id, req.Enabled)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, p)
}

// Suites 返回计划的用例集成员（含"能不能跑"的标注）。
func (h *planHandler) Suites(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.Suites(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// SetSuites 全量替换成员。数组顺序即执行顺序。
func (h *planHandler) SetSuites(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.SetPlanSuitesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	// 空数组也要能提交（表示清空成员）。
	list, err := h.svc.SetSuites(id, req.SuiteIDs)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}
