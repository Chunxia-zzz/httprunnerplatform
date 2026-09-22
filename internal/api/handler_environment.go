package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type environmentHandler struct{ svc *service.EnvironmentService }

func newEnvironmentHandler(deps *Deps) *environmentHandler {
	return &environmentHandler{svc: deps.Services.Environment}
}

// List 返回项目下的环境列表。
//
// 非管理员看到的是掩码后的值。掩码在服务端完成，前端拿不到原文。
func (h *environmentHandler) List(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.List(projectID, currentIsAdmin(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OKPage(c, list, int64(len(list)), Pagination{Page: 1, PageSize: len(list)})
}

// Get 返回单个环境。
func (h *environmentHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	e, err := h.svc.Get(id, currentIsAdmin(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, e)
}

// Create 在项目下创建环境。
func (h *environmentHandler) Create(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.EnvReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	e, err := h.svc.Create(projectID, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, e)
}

// Update 全量更新环境。
//
// 请求体里值为 "***" 的敏感项会被服务端忽略（保留原值），
// 因此"只改备注"的保存不会把 token 冲成星号。
func (h *environmentHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.EnvReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	e, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, e)
}

// Delete 删除环境。
func (h *environmentHandler) Delete(c *gin.Context) {
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
