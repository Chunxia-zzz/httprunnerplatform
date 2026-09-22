package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type projectHandler struct{ svc *service.ProjectService }

func newProjectHandler(deps *Deps) *projectHandler {
	return &projectHandler{svc: deps.Services.Project}
}

type projectListQuery struct {
	pageQuery
	Keyword string `form:"keyword"`
}

// List 返回项目分页列表。
func (h *projectHandler) List(c *gin.Context) {
	var q projectListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}

	list, total, err := h.svc.List(service.ProjectListQuery{Keyword: q.Keyword}, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}

	p := q.toPage().Normalize()
	OKPage(c, list, total, Pagination{Page: p.Page, PageSize: p.PageSize})
}

// Get 返回单个项目。
func (h *projectHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	p, err := h.svc.Get(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, p)
}

// Create 创建项目。
func (h *projectHandler) Create(c *gin.Context) {
	var req service.CreateProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	p, err := h.svc.Create(req, currentUserID(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, p)
}

// Update 全量更新项目。
func (h *projectHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateProjectReq
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

// Delete 软删除项目。
func (h *projectHandler) Delete(c *gin.Context) {
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
