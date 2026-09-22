package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type runHandler struct{ svc *service.RunService }

func newRunHandler(deps *Deps) *runHandler {
	return &runHandler{svc: deps.Services.Run}
}

type runListQuery struct {
	pageQuery
	ProjectID  uint64 `form:"project_id"`
	Status     string `form:"status"`
	TargetType string `form:"target_type"`
	TargetID   uint64 `form:"target_id"`
}

// Create 启动一次执行。
//
// 立刻返回 run_id（异步执行），前端靠轮询详情拿进度。
// 同步等待会让一次慢用例把 HTTP 连接占满，也让"批量/计划执行"在 M2 无从实现。
func (h *runHandler) Create(c *gin.Context) {
	var req service.StartRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	run, err := h.svc.Start(req, currentUserID(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"run_id": run.ID, "status": run.Status})
}

// List 返回执行记录分页列表。
func (h *runHandler) List(c *gin.Context) {
	var q runListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	list, total, err := h.svc.ListRuns(service.RunListQuery{
		ProjectID:  q.ProjectID,
		Status:     q.Status,
		TargetType: q.TargetType,
		TargetID:   q.TargetID,
	}, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}
	p := q.toPage().Normalize()
	OKPage(c, list, total, Pagination{Page: p.Page, PageSize: p.PageSize})
}

// Get 返回执行详情（含用例级结果，不含步骤明细）。
//
// 步骤明细刻意拆到 GET /runs/{id}/cases：报文快照与逐条断言很容易
// 达到几百 KB，而前端在轮询详情时并不需要它们。
func (h *runHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	run, err := h.svc.GetRun(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	cases, err := h.svc.CaseResultViews(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"run": run, "cases": cases})
}

// Cases 返回步骤与断言明细。
func (h *runHandler) Cases(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.CaseStepsDetail(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// Cancel 终止执行。
func (h *runHandler) Cancel(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	run, err := h.svc.Cancel(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"run_id": run.ID, "status": run.Status})
}

// Report 直接返回 HTML 报告，供前端 iframe 内嵌。
//
// ⚠️ 这里的 HTTP 状态码刻意偏离默认映射：契约（5 节）要求报告不存在时
// 返回 **404**，而 50006 在通用映射里是 HTTP 200。原因是前端需要用
// `iframe.onerror` / 状态码来判断"该显示报告还是显示友好替代文案"，
// 一个 200 但其实不是 HTML 的响应会让它渲染出一片空白。
func (h *runHandler) Report(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	html, err := h.svc.Report(id, queryUint64(c, "case_result_id"))
	if err != nil {
		var bizErr *response.Error
		if errors.As(err, &bizErr) && bizErr.Code == response.CodeReportMissing {
			AbortWithStatus(c, http.StatusNotFound, bizErr.Code, bizErr.Message)
			return
		}
		WriteError(c, err)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", html)
}

// Logs 返回原始双流日志。
func (h *runHandler) Logs(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	logs, err := h.svc.Logs(id, queryUint64(c, "case_result_id"))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, logs)
}
