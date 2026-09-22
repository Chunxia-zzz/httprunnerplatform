package api

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api/middleware"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// 本文件是 CI 的入口（`/open` 前缀）。
//
// 与 `/api/v1` 分开的理由：CI 触发不走会话体系，语义（没有登录态）
// 与限流（机器高频调用）都和页面上的人不同，混在一个前缀里迟早要拆。
//
// 两条最要紧的规则：
//
//  1. **默认异步**返回 run_id，同步等待靠 `?wait=1` 选配。
//     CI 里的长连接很容易被网关/代理掐断，让流水线挂在一个 HTTP 请求上
//     等 20 分钟是脆弱的。
//  2. **给一个数字让 pipeline 直接判成败**（exit_code_for_ci），
//     不给的话每条流水线都要写一段 jq 去解析 status 枚举，
//     而且每个人写的判断都不一样（有人会把 error 当成功）。

type openHandler struct {
	deps   *Deps
	svc    *service.RunService
	tokens *service.TokenService
}

func newOpenHandler(deps *Deps) *openHandler {
	return &openHandler{deps: deps, svc: deps.Services.Run, tokens: deps.Services.Token}
}

// currentToken 取出本次请求绑定的令牌。
//
// 拿不到就直接 401：所有 /open 端点都必须挂在 RequireToken 之后，
// 漏挂是配置错误，静默放行比直接报错危险得多。
func currentToken(c *gin.Context) (*model.APIToken, bool) {
	tk, ok := middleware.CurrentToken(c)
	if !ok {
		Fail(c, response.CodeUnauthorized, "缺少令牌上下文（路由未挂鉴权中间件）")
		return nil, false
	}
	return tk, true
}

// scoped 校验令牌权限。
//
// 分权限而不是"有令牌就能干一切"：只读令牌给出去（例如给看板拉结果）时，
// 不该顺带获得触发执行的能力。
func (h *openHandler) scoped(c *gin.Context, tk *model.APIToken, want string) bool {
	if h.tokens.HasScope(tk, want) {
		return true
	}
	Fail(c, response.CodeForbidden, fmt.Sprintf(
		"令牌没有 %s 权限（当前权限：%s）", want, tk.Scope))
	return false
}

// ---------------------------------------------------------------------------
// POST /open/runs —— 触发
// ---------------------------------------------------------------------------

// Trigger 触发一次执行。请求体与 POST /api/v1/runs 一致。
func (h *openHandler) Trigger(c *gin.Context) {
	tk, ok := currentToken(c)
	if !ok {
		return
	}
	if !h.scoped(c, tk, service.ScopeRunTrigger) {
		return
	}

	var req service.StartRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}

	// ⭐ 项目校验是"按项目签发"的全部意义：不校验的话，
	// 一个项目的 token 能触发任意项目的执行。
	if req.ProjectID == 0 {
		Fail(c, response.CodeBadParam, "project_id 不能为空")
		return
	}
	if req.ProjectID != tk.ProjectID {
		Fail(c, response.CodeForbidden, fmt.Sprintf(
			"令牌属于项目 %d，不能触发项目 %d 的执行", tk.ProjectID, req.ProjectID))
		return
	}
	req.TriggerType = model.TriggerCI

	run, err := h.svc.Start(req, 0)
	if err != nil {
		WriteError(c, err)
		return
	}

	// 默认异步：给 run_id 让流水线自己轮询。
	if !wantSync(c) {
		OK(c, gin.H{
			"run_id":  run.ID,
			"status":  run.Status,
			"wait":    false,
			"poll_at": "/open/runs/" + strconv.FormatUint(run.ID, 10) + "/result",
		})
		return
	}
	h.waitAndAnswer(c, run.ID)
}

// ---------------------------------------------------------------------------
// GET /open/runs/{id}/result —— 取结果
// ---------------------------------------------------------------------------

// Result 返回一次执行的结果摘要，供 pipeline 直接判成败。
func (h *openHandler) Result(c *gin.Context) {
	tk, ok := currentToken(c)
	if !ok {
		return
	}
	if !h.scoped(c, tk, service.ScopeRunRead) {
		return
	}

	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	run, err := h.svc.GetRun(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	// 令牌只能看自己项目的执行。少了这一步，"按项目签发"就只管了写、没管读。
	if run.ProjectID != tk.ProjectID {
		Fail(c, response.CodeForbidden, fmt.Sprintf(
			"令牌属于项目 %d，不能读取项目 %d 的执行", tk.ProjectID, run.ProjectID))
		return
	}

	if !wantSync(c) || isTerminal(run.Status) {
		OK(c, h.resultOf(id, run, wantSync(c) && isTerminal(run.Status)))
		return
	}
	// ?wait=1 且还没跑完 ⇒ 阻塞等一会儿再给结果
	h.waitAndAnswer(c, id)
}

// ---------------------------------------------------------------------------
// 同步等待
// ---------------------------------------------------------------------------

// waitAndAnswer 阻塞等待执行进入终态，再返回结果。
//
// 上限是硬性的：CI 的一个 HTTP 请求不该无限挂着。超了就返回当前状态
// （status 仍是 running，exit_code_for_ci 给 2），让流水线自己决定
// 是继续轮询还是放弃 —— 比在这里替它做决定好。
func (h *openHandler) waitAndAnswer(c *gin.Context, runID uint64) {
	deadline := time.Now().Add(waitTimeout(c))

	for {
		run, err := h.svc.GetRun(runID)
		if err != nil {
			WriteError(c, err)
			return
		}
		if isTerminal(run.Status) {
			OK(c, h.resultOf(runID, run, true))
			return
		}
		// 超时也算"等过了"：exit_code_for_ci 会是 2，pipeline 自己决定怎么办
		if time.Now().After(deadline) {
			OK(c, h.resultOf(runID, run, true))
			return
		}
		select {
		case <-c.Request.Context().Done():
			// 客户端断了（网关掐断 / 流水线取消）：没必要继续等
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// resultOf 组装结果摘要。
//
// waited 表示这次结果是"阻塞等到终态"拿到的，还是"看一眼就返回"的。
// CI 需要区分：前者可以直接拿 exit_code_for_ci 判成败，
// 后者可能还在跑（exit_code_for_ci=2），得继续轮询。
func (h *openHandler) resultOf(runID uint64, run *model.RunRecord, waited bool) gin.H {
	out := gin.H{
		"run_id":           run.ID,
		"wait":             waited,
		"status":           run.Status,
		"exit_code_for_ci": exitCodeForCI(run.Status),
		"total":            run.Total,
		"passed":           run.Passed,
		"failed":           run.Failed,
		"error":            run.Error,
		"skipped":          run.Skipped,
		"count_mismatch":   run.CountMismatch,
		"duration_ms":      run.DurationMs,
		"error_msg":        run.ErrorMsg,
	}

	// 失败用例清单：CI 的日志里最有用的就是"哪几条挂了、挂在哪一类"。
	// 只列不到终态之外的失败项，且最多 50 条 —— 一次全挂时不要刷屏。
	if run.Failed > 0 || run.Error > 0 {
		cases, err := h.svc.CaseResults(runID)
		if err == nil {
			list := make([]gin.H, 0, len(cases))
			for _, cr := range cases {
				if cr.Status != model.StatusFail && cr.Status != model.StatusError {
					continue
				}
				list = append(list, gin.H{
					"case_code":   cr.CaseCode,
					"status":      cr.Status,
					"attribution": cr.Attribution,
					"error_msg":   cr.ErrorMsg,
				})
				if len(list) >= 50 {
					break
				}
			}
			out["failed_cases"] = list
		}
	}
	return out
}

// exitCodeForCI 把执行状态翻成一个 pipeline 能直接用的数字。
//
// 0 = 成功，1 = 有用例没过，2 = 执行本身没跑完（还在跑 / 被取消 / 内部错误）。
// 分三档而不是两档：把"还在跑"当成"失败"会让流水线以为测试挂了，
// 当成"成功"更糟 —— 它是假绿。
func exitCodeForCI(status string) int {
	switch status {
	case model.RunSuccess:
		return 0
	case model.RunFailed, model.RunError:
		return 1
	default:
		// queued / running / canceled 都归到这里
		return 2
	}
}

func isTerminal(status string) bool {
	switch status {
	case model.RunSuccess, model.RunFailed, model.RunError, model.RunCanceled:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// 查询参数
// ---------------------------------------------------------------------------

// wantSync 是否要同步等待。
func wantSync(c *gin.Context) bool {
	v := c.Query("wait")
	return v == "1" || v == "true"
}

// waitTimeout 同步等待的上限，默认 5 分钟，最大 30 分钟。
func waitTimeout(c *gin.Context) time.Duration {
	sec, err := strconv.Atoi(c.Query("timeout"))
	if err != nil || sec <= 0 {
		return 5 * time.Minute
	}
	if sec > 1800 {
		sec = 1800
	}
	return time.Duration(sec) * time.Second
}
