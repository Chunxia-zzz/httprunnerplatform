package api

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// notifyHandler 管理通知配置（F2）。
type notifyHandler struct{ svc *service.NotifyService }

func newNotifyHandler(deps *Deps) *notifyHandler {
	return &notifyHandler{svc: deps.Services.Notify}
}

// List 返回项目通知配置（非管理员掩码）。
func (h *notifyHandler) List(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.List(projectID, currentIsAdmin(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// Create 新建配置（仅管理员）。
func (h *notifyHandler) Create(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.NotifyConfigReq
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

// Update 更新配置（仅管理员）。
func (h *notifyHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.NotifyConfigReq
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

// Delete 删除配置（仅管理员）。
func (h *notifyHandler) Delete(c *gin.Context) {
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

// Test 发送测试消息（仅管理员）。
func (h *notifyHandler) Test(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	res, err := h.svc.Test(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, res)
}

// Logs 返回最近投递记录。
func (h *notifyHandler) Logs(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	limit := 50
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	list, err := h.svc.Logs(projectID, limit)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}
