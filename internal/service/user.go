package service

import (
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/logx"
)

// UserService 负责账号管理。
//
// 范围刻意很小（方案 10.2：只 admin / member 两档，不做 RBAC）：
// 建号、改昵称/角色/启用状态、删号、改密码、重置密码。
//
// 这个文件里真正的价值不在 CRUD，而在**三条守卫**：
//
//  1. 不能把自己降级或禁用 —— 否则管理员一点就把自己关在门外；
//  2. 系统里必须永远剩至少一个"可用的管理员"（role=admin 且 enabled）
//     —— 这是防止"所有人都进不去管理界面"的唯一保险；
//  3. 改了角色/启用状态就必须吊销该用户的会话 —— 会话表里缓存着 Principal，
//     不吊销的话，被降级的管理员在 TTL（默认 24h）内继续拥有管理员权限。
//
// 第 3 条是最容易被漏掉的一条：数据库改对了、页面也刷新了，
// 但对方的浏览器里那份会话仍然生效，"改了个寂寞"。
type UserService struct{ Deps }

// ---------------------------------------------------------------------------
// 视图与请求
// ---------------------------------------------------------------------------

// UserView 是账号列表项。
//
// 不直接返回 model.User 的原因：要额外带上"在线会话数"。
// 它不是为了好看 —— 管理员禁用某人后，这个数字归零才是
// 「吊销真的发生了」的可见证据（否则只能靠猜）。
type UserView struct {
	ID             uint64 `json:"id"`
	Username       string `json:"username"`
	Nickname       string `json:"nickname"`
	Role           string `json:"role"`
	Enabled        bool   `json:"enabled"`
	OnlineSessions int    `json:"online_sessions"`
	CreatedAt      string `json:"created_at"`
}

// UserListQuery 是账号列表的过滤条件。
type UserListQuery struct {
	Keyword string
	Role    string
}

// CreateUserReq 是新建账号的请求。
type CreateUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
	Enabled  *bool  `json:"enabled"` // 留空默认启用
}

// UpdateUserReq 是修改账号的请求。
//
// **没有 Username 字段**，这是刻意的：用户名是登录标识，且会写进
// run_record.trigger_by 关联的历史；允许改名等于让历史记录悄悄换人。
// 要换名字就新建账号、停用旧账号。
type UpdateUserReq struct {
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
	Enabled  *bool  `json:"enabled"`
}

// ---------------------------------------------------------------------------
// 校验
// ---------------------------------------------------------------------------

// usernameRe 允许字母数字与 . _ - @，便于直接用邮箱当用户名。
//
// 不含首尾限制以外的花样：内网 10 人团队，规则越简单越不会被绕过。
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{1,63}$`)

// BcryptMaxBytes 是 bcrypt 的输入上限。
//
// ⚠️ bcrypt **静默截断**超过 72 字节的输入。若不拦，用户设了 100 位密码，
// 实际只有前 72 字节参与校验，而且没人会知道。宁可在这里报错。
const BcryptMaxBytes = 72

// MinPasswordLen 是密码最小长度。
const MinPasswordLen = 8

// weakPasswords 是明确要拦掉的弱密码。
//
// `admin123` 在列是因为它就是种子账号的初始密码 —— 最该被改掉的那个，
// 结果最容易被原样填回来。
var weakPasswords = map[string]struct{}{
	"admin123":   {},
	"password":   {},
	"12345678":   {},
	"11111111":   {},
	"qwertyui":   {},
	"abc12345":   {},
	"admin@123":  {},
	"passw0rd":   {},
	"hrp123456":  {},
	"httprunner": {},
}

// normalizeUsername 去除空白并统一转小写。
//
// 转小写是为了让用户名天然大小写不敏感：`Admin` 与 `admin` 若被当成两个账号，
// 登录时一次大小写打错就会得到"用户名或密码错误"，而用户会坚信自己没打错。
func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// validatePassword 校验密码强度，返回可直接展示给用户的原因。
//
// 规则刻意只有四条：长度、无空白、不与用户名相同、不在弱密码表里。
// 内网平台的密码策略一旦复杂起来，结果一定是全员把密码写在便签上。
func validatePassword(pw, username string) error {
	if strings.ContainsAny(pw, " \t\r\n") {
		return errBadParam("密码不能包含空白字符")
	}
	if len(pw) < MinPasswordLen {
		return errBadParam("密码至少 %d 位", MinPasswordLen)
	}
	if len(pw) > BcryptMaxBytes {
		return errBadParam("密码不能超过 %d 字节（bcrypt 会静默截断更长的输入）", BcryptMaxBytes)
	}
	if username != "" && strings.EqualFold(pw, username) {
		return errBadParam("密码不能与用户名相同")
	}
	if _, weak := weakPasswords[strings.ToLower(pw)]; weak {
		return errBadParam("该密码过于常见，请换一个")
	}
	return nil
}

// normalizeRole 校验并归一化角色。
func normalizeRole(role string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", model.RoleMember:
		return model.RoleMember, true
	case model.RoleAdmin:
		return model.RoleAdmin, true
	default:
		return "", false
	}
}

// hashPassword 生成 bcrypt 哈希。
func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", errInternal("生成密码哈希失败", err)
	}
	return string(h), nil
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 返回账号分页列表。
func (s *UserService) List(q UserListQuery, page Page) ([]UserView, int64, error) {
	tx := s.DB.Model(&model.User{})
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		like := "%" + strings.ToLower(kw) + "%"
		tx = tx.Where("username LIKE ? OR LOWER(nickname) LIKE ?", like, like)
	}
	if role := strings.TrimSpace(q.Role); role != "" {
		tx = tx.Where("role = ?", role)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, errInternal("统计账号数失败", err)
	}

	page = page.Normalize()
	var users []model.User
	if err := tx.Order("id asc").Offset(page.Offset()).Limit(page.Limit()).Find(&users).Error; err != nil {
		return nil, 0, errInternal("查询账号列表失败", err)
	}

	views := make([]UserView, 0, len(users))
	for _, u := range users {
		views = append(views, UserView{
			ID:             u.ID,
			Username:       u.Username,
			Nickname:       u.Nickname,
			Role:           u.Role,
			Enabled:        u.Enabled,
			OnlineSessions: s.sessionsOf(u.ID),
			CreatedAt:      u.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return views, total, nil
}

// Get 返回单个账号。
func (s *UserService) Get(id uint64) (*model.User, error) { return loadUser(s.DB, id) }

// ---------------------------------------------------------------------------
// 写操作
// ---------------------------------------------------------------------------

// Create 新建账号。
func (s *UserService) Create(req CreateUserReq) (*model.User, error) {
	username := normalizeUsername(req.Username)
	if !usernameRe.MatchString(username) {
		return nil, errBadParam(
			"用户名非法（%q）：2-64 位，只允许字母、数字与 . _ - @，且必须以字母或数字开头", req.Username)
	}
	if err := validatePassword(req.Password, username); err != nil {
		return nil, err
	}
	role, ok := normalizeRole(req.Role)
	if !ok {
		return nil, errBadParam("角色只能是 admin 或 member，收到 %q", req.Role)
	}

	var exists int64
	if err := s.DB.Model(&model.User{}).Where("username = ?", username).Count(&exists).Error; err != nil {
		return nil, errInternal("检查用户名是否重复失败", err)
	}
	if exists > 0 {
		return nil, errConflict("用户名 %q 已存在", username)
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = username
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	u := &model.User{
		Username: username,
		Password: hash,
		Nickname: nickname,
		Role:     role,
		Enabled:  enabled,
	}
	if err := s.DB.Create(u).Error; err != nil {
		if isDuplicated(err) {
			return nil, errConflict("用户名 %q 已存在", username)
		}
		return nil, errInternal("创建账号失败", err)
	}
	logx.L().Info().Str("username", u.Username).Str("role", u.Role).Msg("创建账号")
	return u, nil
}

// Update 修改账号的昵称、角色与启用状态。
//
// operatorID 用于自我保护检查；它由调用方从登录态取得，不接受请求体传入。
func (s *UserService) Update(id, operatorID uint64, req UpdateUserReq) (*model.User, error) {
	u, err := loadUser(s.DB, id)
	if err != nil {
		return nil, err
	}

	role, ok := normalizeRole(req.Role)
	if !ok {
		return nil, errBadParam("角色只能是 admin 或 member，收到 %q", req.Role)
	}
	enabled := u.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = u.Username
	}

	// 守卫 1：不能把自己降级或禁用。
	//
	// 单独做这个检查（而不只依赖"最后一个管理员"）是为了给出可执行的提示：
	// "还有别的管理员，所以技术上允许"与"你不该把自己关在门外"是两件事。
	if id == operatorID {
		if role != model.RoleAdmin {
			return nil, errForbidden("不能把自己的角色降为 %s（请让另一位管理员来改）", role)
		}
		if !enabled {
			return nil, errForbidden("不能禁用自己的账号")
		}
	}

	// 守卫 2：不能干掉最后一个可用管理员。
	losingAdmin := u.Role == model.RoleAdmin && u.Enabled && (role != model.RoleAdmin || !enabled)
	if losingAdmin {
		if err := s.assertAnotherAdminExists(id); err != nil {
			return nil, err
		}
	}

	roleChanged := role != u.Role
	enabledChanged := enabled != u.Enabled
	nicknameChanged := nickname != u.Nickname

	u.Nickname = nickname
	u.Role = role
	u.Enabled = enabled

	if err := s.DB.Model(&model.User{}).Where("id = ?", id).
		Updates(map[string]any{"nickname": nickname, "role": role, "enabled": enabled}).Error; err != nil {
		return nil, errInternal("更新账号失败", err)
	}

	// 守卫 3：权限变更必须让旧会话失效。
	switch {
	case roleChanged || enabledChanged:
		if n := s.revokeSessions(id, "角色或启用状态变更"); n > 0 {
			logx.L().Info().Uint64("user_id", id).Int("revoked", n).
				Msg("账号权限变更，已吊销其全部会话")
		}
	case nicknameChanged:
		// 只改昵称不影响权限，刷新一下会话里的展示信息即可，
		// 不必把人踢下线（否则管理员改个昵称全组都被登出，很招人烦）。
		s.refreshPrincipal(id, func(p auth.Principal) auth.Principal {
			p.Nickname = nickname
			return p
		})
	}
	return u, nil
}

// Delete 删除账号（软删除，保留 run_record 里的历史关联）。
func (s *UserService) Delete(id, operatorID uint64) error {
	u, err := loadUser(s.DB, id)
	if err != nil {
		return err
	}
	if id == operatorID {
		return errForbidden("不能删除自己的账号")
	}
	if u.Role == model.RoleAdmin && u.Enabled {
		if err := s.assertAnotherAdminExists(id); err != nil {
			return err
		}
	}

	// 先吊销会话再删记录：顺序反过来的话，删完再吊销就查不到这个用户了，
	// 而会话表按 userID 匹配，其实不依赖用户行存在——但先吊销更直观。
	s.revokeSessions(id, "账号被删除")

	if err := s.DB.Delete(&model.User{}, id).Error; err != nil {
		return errInternal("删除账号失败", err)
	}
	logx.L().Info().Uint64("user_id", id).Str("username", u.Username).Msg("删除账号")
	return nil
}

// ChangeOwnPassword 用于本人修改密码，必须验旧密码。
//
// currentToken 是发起这次请求所用的会话 ID：改完密码要把**其它**设备上的
// 会话踢掉（安全），但不能踢掉正在操作的这一个（否则用户改完立刻被登出，
// 会以为改密码把账号弄坏了）。
func (s *UserService) ChangeOwnPassword(userID uint64, currentToken, oldPW, newPW string) error {
	u, err := loadUser(s.DB, userID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(oldPW)) != nil {
		return errBadParam("原密码不正确")
	}
	if err := validatePassword(newPW, u.Username); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(newPW)) == nil {
		return errBadParam("新密码不能与原密码相同")
	}

	hash, err := hashPassword(newPW)
	if err != nil {
		return err
	}
	if err := s.DB.Model(&model.User{}).Where("id = ?", userID).
		Update("password", hash).Error; err != nil {
		return errInternal("更新密码失败", err)
	}

	if n := s.revokeExcept(userID, currentToken, "本人修改密码"); n > 0 {
		logx.L().Info().Uint64("user_id", userID).Int("revoked", n).
			Msg("修改密码，已吊销其它设备上的会话")
	}
	return nil
}

// ResetPassword 由管理员重置他人的密码。
//
// 重置自己的密码请走 ChangeOwnPassword：那条路径要验旧密码，
// 而且不会把自己踢下线。这里豁免旧密码校验，因此必须禁止作用于自己
// —— 否则"忘记旧密码"就能成为一条绕过旧密码校验的后门。
func (s *UserService) ResetPassword(targetID, operatorID uint64, newPW string) error {
	if targetID == operatorID {
		return errForbidden("重置自己的密码请使用「修改密码」（需要验证原密码）")
	}
	u, err := loadUser(s.DB, targetID)
	if err != nil {
		return err
	}
	if err := validatePassword(newPW, u.Username); err != nil {
		return err
	}

	hash, err := hashPassword(newPW)
	if err != nil {
		return err
	}
	if err := s.DB.Model(&model.User{}).Where("id = ?", targetID).
		Update("password", hash).Error; err != nil {
		return errInternal("重置密码失败", err)
	}

	// 重置密码的意义就是"把对方踢下线，让他用新密码进来"，
	// 所以这里连他当前的会话一起吊销。
	if n := s.revokeSessions(targetID, "管理员重置密码"); n > 0 {
		logx.L().Info().Uint64("user_id", targetID).Int("revoked", n).
			Int("operator_id", int(operatorID)).Msg("重置密码，已吊销目标用户全部会话")
	}
	return nil
}

// ---------------------------------------------------------------------------
// 守卫与会话
// ---------------------------------------------------------------------------

// assertAnotherAdminExists 断言除 excludeID 之外还存在可用的管理员。
//
// 判据是「role=admin 且 enabled=true」：被禁用的管理员无法登录，
// 不能算数——这是这类守卫最常见的写错之处（只数 role=admin，
// 结果把系统锁死在"有一个管理员但登不进去"的状态）。
func (s *UserService) assertAnotherAdminExists(excludeID uint64) error {
	var n int64
	err := s.DB.Model(&model.User{}).
		Where("role = ? AND enabled = ? AND id <> ?", model.RoleAdmin, true, excludeID).
		Count(&n).Error
	if err != nil {
		return errInternal("检查剩余管理员数量失败", err)
	}
	if n == 0 {
		return errForbidden("这是最后一个可用的管理员账号，禁用、降级或删除它会导致无人能管理系统")
	}
	return nil
}

func (s *UserService) sessionsOf(userID uint64) int {
	if s.Sessions == nil {
		return 0
	}
	return s.Sessions.SessionsOf(userID)
}

// revokeSessions 吊销某用户全部会话。
//
// Sessions 未接线时**不静默跳过**：那会让"禁用用户"看起来成功、
// 实际对方的登录态还在。宁可打一条 error 日志把问题暴露出来。
func (s *UserService) revokeSessions(userID uint64, reason string) int {
	if s.Sessions == nil {
		logx.L().Error().Uint64("user_id", userID).Str("reason", reason).
			Msg("会话表未接线，无法吊销会话 —— 该用户的登录态在 TTL 内仍然有效")
		return 0
	}
	return s.Sessions.Revoke(userID)
}

func (s *UserService) revokeExcept(userID uint64, keepToken, reason string) int {
	if s.Sessions == nil {
		logx.L().Error().Uint64("user_id", userID).Str("reason", reason).
			Msg("会话表未接线，无法吊销会话")
		return 0
	}
	return s.Sessions.RevokeExcept(userID, keepToken)
}

func (s *UserService) refreshPrincipal(userID uint64, fn func(auth.Principal) auth.Principal) {
	if s.Sessions == nil {
		return
	}
	s.Sessions.RefreshPrincipal(userID, fn)
}

// loadUser 按 ID 读取账号。
//
// 这里不复用 repo.IsNotFound 那套包装，而是直接落成 40002，
// 让"账号不存在"与"密码错误"在响应上可区分——前者只有管理员会看到。
func loadUser(db *gorm.DB, id uint64) (*model.User, error) {
	var u model.User
	if err := db.First(&u, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errNotFound("账号 %d 不存在", id)
		}
		return nil, errInternal("查询账号失败", err)
	}
	return &u, nil
}
