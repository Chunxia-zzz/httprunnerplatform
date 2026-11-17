package api

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// mockHandler 是 Mock 服务的 HTTP 层（F4）。
type mockHandler struct{ svc *service.MockService }

func newMockHandler(deps *Deps) *mockHandler {
	return &mockHandler{svc: deps.Services.Mock}
}

// Serve 是**公开**的 mock 端点：调用方是被测系统或前端，不是平台用户。
//
// 因此这里不返回统一 JSON 信封，而是按接口定义回放真实响应
// （状态码 / Content-Type / body），调用方可直接当业务接口使用。
func (h *mockHandler) Serve(c *gin.Context) {
	code := c.Param("code")
	rest := c.Param("rest")
	if rest == "" {
		rest = "/"
	}
	status, headers, body := h.svc.Serve(code, c.Request.Method, rest)
	for k, v := range headers {
		c.Header(k, v)
	}
	if headers == nil {
		c.Header("Content-Type", "application/json; charset=utf-8")
	}
	c.Status(status)
	_, _ = c.Writer.Write(body)
}

// Info 返回 Mock 页所需信息（开关状态、基址、可 mock 的接口、示例 curl）。
func (h *mockHandler) Info(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	info, err := h.svc.Info(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, info)
}

// SetEnabled 开关项目的 Mock。
func (h *mockHandler) SetEnabled(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	if req.Enabled == nil {
		Fail(c, response.CodeBadParam, "缺少 enabled 字段")
		return
	}
	if err := h.svc.SetEnabled(projectID, *req.Enabled); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"enabled": *req.Enabled})
}

// Hits 返回最近的命中记录（内存态，重启即失）。
func (h *mockHandler) Hits(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	list := h.svc.Hits(projectID, limit)
	OK(c, gin.H{"hits": list, "note": "命中记录保存在内存中，服务重启后清空"})
}
