package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// testUserDeps 在 testDeps 基础上接一个真实会话表。
//
// 用真实 Store 而不是替身，是因为本文件要验的正是
// "改动之后对方的会话到底还在不在" —— 用替身等于把被测对象换掉了。
func testUserDeps(t *testing.T) (Deps, *auth.Store) {
	t.Helper()
	d := testDeps(t)
	store := auth.NewStore(time.Hour)
	d.Sessions = store
	return d, store
}

func newUserSvc(t *testing.T) (*UserService, Deps, *auth.Store) {
	t.Helper()
	d, store := testUserDeps(t)
	return &UserService{Deps: d}, d, store
}

// seedUser 直接写库建账号，返回明文密码。
func seedUser(t *testing.T, d Deps, username, role string, enabled bool) *model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("initpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	u := &model.User{
		Username: username,
		Password: string(hash),
		Nickname: username,
		Role:     role,
		Enabled:  enabled,
	}
	if err := d.DB.Create(u).Error; err != nil {
		t.Fatalf("建账号失败: %v", err)
	}
	return u
}

// login 建一个会话并返回 token，用来模拟"某人已经登录着"。
func login(store *auth.Store, u *model.User) string {
	return store.Create(auth.Principal{
		UserID: u.ID, Username: u.Username, Nickname: u.Nickname, Role: u.Role,
	})
}

func codeOfErr(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var biz *response.Error
	if !errors.As(err, &biz) {
		t.Fatalf("不是业务错误: %v", err)
	}
	return biz.Code
}

// ---------------------------------------------------------------------------
// 创建
// ---------------------------------------------------------------------------

func TestCreate对用户名做小写归一(t *testing.T) {
	svc, _, _ := newUserSvc(t)

	u, err := svc.Create(CreateUserReq{Username: "  Alice  ", Password: "goodpass123", Role: "admin"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("用户名 = %q, want alice（必须小写归一，否则 Admin 与 admin 会变成两个账号）", u.Username)
	}
	if u.Nickname != "alice" {
		t.Errorf("昵称留空时应回落成用户名，实际 %q", u.Nickname)
	}

	// 大小写不同视为重复
	_, err = svc.Create(CreateUserReq{Username: "ALICE", Password: "goodpass123"})
	if code := codeOfErr(t, err); code != response.CodeConflict {
		t.Errorf("大小写不同的同名账号应冲突，实际 code=%d err=%v", code, err)
	}
}

func TestCreate的角色与启用默认值(t *testing.T) {
	svc, d, _ := newUserSvc(t)

	u, err := svc.Create(CreateUserReq{Username: "bob", Password: "goodpass123"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if u.Role != model.RoleMember {
		t.Errorf("角色默认应为 member，实际 %q", u.Role)
	}
	if !u.Enabled {
		t.Error("账号默认应启用")
	}

	var stored model.User
	if err := d.DB.First(&stored, u.ID).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	// bcrypt 哈希必须真的落库，且不能等于明文
	if stored.Password == "goodpass123" {
		t.Fatal("密码被明文存储了")
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("goodpass123")) != nil {
		t.Error("落库的哈希无法校验原密码")
	}
}

func TestCreate拒绝非法用户名(t *testing.T) {
	svc, _, _ := newUserSvc(t)
	bad := []string{"", "a", ".abc", "-abc", "a b", "ab@cd@ef/", strings.Repeat("x", 65)}
	for _, name := range bad {
		if _, err := svc.Create(CreateUserReq{Username: name, Password: "goodpass123"}); err == nil {
			t.Errorf("用户名 %q 应被拒绝", name)
		}
	}
}

// 密码策略是唯一会直接暴露给用户的规则，逐条锁住。
func TestCreate密码策略(t *testing.T) {
	svc, _, _ := newUserSvc(t)

	cases := []struct {
		name     string
		username string
		password string
		wantErr  bool
	}{
		{"正常", "u1", "goodpass123", false},
		{"太短", "u2", "short1", true},
		{"含空格", "u3", "good pass123", true},
		{"含制表符", "u4", "good\tpass123", true},
		{"与用户名相同（忽略大小写）", "samepass", "SAMEPASS", true},
		{"弱密码 admin123", "u5", "admin123", true},
		{"弱密码 password", "u6", "password", true},
		// bcrypt 静默截断 72 字节以上，必须拦掉而不是让它悄悄截断
		{"超 72 字节", "u7", strings.Repeat("a", 73), true},
		{"刚好 72 字节", "u8", strings.Repeat("a", 72), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Create(CreateUserReq{Username: c.username, Password: c.password})
			if c.wantErr && err == nil {
				t.Errorf("密码 %q 应被拒绝", c.password)
			}
			if !c.wantErr && err != nil {
				t.Errorf("密码 %q 不该被拒绝: %v", c.password, err)
			}
		})
	}
}

func TestCreate拒绝非法角色(t *testing.T) {
	svc, _, _ := newUserSvc(t)
	if _, err := svc.Create(CreateUserReq{Username: "u", Password: "goodpass123", Role: "superuser"}); err == nil {
		t.Error("未知角色应被拒绝")
	} else if code := codeOfErr(t, err); code != response.CodeBadParam {
		t.Errorf("code = %d, want 40000", code)
	}
}

// ---------------------------------------------------------------------------
// 守卫 1：不能把自己关在门外
// ---------------------------------------------------------------------------

func TestUpdate不能把自己降级(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	me := seedUser(t, d, "me", model.RoleAdmin, true)
	other := seedUser(t, d, "other", model.RoleAdmin, true)
	_ = other

	_, err := svc.Update(me.ID, me.ID, UpdateUserReq{Nickname: "me", Role: model.RoleMember})
	if code := codeOfErr(t, err); code != response.CodeForbidden {
		t.Fatalf("把自己降级应被拒绝（40300），实际 code=%d err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "让另一位管理员") {
		t.Errorf("提示应给出可执行的下一步，实际: %v", err)
	}

	// 确认库里没被改
	var stored model.User
	if err := d.DB.First(&stored, me.ID).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if stored.Role != model.RoleAdmin {
		t.Errorf("角色被改了: %q", stored.Role)
	}
}

func TestUpdate不能禁用自己(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	me := seedUser(t, d, "me", model.RoleAdmin, true)
	dis := false

	_, err := svc.Update(me.ID, me.ID, UpdateUserReq{Nickname: "me", Role: model.RoleAdmin, Enabled: &dis})
	if code := codeOfErr(t, err); code != response.CodeForbidden {
		t.Fatalf("禁用自己应被拒绝，实际 code=%d err=%v", code, err)
	}
}

func TestDelete不能删除自己(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	me := seedUser(t, d, "me", model.RoleAdmin, true)
	seedUser(t, d, "other", model.RoleAdmin, true)

	if code := codeOfErr(t, svc.Delete(me.ID, me.ID)); code != response.CodeForbidden {
		t.Fatalf("删除自己应被拒绝，实际 code=%d", code)
	}
}

// ---------------------------------------------------------------------------
// 守卫 2：系统里必须永远剩一个可用管理员
// ---------------------------------------------------------------------------

func TestUpdate不能干掉最后一个可用管理员(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	// 场景：A（管理员，操作者）与 B（管理员，目标）。A 把 B 降级后系统仍有 A，
	// 所以这条不该被拦。真正该拦的是"目标是最后一个"。
	// 这里构造"只有一个管理员"，由同时是操作者触发守卫 1，
	// 所以换个不触发守卫 1 的角度：目标是唯一管理员，操作者是另一个账号。
	only := seedUser(t, d, "only", model.RoleAdmin, true)
	operator := seedUser(t, d, "op", model.RoleMember, true) // 现实里不可能，但恰好隔离出守卫 2

	_, err := svc.Update(only.ID, operator.ID, UpdateUserReq{Nickname: "only", Role: model.RoleMember})
	if code := codeOfErr(t, err); code != response.CodeForbidden {
		t.Fatalf("降级最后一个可用管理员应被拒绝，实际 code=%d err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "无人能管理") {
		t.Errorf("提示应说明后果，实际: %v", err)
	}

	dis := false
	_, err = svc.Update(only.ID, operator.ID, UpdateUserReq{Nickname: "only", Role: model.RoleAdmin, Enabled: &dis})
	if code := codeOfErr(t, err); code != response.CodeForbidden {
		t.Errorf("禁用最后一个可用管理员应被拒绝，实际 code=%d err=%v", code, err)
	}

	if code := codeOfErr(t, svc.Delete(only.ID, operator.ID)); code != response.CodeForbidden {
		t.Errorf("删除最后一个可用管理员应被拒绝，实际 code=%d", code)
	}
}

// ⭐ 这条锁住的是最容易写错的地方：只数 role=admin 会把系统锁死。
//
// 场景：一个被禁用的管理员 + 一个启用的管理员。把启用的那个降级/禁用后，
// 系统里"看起来还有管理员"，但那个登不进去 —— 等于无人能管理。
func Test被禁用的管理员不算数(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	disabled := seedUser(t, d, "ghost", model.RoleAdmin, false) // 管理员但已禁用
	active := seedUser(t, d, "real", model.RoleAdmin, true)

	_, err := svc.Update(active.ID, disabled.ID, UpdateUserReq{Nickname: "real", Role: model.RoleMember})
	if code := codeOfErr(t, err); code != response.CodeForbidden {
		t.Fatalf("已禁用的管理员不能算作可用的管理员，应拒绝降级 real，实际 code=%d err=%v", code, err)
	}
}

func Test有另一个管理员时允许降级(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	a := seedUser(t, d, "a", model.RoleAdmin, true)
	b := seedUser(t, d, "b", model.RoleAdmin, true)

	// 操作者是 a，目标 b：系统还剩 a，允许
	if _, err := svc.Update(b.ID, a.ID, UpdateUserReq{Nickname: "b", Role: model.RoleMember}); err != nil {
		t.Fatalf("有另一个可用管理员时不该拒绝: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 守卫 3：权限变更必须吊销会话
// ---------------------------------------------------------------------------

func Test降级会吊销对方全部会话(t *testing.T) {
	svc, d, store := newUserSvc(t)
	op := seedUser(t, d, "op", model.RoleAdmin, true)
	target := seedUser(t, d, "target", model.RoleAdmin, true)

	t1 := login(store, target)
	t2 := login(store, target)
	byop := login(store, op)

	if _, err := svc.Update(target.ID, op.ID, UpdateUserReq{Nickname: "target", Role: model.RoleMember}); err != nil {
		t.Fatalf("降级失败: %v", err)
	}

	if _, ok := store.Get(t1); ok {
		t.Error("被降级用户的会话 t1 必须立即失效（否则他仍是管理员）")
	}
	if _, ok := store.Get(t2); ok {
		t.Error("被降级用户的会话 t2 必须立即失效")
	}
	if _, ok := store.Get(byop); !ok {
		t.Error("操作者自己的会话被误杀")
	}
}

func Test禁用会吊销对方全部会话(t *testing.T) {
	svc, d, store := newUserSvc(t)
	op := seedUser(t, d, "op", model.RoleAdmin, true)
	target := seedUser(t, d, "target", model.RoleMember, true)
	tok := login(store, target)
	dis := false

	if _, err := svc.Update(target.ID, op.ID, UpdateUserReq{Nickname: "target", Role: model.RoleMember, Enabled: &dis}); err != nil {
		t.Fatalf("禁用失败: %v", err)
	}
	if _, ok := store.Get(tok); ok {
		t.Error("被禁用用户的会话必须立即失效（否则禁用要等 TTL 才生效）")
	}
}

func Test删除会吊销对方会话(t *testing.T) {
	svc, d, store := newUserSvc(t)
	op := seedUser(t, d, "op", model.RoleAdmin, true)
	target := seedUser(t, d, "target", model.RoleMember, true)
	tok := login(store, target)

	if err := svc.Delete(target.ID, op.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok := store.Get(tok); ok {
		t.Error("被删除用户的会话必须立即失效")
	}
	if _, err := svc.Get(target.ID); err == nil {
		t.Error("账号应已删除")
	}
}

// 只改昵称不该把人踢下线 —— 否则管理员改个昵称全组被登出。
func Test只改昵称不吊销会话但刷新主体(t *testing.T) {
	svc, d, store := newUserSvc(t)
	op := seedUser(t, d, "op", model.RoleAdmin, true)
	target := seedUser(t, d, "target", model.RoleMember, true)
	tok := login(store, target)

	if _, err := svc.Update(target.ID, op.ID, UpdateUserReq{Nickname: "新昵称", Role: model.RoleMember}); err != nil {
		t.Fatalf("改昵称失败: %v", err)
	}

	p, ok := store.Get(tok)
	if !ok {
		t.Fatal("只改昵称不该吊销会话")
	}
	if p.Nickname != "新昵称" {
		t.Errorf("会话里的昵称未刷新: %q", p.Nickname)
	}
}

// 会话表没接线时必须留下一条 error 日志，而不是静默成功。
// 这条断言防的是"禁用用户看起来生效了、其实对方还能操作"。
func Test未接线会话表时不静默通过(t *testing.T) {
	d := testDeps(t) // 刻意不设 Sessions
	svc := &UserService{Deps: d}
	op := seedUser(t, d, "op", model.RoleAdmin, true)
	target := seedUser(t, d, "target", model.RoleMember, true)

	if _, err := svc.Update(target.ID, op.ID, UpdateUserReq{Nickname: "t", Role: model.RoleMember}); err != nil {
		t.Fatalf("不该因为会话表缺失而报错: %v", err)
	}
	if got := svc.sessionsOf(target.ID); got != 0 {
		t.Errorf("sessionsOf = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// 密码
// ---------------------------------------------------------------------------

func TestChangeOwnPassword验旧密码(t *testing.T) {
	svc, d, store := newUserSvc(t)
	u := seedUser(t, d, "u", model.RoleMember, true)
	tok := login(store, u)

	if code := codeOfErr(t, svc.ChangeOwnPassword(u.ID, tok, "wrongold", "newpass123")); code != response.CodeBadParam {
		t.Fatalf("原密码错误应被拒，实际 code=%d", code)
	}
	if err := svc.ChangeOwnPassword(u.ID, tok, "initpass123", "newpass123"); err != nil {
		t.Fatalf("改密码失败: %v", err)
	}

	var stored model.User
	if err := d.DB.First(&stored, u.ID).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("newpass123")) != nil {
		t.Error("新密码未生效")
	}
}

func TestChangeOwnPassword踢其它设备但保留当前(t *testing.T) {
	svc, d, store := newUserSvc(t)
	u := seedUser(t, d, "u", model.RoleMember, true)
	current := login(store, u)
	old1 := login(store, u)

	if err := svc.ChangeOwnPassword(u.ID, current, "initpass123", "newpass123"); err != nil {
		t.Fatalf("改密码失败: %v", err)
	}
	if _, ok := store.Get(current); !ok {
		t.Error("当前会话必须保留（否则改完密码立刻被登出，像把账号弄坏了）")
	}
	if _, ok := store.Get(old1); ok {
		t.Error("其它设备的会话应被踢掉")
	}
}

func TestChangeOwnPassword拒绝与原密码相同(t *testing.T) {
	svc, d, store := newUserSvc(t)
	u := seedUser(t, d, "u", model.RoleMember, true)
	tok := login(store, u)
	if code := codeOfErr(t, svc.ChangeOwnPassword(u.ID, tok, "initpass123", "initpass123")); code != response.CodeBadParam {
		t.Errorf("新密码与原密码相同应被拒，实际 code=%d", code)
	}
}

// 重置密码是"忘记旧密码"的救济路径，因此必须禁止作用于自己 ——
// 否则它就成了绕过旧密码校验的后门。
func TestResetPassword不能重置自己(t *testing.T) {
	svc, d, _ := newUserSvc(t)
	op := seedUser(t, d, "op", model.RoleAdmin, true)

	err := svc.ResetPassword(op.ID, op.ID, "newpass123")
	if code := codeOfErr(t, err); code != response.CodeForbidden {
		t.Fatalf("重置自己应被拒绝，实际 code=%d err=%v", code, err)
	}
}

func TestResetPassword吊销目标全部会话(t *testing.T) {
	svc, d, store := newUserSvc(t)
	op := seedUser(t, d, "op", model.RoleAdmin, true)
	target := seedUser(t, d, "target", model.RoleMember, true)
	t1 := login(store, target)
	t2 := login(store, target)
	keep := login(store, op)

	if err := svc.ResetPassword(target.ID, op.ID, "newpass123"); err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	for _, tok := range []string{t1, t2} {
		if _, ok := store.Get(tok); ok {
			t.Error("重置密码必须把目标用户全部会话踢掉")
		}
	}
	if _, ok := store.Get(keep); !ok {
		t.Error("操作者自己的会话被误杀")
	}

	var stored model.User
	if err := d.DB.First(&stored, target.ID).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("newpass123")) != nil {
		t.Error("新密码未生效")
	}
}

// ---------------------------------------------------------------------------
// 列表
// ---------------------------------------------------------------------------

func TestList过滤与在线会话数(t *testing.T) {
	svc, d, store := newUserSvc(t)
	a := seedUser(t, d, "alice", model.RoleAdmin, true)
	seedUser(t, d, "bob", model.RoleMember, true)
	seedUser(t, d, "carol", model.RoleMember, false)

	login(store, a)

	list, total, err := svc.List(UserListQuery{}, Page{})
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	byName := map[string]UserView{}
	for _, v := range list {
		byName[v.Username] = v
	}
	if got := byName["alice"].OnlineSessions; got != 1 {
		t.Errorf("alice 在线会话 = %d, want 1（这是「吊销真的发生了」的可见证据）", got)
	}
	if got := byName["bob"].OnlineSessions; got != 0 {
		t.Errorf("bob 在线会话 = %d, want 0", got)
	}

	admins, n, err := svc.List(UserListQuery{Role: model.RoleAdmin}, Page{})
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if n != 1 || len(admins) != 1 || admins[0].Username != "alice" {
		t.Errorf("角色过滤结果不对: n=%d list=%+v", n, admins)
	}

	kw, n, err := svc.List(UserListQuery{Keyword: "BO"}, Page{})
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if n != 1 || len(kw) != 1 || kw[0].Username != "bob" {
		t.Errorf("关键字过滤应大小写不敏感: n=%d list=%+v", n, kw)
	}
}
