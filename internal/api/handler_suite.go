package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// suiteHandler 是用例集的 HTTP 入口。
//
// 与 userHandler 同一套路：本文件只做参数绑定与响应翻译，
// 规则（成员唯一、跨项目拒绝、parallel 拒绝、被计划引用时拒绝删除）
// 全在 service.SuiteService 里，以便被普通单测直接覆盖。
type suiteHandler struct{ svc *service.SuiteService }

func newSuiteHandler(deps *Deps) *suiteHandler {
	return &suiteHandler{svc: deps.Services.Suite}
}

// List 列举项目下的用例集。
func (h *suiteHandler) List(c *gin.Context) {
	pid, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var q struct {
		pageQuery
		Keyword string `form:"keyword"`
	}
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	list, total, err := h.svc.List(pid, service.SuiteListQuery{Keyword: q.Keyword}, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}
	OKPage(c, list, total, Pagination{Page: q.Page, PageSize: q.PageSize})
}

// Create 新建用例集。
func (h *suiteHandler) Create(c *gin.Context) {
	pid, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.CreateSuiteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	su, err := h.svc.Create(pid, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, su)
}

// Get 读取单个用例集。
func (h *suiteHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	su, err := h.svc.Get(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, su)
}

// Update 修改用例集（不含 code）。
func (h *suiteHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateSuiteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	su, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, su)
}

// Delete 删除用例集。被测试计划引用时返回 40003。
func (h *suiteHandler) Delete(c *gin.Context) {
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

// Members 返回成员列表（含"能不能跑"的标注）。
func (h *suiteHandler) Members(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.Members(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// SetMembers 全量替换成员。数组顺序即执行顺序。
func (h *suiteHandler) SetMembers(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.SetMembersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	// 空数组也要能提交（表示清空成员），因此不把 len==0 当成"没传"。
	list, err := h.svc.SetMembers(id, req.CaseIDs)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}
