package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// memberHandler 管理项目成员（F8）。
type memberHandler struct{ svc *service.MemberService }

func newMemberHandler(deps *Deps) *memberHandler {
	return &memberHandler{svc: deps.Services.Member}
}

// List 返回项目成员（含项目所有者）。
func (h *memberHandler) List(c *gin.Context) {
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

// Add 把用户加入项目（仅管理员）。
func (h *memberHandler) Add(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.AddMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	m, err := h.svc.Add(projectID, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, m)
}

// Remove 移除成员（仅管理员）。
func (h *memberHandler) Remove(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	userID, ok := ParseIDParam(c, "userId")
	if !ok {
		return
	}
	if err := h.svc.Remove(projectID, userID); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}
