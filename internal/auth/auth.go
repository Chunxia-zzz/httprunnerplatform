// Package auth 提供登录态管理。
//
// M1 采用最简方案：进程内会话表 + HttpOnly Cookie。
//
// 为什么不上 JWT：平台是内网私有化部署，单实例运行，
// 会话表能天然支持"踢下线"与"立即吊销"，而 JWT 需要额外的黑名单。
// 引入 Redis / JWT 属于为不存在的规模问题付成本（方案 6.2 的同一原则）。
//
// M2 引入 api_token（CI 用）时，本包增加 TokenStore，会话机制不变。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// CookieName 是会话 Cookie 名。
const CookieName = "hrp_session"

// Principal 是登录主体。
type Principal struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
}

// IsAdmin 判断是否管理员。
func (p Principal) IsAdmin() bool { return p.Role == "admin" }

type session struct {
	principal Principal
	expireAt  time.Time
}

// Store 是进程内会话表。
type Store struct {
	mu  sync.RWMutex
	m   map[string]session
	ttl time.Duration
}

// NewStore 创建会话表。ttl <= 0 时使用 24 小时。
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Store{m: make(map[string]session), ttl: ttl}
}

// Create 建立会话并返回会话 ID。
func (s *Store) Create(p Principal) string {
	token := randomToken(32)
	s.mu.Lock()
	s.m[token] = session{principal: p, expireAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return token
}

// Get 查询会话，过期即删除。
func (s *Store) Get(token string) (Principal, bool) {
	if token == "" {
		return Principal{}, false
	}
	s.mu.RLock()
	sess, ok := s.m[token]
	s.mu.RUnlock()
	if !ok {
		return Principal{}, false
	}
	if time.Now().After(sess.expireAt) {
		s.Delete(token)
		return Principal{}, false
	}
	return sess.principal, true
}

// Delete 注销会话。
func (s *Store) Delete(token string) {
	s.mu.Lock()
	delete(s.m, token)
	s.mu.Unlock()
}

// Revoke 吊销某个用户的全部会话，返回吊销数量。
//
// ⚠️ 这不是可选项。会话表缓存了 Principal（含 Role），中间件信任这份缓存，
// 因此"禁用/降级一个用户"若只改了数据库，**他的会话在 TTL 内仍然有效**：
// 被降级的管理员会继续拥有管理员权限，被禁用的账号还能继续操作。
// 凡是改变用户身份或权限的动作（禁用、改角色、重置密码、删除），
// 都必须调用它，否则改动等于没生效。
func (s *Store) Revoke(userID uint64) int {
	return s.revoke(userID, "")
}

// RevokeExcept 吊销某用户除 keepToken 之外的全部会话。
//
// 用于"本人修改密码"：安全上要求把其它设备上的登录踢掉，
// 但正在操作的这一次不该被踢——否则用户改完密码立刻被登出，
// 只会让人以为改密码把账号弄坏了。
func (s *Store) RevokeExcept(userID uint64, keepToken string) int {
	return s.revoke(userID, keepToken)
}

func (s *Store) revoke(userID uint64, keepToken string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.m {
		if v.principal.UserID != userID {
			continue
		}
		if keepToken != "" && k == keepToken {
			continue
		}
		delete(s.m, k)
		n++
	}
	return n
}

// RefreshPrincipal 就地更新某用户所有会话里的主体信息，返回更新数量。
//
// 只适用于**不改变权限**的字段（昵称、用户名）。改角色或启用状态请用 Revoke：
// 那类变更必须让用户重新登录，否则中间件读到的仍是旧的构造时快照。
func (s *Store) RefreshPrincipal(userID uint64, fn func(Principal) Principal) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.m {
		if v.principal.UserID != userID {
			continue
		}
		v.principal = fn(v.principal)
		s.m[k] = v
		n++
	}
	return n
}

// SessionsOf 返回某用户当前的有效会话数。
//
// 用途：管理员页面上显示"该账号有几个在线会话"，
// 以及在单测里断言吊销真的发生了（而不是只看返回值）。
func (s *Store) SessionsOf(userID uint64) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, v := range s.m {
		if v.principal.UserID == userID {
			n++
		}
	}
	return n
}

// TTL 返回会话有效期。
func (s *Store) TTL() time.Duration { return s.ttl }

// GC 清理过期会话，返回清理数量。由后台定时任务调用。
func (s *Store) GC() int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.m {
		if now.After(v.expireAt) {
			delete(s.m, k)
			n++
		}
	}
	return n
}

// randomToken 生成指定字节长度的十六进制随机串。
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败属于不可恢复的环境问题；
		// 此处不 panic 会让调用方拿到可预测的弱 token，因此直接 panic。
		panic("auth: 生成随机数失败: " + err.Error())
	}
	return hex.EncodeToString(b)
}
