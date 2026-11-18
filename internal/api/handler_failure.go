package api

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// failureHandler 提供失败聚类（F13）。
type failureHandler struct {
	svc *service.FailureClusterService
}

func newFailureHandler(deps *Deps) *failureHandler {
	return &failureHandler{svc: deps.Services.Failure}
}

// Clusters 返回失败聚类结果。
func (h *failureHandler) Clusters(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var runID uint64
	if v := strings.TrimSpace(c.Query("run_id")); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			Fail(c, response.CodeBadParam, "run_id 必须是数字")
			return
		}
		runID = n
	}
	days, limit := 30, 20
	if v := strings.TrimSpace(c.Query("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	res, err := h.svc.Clusters(projectID, runID, days, limit)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, res)
}
