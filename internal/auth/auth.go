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
