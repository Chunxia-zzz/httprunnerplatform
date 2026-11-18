package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// dbStepHandler 是引擎回环端点（R1）：db 步骤编译为对本端点的 POST，
// 平台执行 SQL 并以 JSON 返回，断言/提取由引擎原生完成。
//
// 安全模型（specs/015-db-steps/plan.md）：
//   - 不挂会话鉴权（引擎没有登录态），但必须带 per-run token（X-Dbstep-Token）；
//   - token 由执行器准备工作区时生成、写入该 run 的 .env，run 终态即失效；
//   - conn 只能在该 run 所属项目的 db_source 白名单内（服务层强制）。
type dbStepHandler struct {
	deps *Deps
}

func newDBStepHandler(deps *Deps) *dbStepHandler {
	return &dbStepHandler{deps: deps}
}

// Exec 处理引擎的 db 步骤请求。
//
// ⭐ 全端点使用真实 HTTP 语义（裸 JSON、真实状态码），不走平台的
// 「200 + 业务码」信封：调用方是引擎，步骤成败由 HTTP 状态码决定。
// 端点错误若回 200+信封，引擎会把 SQL 失败当成 pass —— 假绿（实测记录 A33）。
// 401 的先例同 F9 WS 握手。
func (h *dbStepHandler) Exec(c *gin.Context) {
	token := c.GetHeader("X-Dbstep-Token")
	runID, projectID, ok := h.deps.Services.DBStepTokens.Verify(token)
	if !ok {
		c.AbortWithStatusJSON(401, gin.H{"error": "dbstep token 无效或执行已结束"})
		return
	}

	var req service.DBStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
		return
	}
	res, err := h.deps.Services.DBStep.Exec(projectID, req)
	if err != nil {
		// err 的业务码决定 HTTP 状态：40004→404、40300→403、50000/其它→500。
		// message 原样带回（含数据库原生错误），让步骤错误可读。
		code, msg := response.CodeInternal, err.Error()
		if e, ok := err.(*response.Error); ok {
			code, msg = e.Code, e.Message
		}
		status := 500
		switch code {
		case response.CodeNotFound:
			status = 404
		case response.CodeForbidden:
			status = 403
		case response.CodeBadParam:
			status = 400
		}
		c.JSON(status, gin.H{"error": msg})
		return
	}
	// runID 留给审计/排障：响应头带上，便于从引擎日志反查
	c.Header("X-Dbstep-Run", itoaUint(runID))
	// 成功也是裸 JSON：断言口径是 body.rows / body.affected（不经信封）。
	c.JSON(200, res)
}

func itoaUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
