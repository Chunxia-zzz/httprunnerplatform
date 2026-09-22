package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// TokenService 负责 CI 令牌的签发与校验。
//
// 两条最要紧的规则：
//
//  1. **只存哈希**（决策 4）。明文在创建时返回一次，之后只能看到 Prefix。
//     用 sha256 而不是 bcrypt：bcrypt 的意义是抗暴力破解（密码有猜测空间，
//     要故意算慢），而 CI Token 是 32 字节随机数，没有猜测空间 ——
//     用 bcrypt 只会让每次触发白付 100ms 的 KDF 开销，换不到任何安全性。
//  2. **按项目签发**（决策 1），且请求里的 project_id 必须与令牌所属项目一致。
//     不校验的话，一个项目的 token 能触发任意项目的执行，按项目签发等于白做。
type TokenService struct{ Deps }

// 令牌前缀。带上它有两个用处：
//   - 用户在 CI 的 secret 列表里一眼能认出这是本平台的令牌；
//   - 密钥扫描工具（GitHub secret scanning 之类）能按前缀识别并告警。
const tokenPrefix = "hrp_ci_"

// 权限。默认两个都给：CI 触发之后通常紧接着就要读结果。
const (
	ScopeRunTrigger = "run:trigger"
	ScopeRunRead    = "run:read"
)

// DefaultTokenScope 是新建令牌的默认权限。
const DefaultTokenScope = ScopeRunTrigger + "," + ScopeRunRead

// TokenView 是列表里的令牌（不含任何秘密）。
type TokenView struct {
	ID         uint64  `json:"id"`
	ProjectID  uint64  `json:"project_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	Scope      string  `json:"scope"`
	ExpireAt   *string `json:"expire_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
	// Expired 让"这个令牌已经不能用了"在列表里一眼可见。
	// 否则用户要自己拿 expire_at 跟现在比 —— 而这件事他多半懒得做。
	Expired bool `json:"expired"`
}

// IssuedToken 是签发结果，**唯一一次**带上明文。
type IssuedToken struct {
	TokenView
	// Token 只有这里会给，之后任何接口都不再返回。
	Token string `json:"token"`
}

// CreateTokenReq 是签发请求。
type CreateTokenReq struct {
	Name string `json:"name"`
	// Scope 留空则用默认（触发 + 读结果）。
	Scope string `json:"scope"`
	// TTLDays 为 0 表示不过期。
	TTLDays int `json:"ttl_days"`
}

// ---------------------------------------------------------------------------
// 签发与查询
// ---------------------------------------------------------------------------

// Issue 签发一枚令牌，返回明文（仅此一次）。
func (s *TokenService) Issue(projectID uint64, req CreateTokenReq) (*IssuedToken, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errBadParam("令牌名称不能为空")
	}
	scope, err := normalizeScope(req.Scope)
	if err != nil {
		return nil, err
	}
	if req.TTLDays < 0 {
		return nil, errBadParam("有效期天数不能为负数")
	}

	var exists int64
	if err := s.DB.Model(&model.APIToken{}).
		Where("project_id = ? AND name = ?", projectID, name).Count(&exists).Error; err != nil {
		return nil, errInternal("检查令牌名称是否重复失败", err)
	}
	if exists > 0 {
		return nil, errConflict("项目下已存在名为 %q 的令牌", name)
	}

	raw, err := newRawToken()
	if err != nil {
		return nil, errInternal("生成令牌失败", err)
	}
	hash := sha256Hex(raw)

	tk := &model.APIToken{
		ProjectID: projectID,
		Name:      name,
		TokenHash: hash,
		Prefix:    tokenPrefix + hash[:8],
		Scope:     scope,
	}
	if req.TTLDays > 0 {
		exp := time.Now().AddDate(0, 0, req.TTLDays)
		tk.ExpireAt = &exp
	}
	if err := s.DB.Create(tk).Error; err != nil {
		if isDuplicated(err) {
			// 32 字节随机数撞库的概率可以忽略；真撞了就让用户重试，
			// 不要在这里静默换一个 —— 静默重试会掩盖真正的生成问题。
			return nil, errInternal("生成的令牌与已有令牌重复，请重试", err)
		}
		return nil, errInternal("创建令牌失败", err)
	}
	logx.L().Info().Uint64("token_id", tk.ID).Uint64("project_id", projectID).
		Str("name", name).Str("prefix", tk.Prefix).Msg("签发 CI 令牌")

	v := tokenView(tk, time.Now())
	return &IssuedToken{TokenView: v, Token: raw}, nil
}

// List 列举项目下的令牌。
func (s *TokenService) List(projectID uint64) ([]TokenView, error) {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return nil, err
	}
	var list []model.APIToken
	if err := s.DB.Where("project_id = ?", projectID).
		Order("id desc").Find(&list).Error; err != nil {
		return nil, errInternal("查询令牌列表失败", err)
	}
	now := time.Now()
	out := make([]TokenView, 0, len(list))
	for i := range list {
		out = append(out, tokenView(&list[i], now))
	}
	return out, nil
}

// Revoke 吊销令牌（软删除）。
//
// 吊销即刻生效：校验走的是数据库查询，没有缓存，也就没有"吊销了但还能用"的窗口。
func (s *TokenService) Revoke(projectID, id uint64) error {
	if _, err := loadProject(s.DB, projectID); err != nil {
		return err
	}
	var tk model.APIToken
	if err := s.DB.Where("project_id = ? AND id = ?", projectID, id).First(&tk).Error; err != nil {
		if isNotFound(err) {
			return errNotFound("令牌不存在（id=%d）", id)
		}
		return errInternal("查询令牌失败", err)
	}
	if err := s.DB.Delete(&model.APIToken{}, id).Error; err != nil {
		return errInternal("吊销令牌失败", err)
	}
	logx.L().Info().Uint64("token_id", id).Str("prefix", tk.Prefix).Msg("吊销 CI 令牌")
	return nil
}

// ---------------------------------------------------------------------------
// 校验
// ---------------------------------------------------------------------------

// Verify 校验一枚令牌的明文，返回它绑定的记录。
//
// 返回值里**没有明文**，调用方拿它做项目与权限判断即可。
func (s *TokenService) Verify(raw string) (*model.APIToken, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, response.New(response.CodeUnauthorized, "缺少令牌")
	}
	hash := sha256Hex(raw)

	var tk model.APIToken
	if err := s.DB.Where("token = ?", hash).First(&tk).Error; err != nil {
		if isNotFound(err) {
			return nil, response.New(response.CodeUnauthorized, "令牌无效")
		}
		return nil, errInternal("查询令牌失败", err)
	}
	if tk.ExpireAt != nil && tk.ExpireAt.Before(time.Now()) {
		return nil, response.New(response.CodeUnauthorized, "令牌已过期")
	}

	// LastUsedAt 每次都回写：它是判断"这枚令牌还活着吗"的唯一依据。
	// 写失败不影响本次请求 —— 为了一个审计字段把 CI 触发打断不值得。
	now := time.Now()
	if err := s.DB.Model(&model.APIToken{}).Where("id = ?", tk.ID).
		Update("last_used_at", &now).Error; err != nil {
		logx.L().Warn().Err(err).Uint64("token_id", tk.ID).Msg("回写令牌使用时间失败")
	}
	tk.LastUsedAt = &now
	return &tk, nil
}

// HasScope 判断令牌是否具备某项权限。
func (s *TokenService) HasScope(tk *model.APIToken, want string) bool {
	if tk == nil {
		return false
	}
	for _, part := range strings.Split(tk.Scope, ",") {
		if strings.TrimSpace(part) == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 内部
// ---------------------------------------------------------------------------

func tokenView(tk *model.APIToken, now time.Time) TokenView {
	v := TokenView{
		ID:        tk.ID,
		ProjectID: tk.ProjectID,
		Name:      tk.Name,
		Prefix:    tk.Prefix,
		Scope:     tk.Scope,
		CreatedAt: tk.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if tk.ExpireAt != nil {
		s := tk.ExpireAt.Format("2006-01-02 15:04:05")
		v.ExpireAt = &s
		v.Expired = tk.ExpireAt.Before(now)
	}
	if tk.LastUsedAt != nil {
		s := tk.LastUsedAt.Format("2006-01-02 15:04:05")
		v.LastUsedAt = &s
	}
	return v
}

// newRawToken 生成一枚明文令牌：前缀 + 32 字节随机数（hex）。
func newRawToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(buf), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// normalizeScope 校验权限列表，只接受已知的两项。
func normalizeScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return DefaultTokenScope, nil
	}
	var out []string
	for _, part := range strings.Split(scope, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if p != ScopeRunTrigger && p != ScopeRunRead {
			return "", errBadParam("权限只能是 %s 或 %s，收到 %q", ScopeRunTrigger, ScopeRunRead, p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return "", errBadParam("权限列表不能为空")
	}
	return strings.Join(out, ","), nil
}
