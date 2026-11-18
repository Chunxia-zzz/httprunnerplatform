package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api/middleware"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// dbSourceHandler 管理项目级数据库连接（R1）。
//
// 权限模型与通知配置（F2）一致：读全员（password 掩码）、写仅管理员。
type dbSourceHandler struct{ svc *service.DbSourceService }

func newDBSourceHandler(deps *Deps) *dbSourceHandler {
	return &dbSourceHandler{svc: deps.Services.DBSource}
}

// List 返回项目连接清单（password 掩码）。
func (h *dbSourceHandler) List(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.List(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// Create 新建连接（仅管理员）。
func (h *dbSourceHandler) Create(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.DbSourceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	v, err := h.svc.Create(projectID, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, v)
}

// Update 更新连接（仅管理员）。
func (h *dbSourceHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.DbSourceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	v, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, v)
}

// Delete 删除连接（仅管理员）；被 db 步骤引用时拒绝。
func (h *dbSourceHandler) Delete(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	dbID, ok := ParseIDParam(c, "sourceId")
	if !ok {
		return
	}
	if err := h.svc.Delete(dbID, projectID); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}

var _ = middleware.RequireAdmin
