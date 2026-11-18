package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// ShareService 管理接口文档的只读分享链接（F6）。
//
// 安全模型与 APIToken 一致：库里只存 sha256，明文只返回一次；
// 区别是分享链接**只读**，吊销即删除（过期由 ExpiresAt 判定）。
type ShareService struct {
	Deps
}

const shareTokenPrefix = "shr_"

// ErrShareExpired 表示链接存在但已过期（对外应回 410 而不是 404）。
var ErrShareExpired = errors.New("分享链接已过期")

// CreateShareReq 是生成分享链接的请求。
type CreateShareReq struct {
	Name string `json:"name"`
	// ExpiresInDays 为 0 表示永久有效。
	ExpiresInDays int `json:"expires_in_days"`
}

// IssuedShare 是创建结果（明文 token 仅此一次）。
type IssuedShare struct {
	ID        uint64     `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	Path      string     `json:"path"` // 相对路径：/open/docs/{token}
	ExpiresAt *time.Time `json:"expires_at"`
}

// ShareView 是链接列表项（token 只给前缀）。
type ShareView struct {
	ID        uint64     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Status    string     `json:"status"` // active / expired
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Issue 生成一条只读分享链接。
func (s *ShareService) Issue(projectID uint64, req CreateShareReq, userID uint64) (*IssuedShare, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	if req.ExpiresInDays < 0 {
		return nil, errBadParam("有效期天数不能为负数")
	}
	name := strings.TrimSpace(req.Name)

	raw, err := newShareToken()
	if err != nil {
		return nil, errInternal("生成分享令牌失败", err)
	}
	var exp *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresInDays)
		exp = &t
	}
	link := &model.ShareLink{
		ProjectID: projectID,
		Name:      name,
		TokenHash: sha256Hex(raw),
		ExpiresAt: exp,
		CreatedBy: userID,
	}
	if err := s.DB.Create(link).Error; err != nil {
		return nil, errInternal("保存分享链接失败", err)
	}
	return &IssuedShare{
		ID:        link.ID,
		Name:      name,
		Token:     raw,
		Path:      "/open/docs/" + raw,
		ExpiresAt: exp,
	}, nil
}

// List 返回项目的分享链接（掩码显示）。
func (s *ShareService) List(projectID uint64) ([]ShareView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	var list []model.ShareLink
	if err := s.DB.Where("project_id = ?", projectID).Order("id desc").Find(&list).Error; err != nil {
		return nil, errInternal("查询分享链接失败", err)
	}
	out := make([]ShareView, 0, len(list))
	for i := range list {
		out = append(out, ShareView{
			ID:        list[i].ID,
			Name:      list[i].Name,
			Prefix:    shareTokenPrefix + "…", // 哈希不可逆，前缀只能示意
			Status:    shareStatus(list[i].ExpiresAt),
			ExpiresAt: list[i].ExpiresAt,
			CreatedAt: list[i].CreatedAt,
		})
	}
	return out, nil
}

// Revoke 吊销（删除）一条链接。
func (s *ShareService) Revoke(projectID, id uint64) error {
	var link model.ShareLink
	if err := s.DB.Where("project_id = ? AND id = ?", projectID, id).First(&link).Error; err != nil {
		if isNotFound(err) {
			return errNotFound("分享链接不存在（id=%d）", id)
		}
		return errInternal("查询分享链接失败", err)
	}
	if err := s.DB.Delete(&link).Error; err != nil {
		return errInternal("删除分享链接失败", err)
	}
	return nil
}

// Verify 校验明文 token，返回链接与所属项目。
//
// 三种结果必须可区分：不存在/已吊销（404）、已过期（410）、有效。
func (s *ShareService) Verify(raw string) (*model.ShareLink, *model.Project, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil, errNotFound("分享链接不存在")
	}
	var link model.ShareLink
	if err := s.DB.Where("token_hash = ?", sha256Hex(raw)).First(&link).Error; err != nil {
		if isNotFound(err) {
			return nil, nil, errNotFound("分享链接不存在或已失效")
		}
		return nil, nil, errInternal("校验分享链接失败", err)
	}
	project, err := loadProject(s.DB, link.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		return nil, nil, ErrShareExpired
	}
	return &link, project, nil
}

func shareStatus(exp *time.Time) string {
	if exp != nil && time.Now().After(*exp) {
		return "expired"
	}
	return "active"
}

// newShareToken 生成 32 字节随机 token（shr_ 前缀便于识别来源）。
func newShareToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return shareTokenPrefix + hex.EncodeToString(buf), nil
}
