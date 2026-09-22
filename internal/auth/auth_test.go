package auth

import (
	"sync"
	"testing"
	"time"
)

func newTestStore() *Store { return NewStore(time.Hour) }

func p(id uint64, name, role string) Principal {
	return Principal{UserID: id, Username: name, Nickname: name, Role: role}
}

// Revoke 是"禁用/降级用户"能真正生效的前提，因此它的语义必须锁住：
// 只动目标用户的会话，别人的一个都不能碰。
func TestRevoke只影响目标用户(t *testing.T) {
	s := newTestStore()
	a1 := s.Create(p(1, "alice", "admin"))
	a2 := s.Create(p(1, "alice", "admin"))
	b1 := s.Create(p(2, "bob", "member"))

	if n := s.Revoke(1); n != 2 {
		t.Errorf("Revoke 返回 %d, want 2", n)
	}
	if _, ok := s.Get(a1); ok {
		t.Error("alice 的会话 a1 应已失效")
	}
	if _, ok := s.Get(a2); ok {
		t.Error("alice 的会话 a2 应已失效")
	}
	if _, ok := s.Get(b1); !ok {
		t.Error("bob 的会话被误杀 —— 吊销必须按 userID 精确匹配")
	}
}

func TestRevoke对无会话用户是空操作(t *testing.T) {
	s := newTestStore()
	s.Create(p(1, "alice", "admin"))
	if n := s.Revoke(999); n != 0 {
		t.Errorf("Revoke 不存在的用户返回 %d, want 0", n)
	}
	if s.SessionsOf(1) != 1 {
		t.Error("不该影响其它用户")
	}
}

// 改密码要踢掉其它设备，但不能把正在操作的这一次也踢掉 ——
// 否则用户改完密码立刻被登出，会以为改密码把账号弄坏了。
func TestRevokeExcept保留当前会话(t *testing.T) {
	s := newTestStore()
	keep := s.Create(p(1, "alice", "admin"))
	old1 := s.Create(p(1, "alice", "admin"))
	other := s.Create(p(2, "bob", "member"))

	if n := s.RevokeExcept(1, keep); n != 1 {
		t.Errorf("RevokeExcept 返回 %d, want 1", n)
	}
	if _, ok := s.Get(keep); !ok {
		t.Error("当前会话必须保留")
	}
	if _, ok := s.Get(old1); ok {
		t.Error("其它设备上的会话应被踢")
	}
	if _, ok := s.Get(other); !ok {
		t.Error("别人的会话被误杀")
	}
}

func TestRevokeExcept空token等价于全吊销(t *testing.T) {
	s := newTestStore()
	tok := s.Create(p(1, "alice", "admin"))
	if n := s.RevokeExcept(1, ""); n != 1 {
		t.Errorf("返回 %d, want 1", n)
	}
	if _, ok := s.Get(tok); ok {
		t.Error("空 keepToken 时不该保留任何会话")
	}
}

func TestRefreshPrincipal就地更新且不动会话数(t *testing.T) {
	s := newTestStore()
	t1 := s.Create(p(1, "alice", "admin"))
	t2 := s.Create(p(1, "alice", "admin"))
	s.Create(p(2, "bob", "member"))

	n := s.RefreshPrincipal(1, func(old Principal) Principal {
		old.Nickname = "爱丽丝"
		old.Username = "alice2"
		return old
	})
	if n != 2 {
		t.Errorf("RefreshPrincipal 返回 %d, want 2", n)
	}
	for _, tok := range []string{t1, t2} {
		got, ok := s.Get(tok)
		if !ok {
			t.Fatalf("会话不该因刷新而失效: %s", tok)
		}
		if got.Nickname != "爱丽丝" || got.Username != "alice2" {
			t.Errorf("主体未更新: %+v", got)
		}
	}
	if s.SessionsOf(2) != 1 {
		t.Error("不该影响其它用户")
	}
}

func TestSessionsOf只数目标用户(t *testing.T) {
	s := newTestStore()
	s.Create(p(1, "alice", "admin"))
	s.Create(p(1, "alice", "admin"))
	s.Create(p(2, "bob", "member"))

	if got := s.SessionsOf(1); got != 2 {
		t.Errorf("SessionsOf(1) = %d, want 2", got)
	}
	if got := s.SessionsOf(2); got != 1 {
		t.Errorf("SessionsOf(2) = %d, want 1", got)
	}
	if got := s.SessionsOf(3); got != 0 {
		t.Errorf("SessionsOf(3) = %d, want 0", got)
	}
}

// 吊销要和 Get 的过期清理共用同一把锁；并发下任何一处漏锁都会
// 表现为"偶发吊销失败"，这类 bug 只在生产环境偶现，必须在这里压住。
func Test并发吊销与读取不串台(t *testing.T) {
	s := newTestStore()
	tokens := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		tokens = append(tokens, s.Create(p(uint64(i%5), "u", "member")))
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			s.Revoke(id)
		}(uint64(i))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, tok := range tokens {
			s.Get(tok)
		}
	}()
	wg.Wait()

	if n := s.SessionsOf(0) + s.SessionsOf(1) + s.SessionsOf(2) + s.SessionsOf(3) + s.SessionsOf(4); n != 0 {
		t.Errorf("全部用户都被吊销后剩余会话数 = %d, want 0", n)
	}
}
