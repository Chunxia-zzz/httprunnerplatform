package service

import (
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// dbSourceService 管理项目级数据库连接配置（R1）。
//
// 权限与 F7 认证配置同构：写仅管理员，读全员但 password 掩码；
// 回传掩码占位符表示「保持原值」。
type DbSourceService struct{ Deps }

// DbSourceReq 是新建/更新连接的请求体。
type DbSourceReq struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DBName   string `json:"dbname"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// DbSourceView 是返回给前端的视图（password 掩码）。
type DbSourceView struct {
	ID       uint64 `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DBName   string `json:"dbname"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *DbSourceService) sanitize(req DbSourceReq, prev *model.DbSource) (model.DbSource, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return model.DbSource{}, errBadParam("连接名称不能为空（db 步骤按它引用连接）")
	}
	driver := strings.ToLower(strings.TrimSpace(req.Driver))
	switch driver {
	case model.DBDrivers[0], model.DBDrivers[1], model.DBDrivers[2]:
	default:
		return model.DbSource{}, errBadParam(
			"driver 只支持 mysql / postgres / sqlite，收到 %q", req.Driver)
	}
	out := model.DbSource{
		Name:     name,
		Driver:   driver,
		Host:     strings.TrimSpace(req.Host),
		Port:     req.Port,
		DBName:   strings.TrimSpace(req.DBName),
		Username: strings.TrimSpace(req.Username),
		Password: strings.TrimSpace(req.Password),
	}
	switch driver {
	case "mysql":
		if out.Host == "" || out.Port == 0 || out.DBName == "" {
			return model.DbSource{}, errBadParam("mysql 连接需要 host / port / dbname")
		}
	case "postgres":
		if out.Host == "" || out.Port == 0 || out.DBName == "" {
			return model.DbSource{}, errBadParam("postgres 连接需要 host / port / dbname")
		}
	case "sqlite":
		if out.DBName == "" {
			return model.DbSource{}, errBadParam("sqlite 连接需要 dbname（数据库文件路径）")
		}
	}
	if prev != nil && out.Password == MaskedValue {
		out.Password = prev.Password
	}
	return out, nil
}

func (s *DbSourceService) view(m model.DbSource) DbSourceView {
	pw := MaskedValue
	if m.Password == "" {
		pw = ""
	}
	return DbSourceView{
		ID: m.ID, Name: m.Name, Driver: m.Driver, Host: m.Host,
		Port: m.Port, DBName: m.DBName, Username: m.Username, Password: pw,
	}
}

// List 返回项目连接清单（password 掩码）。
func (s *DbSourceService) List(projectID uint64) ([]DbSourceView, error) {
	var list []model.DbSource
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&list).Error; err != nil {
		return nil, errInternal("查询数据库连接失败", err)
	}
	out := make([]DbSourceView, 0, len(list))
	for _, m := range list {
		out = append(out, s.view(m))
	}
	return out, nil
}

// Create 新建连接（仅管理员，handler 层控制）。
func (s *DbSourceService) Create(projectID uint64, req DbSourceReq) (DbSourceView, error) {
	m, err := s.sanitize(req, nil)
	if err != nil {
		return DbSourceView{}, err
	}
	m.ProjectID = projectID
	var total int64
	if err := s.DB.Model(&model.DbSource{}).Where("project_id = ? AND name = ?", projectID, m.Name).
		Count(&total).Error; err != nil {
		return DbSourceView{}, errInternal("检查连接名是否重复失败", err)
	}
	if total > 0 {
		return DbSourceView{}, errConflict("项目下已存在名为 %q 的数据库连接", m.Name)
	}
	if err := s.DB.Create(&m).Error; err != nil {
		return DbSourceView{}, errInternal("创建数据库连接失败", err)
	}
	return s.view(m), nil
}

// Update 更新连接（仅管理员）。
func (s *DbSourceService) Update(id uint64, req DbSourceReq) (DbSourceView, error) {
	var prev model.DbSource
	if err := s.DB.First(&prev, id).Error; err != nil {
		if isNotFound(err) {
			return DbSourceView{}, errNotFound("数据库连接不存在（id=%d）", id)
		}
		return DbSourceView{}, errInternal("查询数据库连接失败", err)
	}
	m, err := s.sanitize(req, &prev)
	if err != nil {
		return DbSourceView{}, err
	}
	m.ProjectID = prev.ProjectID
	m.ID = prev.ID
	// 连接被 db 步骤引用时改名的连带影响：名字是引用 key，改名等于断链。
	// 这里选择拒绝改名（简单且可解释）；要换名请新建连接再改用例。
	if m.Name != prev.Name {
		return DbSourceView{}, errBadParam("连接名称不支持修改（db 步骤按名称引用，改名等于断链）；如需换名请新建连接")
	}
	if err := s.DB.Model(&model.DbSource{}).Where("id = ?", id).Updates(map[string]any{
		"driver": m.Driver, "host": m.Host, "port": m.Port,
		"db_name": m.DBName, "username": m.Username, "password": m.Password,
	}).Error; err != nil {
		return DbSourceView{}, errInternal("更新数据库连接失败", err)
	}
	m.CreatedAt = prev.CreatedAt
	return s.view(m), nil
}

// Delete 删除连接；被 db 步骤引用时拒绝（保持「引用不断链」原则）。
func (s *DbSourceService) Delete(id, projectID uint64) error {
	var m model.DbSource
	if err := s.DB.Where("id = ? AND project_id = ?", id, projectID).First(&m).Error; err != nil {
		if isNotFound(err) {
			return errNotFound("数据库连接不存在（id=%d）", id)
		}
		return errInternal("查询数据库连接失败", err)
	}
	var used int64
	if err := s.DB.Model(&model.TestStep{}).Where("step_type = ? AND db_source_name = ? AND deleted_at IS NULL",
		model.StepDB, m.Name).Count(&used).Error; err != nil {
		return errInternal("统计连接引用失败", err)
	}
	if used > 0 {
		return errInUse("连接 %q 正被 %d 个步骤引用，无法删除；请先移除相关 db 步骤", m.Name, used)
	}
	if err := s.DB.Delete(&m).Error; err != nil {
		return errInternal("删除数据库连接失败", err)
	}
	return nil
}

// GetByName 取项目内连接（db 步骤执行/校验用）。
func (s *DbSourceService) GetByName(projectID uint64, name string) (*model.DbSource, error) {
	var m model.DbSource
	if err := s.DB.Where("project_id = ? AND name = ?", projectID, name).First(&m).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("数据库连接 %q 不存在（本项目）", name)
		}
		return nil, errInternal("查询数据库连接失败", err)
	}
	return &m, nil
}
