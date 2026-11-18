package api

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
)

// healthHandler 提供资产健康度体检（F12）。
type healthHandler struct{ svc *service.HealthService }

func newHealthHandler(deps *Deps) *healthHandler {
	return &healthHandler{svc: deps.Services.Health}
}

// Get 返回项目的健康度报告。
func (h *healthHandler) Get(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	opts := service.HealthOptions{}
	if v := strings.TrimSpace(c.Query("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Days = n
		}
	}
	report, err := h.svc.Analyze(projectID, opts)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, report)
}
