package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// userHandler 是账号管理的 HTTP 入口。
//
// 本文件只做三件事：绑定参数、取操作者身份、把 service 错误翻译成响应。
// 所有守卫（不能删自己、不能干掉最后一个管理员、权限变更要吊销会话）
// 都在 service.UserService 里 —— 它们必须能被普通单测直接验证，
// 而不是寄生在 HTTP 层。
type userHandler struct{ svc *service.UserService }

func newUserHandler(deps *Deps) *userHandler {
	return &userHandler{svc: deps.Services.User}
}

// List 返回账号列表（仅管理员，见路由上的 RequireAdmin）。
func (h *userHandler) List(c *gin.Context) {
	var q pageQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	list, total, err := h.svc.List(service.UserListQuery{
		Keyword: c.Query("keyword"),
		Role:    c.Query("role"),
	}, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}
	OKPage(c, list, total, Pagination{Page: q.Page, PageSize: q.PageSize})
}

// Create 新建账号。
func (h *userHandler) Create(c *gin.Context) {
	var req service.CreateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	u, err := h.svc.Create(req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, u)
}

// Update 修改账号的昵称、角色与启用状态。
//
// 操作者身份取自登录态（currentUserID），**不接受请求体传入** ——
// 否则任何人都能伪造 operator 绕过"不能对自己下手"的守卫。
func (h *userHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	u, err := h.svc.Update(id, currentUserID(c), req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, u)
}

// Delete 删除账号（软删除）。
func (h *userHandler) Delete(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(id, currentUserID(c)); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}

// ResetPassword 由管理员重置他人密码（无需原密码）。
func (h *userHandler) ResetPassword(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	if err := h.svc.ResetPassword(id, currentUserID(c), req.Password); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}
