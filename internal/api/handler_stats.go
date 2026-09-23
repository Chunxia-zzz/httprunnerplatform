package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// statsHandler 提供统计看板（M4）的查询接口。全部只读。
type statsHandler struct{ svc *service.StatsService }

func newStatsHandler(deps *Deps) *statsHandler {
	return &statsHandler{svc: deps.Services.Stats}
}

type statsQuery struct {
	ProjectID uint64 `form:"project_id"`
	Days      int    `form:"days"`
	Limit     int    `form:"limit"`
}

// Trend 返回通过率趋势。
func (h *statsHandler) Trend(c *gin.Context) {
	var q statsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	points, err := h.svc.Trend(service.StatsQuery{ProjectID: q.ProjectID, Days: q.Days})
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"points": points})
}

// Flaky 返回不稳定/常败用例排行。
func (h *statsHandler) Flaky(c *gin.Context) {
	var q statsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	list, err := h.svc.Flaky(service.StatsQuery{ProjectID: q.ProjectID, Days: q.Days}, q.Limit)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"items": list})
}

// Slowest 返回慢用例排行。
func (h *statsHandler) Slowest(c *gin.Context) {
	var q statsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	list, err := h.svc.Slowest(service.StatsQuery{ProjectID: q.ProjectID, Days: q.Days}, q.Limit)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"items": list})
}
