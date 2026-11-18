package service

import (
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// MemberService 管理项目成员并回答「这个人能不能访问这个项目」（F8）。
type MemberService struct {
	Deps
}

// MemberView 是成员列表项（带用户名与昵称）。
type MemberView struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
}

// AddMemberReq 是添加成员的请求。
type AddMemberReq struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// List 返回项目成员（含项目所有者，即使他还没被显式加进成员表）。
func (s *MemberService) List(projectID uint64) ([]MemberView, error) {
	project, err := loadProject(s.DB, projectID)
	if err != nil {
		return nil, err
	}
	var rows []model.ProjectMember
	if err := s.DB.Where("project_id = ?", projectID).Order("id asc").Find(&rows).Error; err != nil {
		return nil, errInternal("查询项目成员失败", err)
	}
	// 所有者隐式视为 owner：存量项目的 owner 从未进过成员表
	seen := map[uint64]bool{}
	out := make([]MemberView, 0, len(rows)+1)
	if project.OwnerID > 0 {
		var u model.User
		if err := s.DB.First(&u, project.OwnerID).Error; err == nil {
			seen[u.ID] = true
			out = append(out, MemberView{UserID: u.ID, Username: u.Username, Nickname: u.Nickname, Role: model.MemberRoleOwner})
		}
	}
	for _, r := range rows {
		if seen[r.UserID] {
			continue
		}
		var u model.User
		if err := s.DB.First(&u, r.UserID).Error; err != nil {
			continue // 用户已删除：成员行留着也没意义，跳过展示
		}
		seen[r.UserID] = true
		out = append(out, MemberView{UserID: u.ID, Username: u.Username, Nickname: u.Nickname, Role: r.Role})
	}
	return out, nil
}

// Add 把用户加入项目。
func (s *MemberService) Add(projectID uint64, req AddMemberReq) (*MemberView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, errBadParam("用户名不能为空")
	}
	var u model.User
	if err := s.DB.Where("username = ?", username).First(&u).Error; err != nil {
		if isNotFound(err) {
			return nil, errNotFound("用户 %q 不存在", username)
		}
		return nil, errInternal("查询用户失败", err)
	}
	if !u.Enabled {
		return nil, errBadParam("用户 %q 已被禁用，无法加入项目", username)
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = model.MemberRoleMember
	}
	if role != model.MemberRoleOwner && role != model.MemberRoleMember {
		return nil, errBadParam("角色只支持 owner / member，收到 %q", req.Role)
	}

	var exists int64
	if err := s.DB.Model(&model.ProjectMember{}).
		Where("project_id = ? AND user_id = ?", projectID, u.ID).Count(&exists).Error; err != nil {
		return nil, errInternal("检查成员是否已存在失败", err)
	}
	if exists > 0 {
		return nil, errConflict("用户 %q 已经是该项目成员", username)
	}

	m := &model.ProjectMember{ProjectID: projectID, UserID: u.ID, Role: role}
	// 收敛开关：第一次加成员就把项目从「全员可见」切成「仅成员可见」。
	if err := s.DB.Model(&model.Project{}).Where("id = ?", projectID).
		Update("access_restricted", true).Error; err != nil {
		return nil, errInternal("更新项目访问模式失败", err)
	}
	if err := s.DB.Create(m).Error; err != nil {
		if isDuplicated(err) {
			return nil, errConflict("用户 %q 已经是该项目成员", username)
		}
		return nil, errInternal("添加成员失败", err)
	}
	return &MemberView{UserID: u.ID, Username: u.Username, Nickname: u.Nickname, Role: role}, nil
}

// Remove 把成员移出项目。项目所有者不能被移除（管理员要移除请改 owner）。
func (s *MemberService) Remove(projectID, userID uint64) error {
	project, err := loadProject(s.DB, projectID)
	if err != nil {
		return err
	}
	if project.OwnerID == userID {
		return errBadParam("项目所有者不能被移除；如需转交请先修改项目所有者")
	}
	res := s.DB.Where("project_id = ? AND user_id = ?", projectID, userID).Delete(&model.ProjectMember{})
	if res.Error != nil {
		return errInternal("移除成员失败", res.Error)
	}
	if res.RowsAffected == 0 {
		return errNotFound("该用户不是项目成员")
	}
	return nil
}

// CanAccess 判断用户能否访问项目。
//
// 三条规则（顺序有意义）：
//  1. 管理员永远可以；
//  2. **项目还没有任何成员** → 视为「未设置成员」，对所有登录用户开放
//     （存量兼容：升级后不能把所有人锁在门外）；
//  3. 否则必须是成员。
func (s *MemberService) CanAccess(projectID, userID uint64, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	var p model.Project
	if err := s.DB.Select("id", "access_restricted").First(&p, projectID).Error; err != nil {
		if isNotFound(err) {
			return true, nil // 项目不存在交给 handler 报 404
		}
		return false, errInternal("查询项目失败", err)
	}
	if !p.AccessRestricted {
		return true, nil
	}
	var mine int64
	if err := s.DB.Model(&model.ProjectMember{}).
		Where("project_id = ? AND user_id = ?", projectID, userID).Count(&mine).Error; err != nil {
		return false, errInternal("查询项目成员失败", err)
	}
	return mine > 0, nil
}

// ProjectOfResource 把「资源类型 + 资源 id」解析成项目 id。
//
// 供访问控制中间件使用：路由里大量端点挂在资源 id 下（/cases/:id），
// 必须先知道它属于哪个项目才能判断访问权。
func (s *MemberService) ProjectOfResource(kind string, id uint64) (uint64, bool) {
	table := map[string]string{
		"projects":     "project",
		"cases":        "test_case",
		"environments": "environment",
		"apis":         "api_def",
		"suites":       "test_suite",
		"plans":        "test_plan",
		"datasets":     "param_dataset",
		"runs":         "run_record",
	}[kind]
	if table == "" {
		return 0, false
	}
	var row struct{ ProjectID uint64 }
	if table == "project" {
		var p model.Project
		if err := s.DB.Select("id").First(&p, id).Error; err != nil {
			return 0, false
		}
		return p.ID, true
	}
	if err := s.DB.Table(table).Select("project_id").Where("id = ?", id).Scan(&row).Error; err != nil || row.ProjectID == 0 {
		return 0, false
	}
	return row.ProjectID, true
}
