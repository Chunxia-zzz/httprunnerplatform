package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type debugHandler struct{ svc *service.DebugService }

func newDebugHandler(deps *Deps) *debugHandler {
	return &debugHandler{svc: deps.Services.Debug}
}

// DebugStep 执行单步调试（同步返回结果）。
//
// 这是 M3 调试体验的核心接口。与 POST /runs 的异步不同，调试是交互式的，
// 用户点了「调试这一步」就盯着等结果，所以这里同步阻塞到执行完成再返回。
//
// 结果与执行详情页的步骤结果同构（parser.StepOutcome），前端调试面板
// 可以直接复用详情页的步骤渲染组件。
func (h *debugHandler) DebugStep(c *gin.Context) {
	var req service.DebugStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	// 调试结果可能很大（报文快照 + 逐条断言），同步等待期间也要给超时兜底。
	result, err := h.svc.DebugStep(c.Request.Context(), req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, result)
}
