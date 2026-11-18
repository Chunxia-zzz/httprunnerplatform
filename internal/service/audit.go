package service

import (
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
)

// AuditService 记录与查询变更历史（F8）。
//
// 两条约定：
//   - 写入**失败不影响业务**（审计是旁路，不能因为日志写不进去就让用户保存失败）；
//   - 只追加：没有 Update/Delete 方法，从代码结构上保证审计数据不可篡改。
type AuditService struct {
	Deps
}

// ChangeEntry 是一条待写入的变更。
type ChangeEntry struct {
	ProjectID uint64
	UserID    uint64
	Username  string
	Action    string
	Entity    string
	EntityID  uint64
	EntityKey string
	Summary   string
}

// 变更动作与实体类型（与前端筛选器共用）。
const (
	ChangeCreate = "create"
	ChangeUpdate = "update"
	ChangeDelete = "delete"

	EntityCase        = "case"
	EntityAPI         = "api"
	EntityEnvironment = "environment"
	EntitySuite       = "suite"
	EntityPlan        = "plan"
	EntityDataset     = "dataset"
)

// Log 写入一条变更。失败只记日志，不返回错误。
func (s *AuditService) Log(e ChangeEntry) {
	if e.ProjectID == 0 {
		return
	}
	entry := &model.ChangeLog{
		ProjectID: e.ProjectID,
		UserID:    e.UserID,
		Username:  strings.TrimSpace(e.Username),
		Action:    e.Action,
		Entity:    e.Entity,
		EntityID:  e.EntityID,
		EntityKey: clip(e.EntityKey, 128),
		Summary:   clip(e.Summary, 1024),
	}
	if err := s.DB.Create(entry).Error; err != nil {
		logx.L().Error().Err(err).
			Uint64("project_id", e.ProjectID).
			Str("entity", e.Entity).
			Msg("写入变更历史失败（不影响业务）")
	}
}

// List 按项目查询变更历史（倒序分页）。
func (s *AuditService) List(projectID uint64, entity, action string, page Page) ([]model.ChangeLog, int64, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, 0, err
	}
	tx := s.DB.Model(&model.ChangeLog{}).Where("project_id = ?", projectID)
	if e := strings.TrimSpace(entity); e != "" {
		tx = tx.Where("entity = ?", e)
	}
	if a := strings.TrimSpace(action); a != "" {
		tx = tx.Where("action = ?", a)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计变更历史失败", err)
	}
	page = page.Normalize()
	var list []model.ChangeLog
	if err := tx.Order("id desc").Offset(page.Offset()).Limit(page.Limit()).Find(&list).Error; err != nil {
		return nil, 0, errInternal("查询变更历史失败", err)
	}
	return list, total, nil
}
