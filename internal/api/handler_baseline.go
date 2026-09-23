package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
)

// baselineHandler 提供「用例基线对比」（M4-d）的查询接口。全部只读。
type baselineHandler struct{ svc *service.BaselineService }

func newBaselineHandler(deps *Deps) *baselineHandler {
	return &baselineHandler{svc: deps.Services.Baseline}
}

// Baseline 返回指定用例的最近 N 次执行对比。
//
// project_id 来自路径（/projects/:id/baseline），case_id 与 limit 来自查询串。
func (h *baselineHandler) Baseline(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	caseID, ok := ParseQueryID(c, "case_id")
	if !ok {
		return
	}
	limit := int(queryUint64(c, "limit"))

	res, err := h.svc.Baseline(service.BaselineQuery{
		ProjectID: projectID,
		CaseID:    caseID,
		Limit:     limit,
	})
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, res)
}
