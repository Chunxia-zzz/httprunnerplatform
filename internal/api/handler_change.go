package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/api/middleware"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// auditChange 写入一条变更历史。
//
// 审计是旁路：写失败不影响业务（AuditService.Log 内部吞掉错误）。
func auditChange(c *gin.Context, deps *Deps, projectID uint64, action, entity string,
	entityID uint64, key, summary string) {
	if deps == nil {
		return
	}
	p, _ := middleware.CurrentPrincipal(c)
	deps.Services.Audit.Log(service.ChangeEntry{
		ProjectID: projectID,
		UserID:    p.UserID,
		Username:  p.Username,
		Action:    action,
		Entity:    entity,
		EntityID:  entityID,
		EntityKey: key,
		Summary:   summary,
	})
}

// changeHandler 查询变更历史（F8）。
type changeHandler struct{ svc *service.AuditService }

func newChangeHandler(deps *Deps) *changeHandler {
	return &changeHandler{svc: deps.Services.Audit}
}

// List 返回项目的变更历史（倒序分页，可按实体/动作筛选）。
func (h *changeHandler) List(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var q struct {
		pageQuery
		Entity string `form:"entity"`
		Action string `form:"action"`
	}
	if err := c.ShouldBindQuery(&q); err != nil {
		Fail(c, response.CodeBadParam, "查询参数非法："+err.Error())
		return
	}
	list, total, err := h.svc.List(projectID, q.Entity, q.Action, q.toPage())
	if err != nil {
		WriteError(c, err)
		return
	}
	p := q.toPage().Normalize()
	OKPage(c, list, total, Pagination{Page: p.Page, PageSize: p.PageSize})
}
