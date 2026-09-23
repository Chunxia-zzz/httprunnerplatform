package api

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type caseHandler struct{ svc *service.CaseService }

func newCaseHandler(deps *Deps) *caseHandler {
	return &caseHandler{svc: deps.Services.Case}
}

type caseListQuery struct {
	pageQuery
	Module   string `form:"module"`
	Priority string `form:"priority"`
	Status   string `form:"status"`
	Keyword  string `form:"keyword"`
}

// List 返回用例分页列表。
func (h *caseHandler) List(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var q caseListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}

	list, total, err := h.svc.List(projectID, service.CaseListQuery{
		Module:   q.Module,
		Priority: q.Priority,
		Status:   q.Status,
		Keyword:  q.Keyword,
	}, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}
	p := q.toPage().Normalize()
	OKPage(c, list, total, Pagination{Page: p.Page, PageSize: p.PageSize})
}

// Tree 返回按模块分组的用例数，供左侧树使用。
//
// 这个接口**不分页**：树的节点数量天然很小，套上分页反而让前端要多写一层
// "列表 + total" 的解包逻辑。
func (h *caseHandler) Tree(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	tree, err := h.svc.Tree(projectID)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, tree)
}

// Get 返回用例详情（含全部步骤，包括被禁用的）。
func (h *caseHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	detail, err := h.svc.Get(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, detail)
}

// Create 创建用例（含步骤，整体写入）。
func (h *caseHandler) Create(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.CaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	detail, err := h.svc.Create(projectID, req, currentUserID(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, detail)
}

// Update 全量覆盖用例（步骤整体替换）。
func (h *caseHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.CaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	detail, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, detail)
}

// Delete 删除用例。
func (h *caseHandler) Delete(c *gin.Context) {
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

// YAML 返回编译后的用例 YAML，供编辑器右侧只读预览。
//
// ⭐ 契约硬要求：这里返回的内容与执行时写到盘上的文件**逐字节一致**，
// 因为它和执行走的是同一条 compiler.Render 渲染链路。
// 任何"为了预览好看"而单独写一版渲染器的做法都会埋下
// 「编辑器看到的 ≠ 实际跑的」这一类最难排查的问题。
func (h *caseHandler) YAML(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	preview, err := h.svc.RenderYAML(id, queryUint64(c, "env_id"))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, preview)
}

// SaveYAML 保存源码视图里编辑过的 YAML（M3 ③-c 4.2）。
//
// 请求体是 { yaml: string }。反解析失败返回 50003，data 带行号信息
// （decompiler.LineError），前端据此高亮报错行。
func (h *caseHandler) SaveYAML(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	if strings.TrimSpace(req.YAML) == "" {
		Fail(c, response.CodeBadParam, "YAML 内容不能为空")
		return
	}
	detail, err := h.svc.SaveYAML(id, req.YAML)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, detail)
}

// Validate 执行静态校验，不调用引擎。
//
// 校验未通过时返回 HTTP 200 + code=50004，`data` 携带问题列表：
// 这不是"接口调用失败"，而是"请求成功、内容有问题"，前端的处理路径完全不同
// （渲染问题列表 vs 弹错误提示）。
func (h *caseHandler) Validate(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Validate(id, queryUint64(c, "env_id"))
	if err != nil {
		WriteError(c, err)
		return
	}
	if !out.OK {
		FailWithData(c, response.CodeValidateFail, "校验未通过", out)
		return
	}
	OK(c, out)
}
