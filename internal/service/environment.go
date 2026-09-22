package service

import (
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// EnvironmentService 负责环境的增删改查。
//
// 环境是唯一会被渲染成 `.env` 文件的实体（base_url 自 hrp v4.1 起从
// 用例 config 移到了 .env），因此它的字段语义直接决定用例能否跑通。
type EnvironmentService struct{ Deps }

// MaskedValue 是非管理员读取敏感值时的占位符。
//
// 前端回写时会把它原样带回来，服务端必须识别并**忽略**它——
// 否则一次"改个备注"的保存就会把 token 覆盖成三个星号。
const MaskedValue = "***"

// secretKeyRe 判定哪些 key 属于敏感信息。
//
// 与契约文档 3 节一致；大小写不敏感。
var secretKeyRe = regexp.MustCompile(`(?i)(token|secret|password|key|auth)`)

// EnvReq 是创建/更新环境的请求（PUT 为全量覆盖）。
type EnvReq struct {
	Name          string    `json:"name"`
	BaseURL       string    `json:"base_url"`
	Environs      jsonx.Map `json:"environs"`
	GlobalHeaders jsonx.Map `json:"global_headers"`
	VerifySSL     bool      `json:"verify_ssl"`
	IsDefault     bool      `json:"is_default"`
}

// List 返回项目下的环境列表。
//
// viewerIsAdmin 为 false 时对敏感值做掩码。掩码在**服务端**做而不是
// 交给前端过滤：否则敏感值会出现在 HTTP 响应体里，任何抓包/日志都能看到。
func (s *EnvironmentService) List(projectID uint64, viewerIsAdmin bool) ([]model.Environment, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}

	var list []model.Environment
	err := s.DB.Where("project_id = ?", projectID).
		Order("is_default desc, id asc").
		Find(&list).Error
	if err != nil {
		return nil, errInternal("查询环境列表失败", err)
	}

	out := make([]model.Environment, 0, len(list))
	for i := range list {
		out = append(out, *maskedEnv(&list[i], viewerIsAdmin))
	}
	return out, nil
}

// Get 返回单个环境（掩码后）。
func (s *EnvironmentService) Get(id uint64, viewerIsAdmin bool) (*model.Environment, error) {
	e, err := loadEnv(s.DB, id)
	if err != nil {
		return nil, err
	}
	return maskedEnv(e, viewerIsAdmin), nil
}

// Create 创建环境。
func (s *EnvironmentService) Create(projectID uint64, req EnvReq) (*model.Environment, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("环境名称不能为空")
	}

	var same int64
	err := s.DB.Model(&model.Environment{}).
		Where("project_id = ? AND name = ?", projectID, name).Count(&same).Error
	if err != nil {
		return nil, errInternal("检查环境名是否重复失败", err)
	}
	if same > 0 {
		return nil, errConflict("环境名称 %q 已存在", name)
	}

	e := &model.Environment{
		ProjectID:     projectID,
		Name:          name,
		BaseURL:       strings.TrimSpace(req.BaseURL),
		Environs:      sanitizeEnvMap(req.Environs, nil),
		GlobalHeaders: sanitizeEnvMap(req.GlobalHeaders, nil),
		VerifySSL:     req.VerifySSL,
		IsDefault:     req.IsDefault,
	}

	// 项目的第一个环境自动成为默认环境：没有默认环境时执行会退回
	// 引擎的内置 base_url，那种失败很难被用户联想到"是环境没设默认"。
	if !e.IsDefault {
		var count int64
		if err := s.DB.Model(&model.Environment{}).
			Where("project_id = ?", projectID).Count(&count).Error; err != nil {
			return nil, errInternal("统计项目环境数失败", err)
		}
		if count == 0 {
			e.IsDefault = true
		}
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if e.IsDefault {
			if err := clearDefaultEnv(tx, projectID, 0); err != nil {
				return err
			}
		}
		return tx.Create(e).Error
	})
	if err != nil {
		if isDuplicated(err) {
			return nil, errConflict("环境名称 %q 已存在", name)
		}
		return nil, errInternal("创建环境失败", err)
	}
	return e, nil
}

// Update 全量更新环境。
func (s *EnvironmentService) Update(id uint64, req EnvReq) (*model.Environment, error) {
	e, err := loadEnv(s.DB, id)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("环境名称不能为空")
	}

	var same int64
	err = s.DB.Model(&model.Environment{}).
		Where("project_id = ? AND name = ? AND id <> ?", e.ProjectID, name, id).
		Count(&same).Error
	if err != nil {
		return nil, errInternal("检查环境名是否重复失败", err)
	}
	if same > 0 {
		return nil, errConflict("环境名称 %q 已存在", name)
	}

	e.Name = name
	e.BaseURL = strings.TrimSpace(req.BaseURL)
	// 回写时识别掩码占位符：值为 "***" 的项保留库里的原值，
	// 绝不把占位符本身存进去。
	e.Environs = sanitizeEnvMap(req.Environs, e.Environs)
	e.GlobalHeaders = sanitizeEnvMap(req.GlobalHeaders, e.GlobalHeaders)
	e.VerifySSL = req.VerifySSL
	e.IsDefault = req.IsDefault

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if e.IsDefault {
			if err := clearDefaultEnv(tx, e.ProjectID, e.ID); err != nil {
				return err
			}
		}
		return tx.Save(e).Error
	})
	if err != nil {
		if isDuplicated(err) {
			return nil, errConflict("环境名称 %q 已存在", name)
		}
		return nil, errInternal("更新环境失败", err)
	}

	// 项目里不能没有默认环境：把最后一个默认环境取消掉时顺手补一个。
	if err := s.ensureDefaultEnv(e.ProjectID); err != nil {
		return nil, err
	}
	return e, nil
}

// Delete 删除环境。
//
// 被测试计划引用时拒绝：计划里的 env_id 会变成悬空引用，
// 而计划执行是无人值守的（定时触发），到那时才发现就太晚了。
func (s *EnvironmentService) Delete(id uint64) error {
	e, err := loadEnv(s.DB, id)
	if err != nil {
		return err
	}

	var refs int64
	err = s.DB.Model(&model.TestPlan{}).Where("env_id = ?", id).Count(&refs).Error
	if err != nil {
		return errInternal("检查环境引用失败", err)
	}
	if refs > 0 {
		return errInUse("环境 %q 被 %d 个测试计划引用，无法删除", e.Name, refs)
	}

	if err := s.DB.Delete(&model.Environment{}, id).Error; err != nil {
		return errInternal("删除环境失败", err)
	}
	return s.ensureDefaultEnv(e.ProjectID)
}

// Default 返回项目默认环境；没有环境时返回 nil（不报错）。
func (s *EnvironmentService) Default(projectID uint64) (*model.Environment, error) {
	return resolveEnv(s.DB, projectID, 0)
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

// maskedEnv 返回掩码后的环境副本。
//
// 返回副本而不是原地修改：调用方可能是执行路径（需要真实值），
// 一旦原地改了，同一个请求内后续步骤就会拿到 "***" 去发请求。
func maskedEnv(e *model.Environment, isAdmin bool) *model.Environment {
	if e == nil || isAdmin {
		return e
	}
	cp := *e
	cp.Environs = maskMap(e.Environs)
	cp.GlobalHeaders = maskMap(e.GlobalHeaders)
	return &cp
}

func maskMap(m jsonx.Map) jsonx.Map {
	if len(m) == 0 {
		return m
	}
	out := make(jsonx.Map, len(m))
	for k, v := range m {
		if secretKeyRe.MatchString(k) {
			out[k] = MaskedValue
			continue
		}
		out[k] = v
	}
	return out
}

// sanitizeEnvMap 归一化写入的键值对，并处理掩码占位符。
//
// prev 是库里已有的值（新建时传 nil）。
// 规则：
//   - 值为 MaskedValue 且 prev 中存在同名键 ⇒ 保留 prev 的原值
//   - 值为 MaskedValue 但 prev 中没有 ⇒ 丢弃该键（占位符不该被存进去）
//   - 键或值为空 ⇒ 丢弃
func sanitizeEnvMap(in jsonx.Map, prev jsonx.Map) jsonx.Map {
	if len(in) == 0 {
		return nil
	}
	out := make(jsonx.Map, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		s, _ := v.(string)
		if s == MaskedValue {
			if old, ok := prev[key]; ok {
				out[key] = old
			}
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clearDefaultEnv 把同项目内其他环境的 is_default 置 false。
//
// keepID 为 0 表示"新记录尚未落库，全部清掉"。
func clearDefaultEnv(db *gorm.DB, projectID, keepID uint64) error {
	q := db.Model(&model.Environment{}).
		Where("project_id = ? AND is_default = ?", projectID, true)
	if keepID > 0 {
		q = q.Where("id <> ?", keepID)
	}
	if err := q.Update("is_default", false).Error; err != nil {
		return fmt.Errorf("清除其他环境的默认标记失败: %w", err)
	}
	return nil
}

// ensureDefaultEnv 保证项目至少有一个默认环境（有环境的前提下）。
func (s *EnvironmentService) ensureDefaultEnv(projectID uint64) error {
	var total int64
	if err := s.DB.Model(&model.Environment{}).
		Where("project_id = ?", projectID).Count(&total).Error; err != nil {
		return errInternal("统计项目环境数失败", err)
	}
	if total == 0 {
		return nil
	}

	var defaults int64
	err := s.DB.Model(&model.Environment{}).
		Where("project_id = ? AND is_default = ?", projectID, true).Count(&defaults).Error
	if err != nil {
		return errInternal("统计默认环境失败", err)
	}
	if defaults > 0 {
		return nil
	}

	var first model.Environment
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").First(&first).Error; err != nil {
		if isNotFound(err) {
			return nil
		}
		return errInternal("查询项目环境失败", err)
	}
	if err := s.DB.Model(&model.Environment{}).
		Where("id = ?", first.ID).Update("is_default", true).Error; err != nil {
		return errInternal("设置默认环境失败", err)
	}
	return nil
}
