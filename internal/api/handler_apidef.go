package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// apiDefHandler 是接口定义库（F1）的 HTTP 层。
type apiDefHandler struct{ svc *service.APIService }

func newAPIDefHandler(deps *Deps) *apiDefHandler {
	return &apiDefHandler{svc: deps.Services.API}
}

// List 返回项目下的接口列表（?module=user 可按服务过滤）。
func (h *apiDefHandler) List(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.List(projectID, c.Query("module"))
	if err != nil {
		WriteError(c, err)
		return
	}
	OKPage(c, list, int64(len(list)), Pagination{Page: 1, PageSize: len(list)})
}

// Tree 返回按服务（module）聚合的分组，供左树使用。
func (h *apiDefHandler) Tree(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	groups, err := h.svc.Tree(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, groups)
}

// Get 返回单个接口定义（含被引用次数）。
func (h *apiDefHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	a, err := h.svc.Get(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, a)
}

// Create 在项目下创建接口定义。
func (h *apiDefHandler) Create(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.APIReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	a, err := h.svc.Create(projectID, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, a)
}

// Update 全量更新接口定义。
func (h *apiDefHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.APIReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	a, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, a)
}

// Delete 删除接口定义（被引用时 40003）。
func (h *apiDefHandler) Delete(c *gin.Context) {
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
