package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// docHandler 是接口文档与只读分享的 HTTP 层（F6）。
type docHandler struct {
	doc   *service.DocService
	share *service.ShareService
}

func newDocHandler(deps *Deps) *docHandler {
	return &docHandler{doc: deps.Services.Doc, share: deps.Services.Share}
}

// Get 返回文档 JSON（登录态预览）。
func (h *docHandler) Get(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	doc, err := h.doc.Build(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, doc)
}

// HTML 返回文档的静态 HTML（登录态预览/下载）。
func (h *docHandler) HTML(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	page, err := h.doc.HTML(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", page)
}

// CreateShare 生成只读分享链接（明文 token 仅此一次）。
func (h *docHandler) CreateShare(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.CreateShareReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	issued, err := h.share.Issue(projectID, req, currentUserID(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, issued)
}

// ListShares 列出项目的分享链接（掩码）。
func (h *docHandler) ListShares(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.share.List(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// RevokeShare 吊销一条链接。
func (h *docHandler) RevokeShare(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	shareID, ok := ParseIDParam(c, "shareId")
	if !ok {
		return
	}
	if err := h.share.Revoke(projectID, shareID); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}

// PublicDoc 是公开的只读文档端点。
//
// 三种终态必须可区分：有效（200）、过期（410）、不存在/已吊销（404）。
// 只允许 GET/HEAD —— 显式用 Any 注册再拒绝其它方法，保证方法不对时是 405
// 而不是笼统的 404（调用方需要知道「地址对但用错方法」）。
func (h *docHandler) PublicDoc(c *gin.Context) {
	if m := c.Request.Method; m != http.MethodGet && m != http.MethodHead {
		c.Data(http.StatusMethodNotAllowed, "text/html; charset=utf-8",
			service.ErrorPageHTML("只读链接", "该链接只支持查看（GET），不提供任何写操作。", http.StatusMethodNotAllowed))
		return
	}

	link, project, err := h.share.Verify(c.Param("token"))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrShareExpired):
			c.Data(http.StatusGone, "text/html; charset=utf-8",
				service.ErrorPageHTML("链接已过期", "这个分享链接已超过有效期，请联系文档所有者重新生成。", http.StatusGone))
		default:
			c.Data(http.StatusNotFound, "text/html; charset=utf-8",
				service.ErrorPageHTML("链接无效", "链接无效或已被吊销，请联系文档所有者确认。", http.StatusNotFound))
		}
		return
	}

	doc, err := h.doc.Build(link.ProjectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	if c.Query("format") == "json" {
		c.JSON(http.StatusOK, doc)
		return
	}
	page, err := h.doc.HTML(project.ID)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", page)
}
