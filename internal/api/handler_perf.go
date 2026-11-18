package api

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// perfHandler 提供接口级性能回归分析（F11）。
type perfHandler struct{ svc *service.PerfService }

func newPerfHandler(deps *Deps) *perfHandler {
	return &perfHandler{svc: deps.Services.Perf}
}

// Get 返回接口级性能对比。
//
// 参数全部可选：run_id / days / baseline_runs / min_samples /
// threshold_pct / min_delta_ms / verdict。
func (h *perfHandler) Get(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	opts := service.PerfOptions{}
	if v := strings.TrimSpace(c.Query("run_id")); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			Fail(c, response.CodeBadParam, "run_id 必须是数字")
			return
		}
		opts.RunID = n
	}
	if v := strings.TrimSpace(c.Query("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Days = n
		}
	}
	if v := strings.TrimSpace(c.Query("baseline_runs")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.BaselineRuns = n
		}
	}
	if v := strings.TrimSpace(c.Query("min_samples")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.MinSamples = n
		}
	}
	if v := strings.TrimSpace(c.Query("threshold_pct")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			opts.ThresholdPct = f
		}
	}
	if v := strings.TrimSpace(c.Query("min_delta_ms")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			opts.MinDeltaMs = n
		}
	}
	opts.Verdict = c.Query("verdict")

	res, err := h.svc.Analyze(projectID, opts)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, res)
}
