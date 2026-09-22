package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// tokenHandler 是 CI 令牌的**管理**入口（签发 / 列表 / 吊销）。
//
// 与「使用令牌」（/open）分开：这里走会话体系，那边只认令牌。
// 混在一起的话，"谁能签发令牌"这个问题会被"令牌能干什么"掩盖掉。
type tokenHandler struct{ svc *service.TokenService }

func newTokenHandler(deps *Deps) *tokenHandler {
	return &tokenHandler{svc: deps.Services.Token}
}

// List 列举项目下的令牌（只有前缀，没有秘密）。
func (h *tokenHandler) List(c *gin.Context) {
	pid, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.List(pid)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// Issue 签发一枚令牌。
//
// ⭐ 明文**只在这一次响应里出现**。之后列表里只有 prefix，
// 忘了就重签 —— 对 CI 而言重签的成本远低于"明文长期躺在库里"。
func (h *tokenHandler) Issue(c *gin.Context) {
	pid, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.CreateTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	issued, err := h.svc.Issue(pid, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, issued)
}

// Revoke 吊销令牌。即刻生效（校验走数据库，没有缓存）。
func (h *tokenHandler) Revoke(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	// project_id 从查询参数取：吊销必须限定在项目内，
	// 否则一个项目的管理员能吊销别人的令牌。
	pid, ok := ParseQueryID(c, "project_id")
	if !ok {
		return
	}
	if err := h.svc.Revoke(pid, id); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}
