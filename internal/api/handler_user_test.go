package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/config"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/repo"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
)

// 与 service 包的测试同源：内存库 + 真实 repo.Open/Migrate。
// 用替身测权限边界没有意义 —— 这里要验的恰恰是"真实中间件 + 真实路由表"。
var apiTestDBCount atomic.Uint64

type testEnv struct {
	r     *gin.Engine
	store *auth.Store
	db    *gorm.DB
	users *service.UserService
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	cfg := &config.Config{}
	cfg.Server.Mode = "release" // 避免 debug 模式再叠一层 CORS 中间件

	dsn := fmt.Sprintf("file:hrp-api-test-%d?mode=memory&cache=shared", apiTestDBCount.Add(1))
	db, err := repo.Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}, false)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := repo.Migrate(db); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	store := auth.NewStore(time.Hour)
	svcs := service.New(service.Deps{DB: db, Cfg: cfg, Sessions: store})
	return &testEnv{
		r:     NewRouter(&Deps{Cfg: cfg, DB: db, Sessions: store, Services: svcs}),
		store: store,
		db:    db,
		users: svcs.User,
	}
}

const seedPassword = "initpass123"

// seedUser 直接写库建账号（绕过 API，避免"测权限边界却先依赖 API 可用"）。
func (e *testEnv) seedUser(t *testing.T, username, role string, enabled bool) *model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(seedPassword), bcrypt.DefaultCost)
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
	if err := e.db.Create(u).Error; err != nil {
		t.Fatalf("建账号失败: %v", err)
	}
	return u
}

type respBody struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// call 发一次请求。cookies 为空表示未登录。
func (e *testEnv) call(t *testing.T, method, path string, body any, cookies []*http.Cookie) (*httptest.ResponseRecorder, respBody) {
	t.Helper()

	payload := []byte("")
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		payload = b
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)

	var parsed respBody
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	}
	return w, parsed
}

// login 走真实的登录接口拿到会话 Cookie。
func (e *testEnv) login(t *testing.T, username, password string) []*http.Cookie {
	t.Helper()
	w, body := e.call(t, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": username, "password": password}, nil)
	if body.Code != 0 {
		t.Fatalf("登录 %s 失败: code=%d msg=%s (http %d)", username, body.Code, body.Message, w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("登录没有下发 Cookie")
	}
	return cookies
}

func unmarshalData(t *testing.T, raw json.RawMessage, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("解析 data 失败: %v (%s)", err, string(raw))
	}
}

// ---------------------------------------------------------------------------
// 权限边界：这是本次改动里"接线了没有"的直接证据
// ---------------------------------------------------------------------------

func Test未登录访问用户管理返回401(t *testing.T) {
	e := newTestEnv(t)
	w, body := e.call(t, http.MethodGet, "/api/v1/users", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("HTTP = %d, want 401", w.Code)
	}
	if body.Code != 40100 {
		t.Errorf("业务码 = %d, want 40100", body.Code)
	}
}

func Test成员访问用户管理返回403(t *testing.T) {
	e := newTestEnv(t)
	e.seedUser(t, "member1", model.RoleMember, true)
	cookies := e.login(t, "member1", seedPassword)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/users", nil},
		{http.MethodPost, "/api/v1/users", map[string]string{"username": "x1", "password": "goodpass123"}},
		{http.MethodPut, "/api/v1/users/1", map[string]string{"nickname": "x", "role": "member"}},
		{http.MethodDelete, "/api/v1/users/9", nil},
		{http.MethodPost, "/api/v1/users/9/password", map[string]string{"password": "goodpass123"}},
	} {
		w, body := e.call(t, tc.method, tc.path, tc.body, cookies)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s HTTP = %d, want 403（RequireAdmin 没接上？）", tc.method, tc.path, w.Code)
		}
		if body.Code != 40300 {
			t.Errorf("%s %s 业务码 = %d, want 40300", tc.method, tc.path, body.Code)
		}
	}
}

func Test管理员可以管理账号(t *testing.T) {
	e := newTestEnv(t)
	admin := e.seedUser(t, "admin1", model.RoleAdmin, true)
	cookies := e.login(t, "admin1", seedPassword)

	// 列表
	w, body := e.call(t, http.MethodGet, "/api/v1/users", nil, cookies)
	if w.Code != http.StatusOK || body.Code != 0 {
		t.Fatalf("列表失败: http=%d code=%d msg=%s", w.Code, body.Code, body.Message)
	}
	var page struct {
		List  []service.UserView `json:"list"`
		Total int64              `json:"total"`
	}
	unmarshalData(t, body.Data, &page)
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("应有 1 个账号，实际 total=%d len=%d", page.Total, len(page.List))
	}
	if page.List[0].Username != "admin1" {
		t.Errorf("账号不对: %+v", page.List[0])
	}
	// 密码绝不能出现在响应里
	if bytes.Contains(body.Data, []byte("password")) || bytes.Contains(body.Data, []byte("$2a$")) {
		t.Errorf("响应里出现了密码字段或哈希: %s", string(body.Data))
	}

	// 新建
	w, body = e.call(t, http.MethodPost, "/api/v1/users",
		map[string]string{"username": "newbie", "password": "goodpass123", "role": "member"}, cookies)
	if body.Code != 0 {
		t.Fatalf("新建账号失败: code=%d msg=%s", body.Code, body.Message)
	}
	var created model.User
	unmarshalData(t, body.Data, &created)

	// 改名与启用状态
	w, body = e.call(t, http.MethodPut, fmt.Sprintf("/api/v1/users/%d", created.ID),
		map[string]any{"nickname": "新人", "role": "member", "enabled": true}, cookies)
	if body.Code != 0 {
		t.Fatalf("更新账号失败: code=%d msg=%s", body.Code, body.Message)
	}

	// 重置密码
	if _, body = e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/password", created.ID),
		map[string]string{"password": "resetpass123"}, cookies); body.Code != 0 {
		t.Fatalf("重置密码失败: code=%d msg=%s", body.Code, body.Message)
	}
	// 新密码能登录
	e.login(t, "newbie", "resetpass123")

	// 删除
	w, body = e.call(t, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", created.ID), nil, cookies)
	if w.Code != http.StatusOK || body.Code != 0 {
		t.Fatalf("删除失败: http=%d code=%d msg=%s", w.Code, body.Code, body.Message)
	}

	_ = admin
}

// 删除项目破坏性最大（连带环境与用例），收紧为管理员专属。
func Test成员不能删项目(t *testing.T) {
	e := newTestEnv(t)
	admin := e.seedUser(t, "admin2", model.RoleAdmin, true)
	e.seedUser(t, "member2", model.RoleMember, true)
	if err := e.db.Create(&model.Project{Code: "p1", Name: "P1", HrpVersion: "v4.3.6"}).Error; err != nil {
		t.Fatalf("建项目失败: %v", err)
	}

	memberCookies := e.login(t, "member2", seedPassword)
	w, body := e.call(t, http.MethodDelete, "/api/v1/projects/1", nil, memberCookies)
	if w.Code != http.StatusForbidden {
		t.Errorf("成员删项目 HTTP = %d, want 403", w.Code)
	}
	if body.Code != 40300 {
		t.Errorf("业务码 = %d, want 40300", body.Code)
	}
	// 确认项目还在
	var n int64
	e.db.Model(&model.Project{}).Count(&n)
	if n != 1 {
		t.Fatalf("项目被删掉了，成员不该有这个权限")
	}

	// 但成员仍可读项目（不能因为收紧而把正常功能一起关掉）
	if _, body := e.call(t, http.MethodGet, "/api/v1/projects", nil, memberCookies); body.Code != 0 {
		t.Errorf("成员应能读取项目列表: code=%d", body.Code)
	}

	// 管理员可以删
	adminCookies := e.login(t, "admin2", seedPassword)
	if _, body := e.call(t, http.MethodDelete, "/api/v1/projects/1", nil, adminCookies); body.Code != 0 {
		t.Errorf("管理员删项目失败: code=%d msg=%s", body.Code, body.Message)
	}
	_ = admin
}

// ---------------------------------------------------------------------------
// 会话吊销的端到端证据
// ---------------------------------------------------------------------------

// ⭐ 这条是本次改动最核心的验收：禁用之后，对方**正在使用的那个会话**
// 必须立刻失效，而不是等 TTL（默认 24h）到点。
func Test禁用用户后其会话立即失效(t *testing.T) {
	e := newTestEnv(t)
	e.seedUser(t, "admin3", model.RoleAdmin, true)
	target := e.seedUser(t, "member3", model.RoleMember, true)

	adminCookies := e.login(t, "admin3", seedPassword)
	memberCookies := e.login(t, "member3", seedPassword)

	// 禁用前：能访问
	if _, body := e.call(t, http.MethodGet, "/api/v1/projects", nil, memberCookies); body.Code != 0 {
		t.Fatalf("禁用前应能访问: code=%d", body.Code)
	}

	disabled := false
	if _, body := e.call(t, http.MethodPut, fmt.Sprintf("/api/v1/users/%d", target.ID),
		map[string]any{"nickname": "member3", "role": "member", "enabled": disabled}, adminCookies); body.Code != 0 {
		t.Fatalf("禁用失败: code=%d msg=%s", body.Code, body.Message)
	}

	w, body := e.call(t, http.MethodGet, "/api/v1/projects", nil, memberCookies)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("禁用后 HTTP = %d, want 401（会话没被吊销，禁用等于没生效）", w.Code)
	}
	if body.Code != 40100 {
		t.Errorf("业务码 = %d, want 40100", body.Code)
	}

	// 而且不能再登录
	if _, body := e.call(t, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "member3", "password": seedPassword}, nil); body.Code == 0 {
		t.Error("被禁用的账号不该能登录")
	}
}

// 降级一个管理员的会话同样要立刻失效 —— 否则他仍是管理员。
func Test降级管理员后其权限立即收回(t *testing.T) {
	e := newTestEnv(t)
	op := e.seedUser(t, "admin4", model.RoleAdmin, true)
	victim := e.seedUser(t, "admin5", model.RoleAdmin, true)

	opCookies := e.login(t, "admin4", seedPassword)
	victimCookies := e.login(t, "admin5", seedPassword)

	// 降级前：被降级者能访问用户管理
	if _, body := e.call(t, http.MethodGet, "/api/v1/users", nil, victimCookies); body.Code != 0 {
		t.Fatalf("降级前应能访问用户管理: code=%d", body.Code)
	}

	if _, body := e.call(t, http.MethodPut, fmt.Sprintf("/api/v1/users/%d", victim.ID),
		map[string]any{"nickname": "admin5", "role": "member", "enabled": true}, opCookies); body.Code != 0 {
		t.Fatalf("降级失败: code=%d msg=%s", body.Code, body.Message)
	}

	// 降级后：会话直接失效（而不是"仍然是管理员"）
	w, body := e.call(t, http.MethodGet, "/api/v1/users", nil, victimCookies)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("降级后 HTTP = %d, want 401（会话里缓存的角色还在，必须吊销）", w.Code)
	}
	if body.Code != 40100 {
		t.Errorf("业务码 = %d, want 40100", body.Code)
	}
	_ = op
}

// 本人改密码：当前会话要保住（否则改完立刻被登出），其它会话要踢掉。
func Test改密码后当前会话保留他人被踢(t *testing.T) {
	e := newTestEnv(t)
	u := e.seedUser(t, "member4", model.RoleMember, true)

	deviceA := e.login(t, "member4", seedPassword)
	deviceB := e.login(t, "member4", seedPassword)

	if _, body := e.call(t, http.MethodPost, "/api/v1/auth/password",
		map[string]string{"old_password": seedPassword, "new_password": "brandnew123"}, deviceA); body.Code != 0 {
		t.Fatalf("改密码失败: code=%d msg=%s", body.Code, body.Message)
	}

	if w, _ := e.call(t, http.MethodGet, "/api/v1/auth/me", nil, deviceA); w.Code != http.StatusOK {
		t.Errorf("当前会话应保留，HTTP = %d", w.Code)
	}
	if w, _ := e.call(t, http.MethodGet, "/api/v1/auth/me", nil, deviceB); w.Code != http.StatusUnauthorized {
		t.Errorf("其它设备的会话应被踢，HTTP = %d", w.Code)
	}

	if _, body := e.call(t, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "member4", "password": seedPassword}, nil); body.Code == 0 {
		t.Error("旧密码不该还能登录")
	}
	e.login(t, "member4", "brandnew123")
	_ = u
}

// 守卫在 API 层同样生效（service 已有单测，这里验它确实被 HTTP 层走到了）。
func TestAPI层的自我保护与最后管理员守卫(t *testing.T) {
	e := newTestEnv(t)
	admin := e.seedUser(t, "admin6", model.RoleAdmin, true)
	cookies := e.login(t, "admin6", seedPassword)

	// 不能删自己
	w, body := e.call(t, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", admin.ID), nil, cookies)
	if body.Code != 40300 {
		t.Errorf("删自己应 40300，实际 code=%d (http %d)", body.Code, w.Code)
	}
	// 不能把自己降级
	w, body = e.call(t, http.MethodPut, fmt.Sprintf("/api/v1/users/%d", admin.ID),
		map[string]any{"nickname": "admin6", "role": "member"}, cookies)
	if body.Code != 40300 {
		t.Errorf("把自己降级应 40300，实际 code=%d (http %d)", body.Code, w.Code)
	}
	// 不能重置自己的密码（那是绕过旧密码校验的后门）
	w, body = e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/password", admin.ID),
		map[string]string{"password": "brandnew123"}, cookies)
	if body.Code != 40300 {
		t.Errorf("重置自己应 40300（应用 /auth/password），实际 code=%d (http %d)", body.Code, w.Code)
	}
	// 会话仍在：以上拒绝不该把管理员踢下线
	if w, _ := e.call(t, http.MethodGet, "/api/v1/auth/me", nil, cookies); w.Code != http.StatusOK {
		t.Errorf("被拒绝的操作不该吊销自己的会话，HTTP = %d", w.Code)
	}
}

// 账号列表里的在线会话数，是"吊销真的发生了"的可见证据。
func Test在线会话数随吊销归零(t *testing.T) {
	e := newTestEnv(t)
	e.seedUser(t, "admin7", model.RoleAdmin, true)
	target := e.seedUser(t, "member5", model.RoleMember, true)
	adminCookies := e.login(t, "admin7", seedPassword)
	e.login(t, "member5", seedPassword)
	e.login(t, "member5", seedPassword)

	find := func() service.UserView {
		t.Helper()
		_, body := e.call(t, http.MethodGet, "/api/v1/users", nil, adminCookies)
		var page struct {
			List []service.UserView `json:"list"`
		}
		unmarshalData(t, body.Data, &page)
		for _, v := range page.List {
			if v.ID == target.ID {
				return v
			}
		}
		t.Fatalf("列表里没找到目标账号")
		return service.UserView{}
	}

	if got := find().OnlineSessions; got != 2 {
		t.Fatalf("禁用前在线会话 = %d, want 2", got)
	}

	disabled := false
	if _, body := e.call(t, http.MethodPut, fmt.Sprintf("/api/v1/users/%d", target.ID),
		map[string]any{"nickname": "member5", "role": "member", "enabled": disabled}, adminCookies); body.Code != 0 {
		t.Fatalf("禁用失败: code=%d", body.Code)
	}
	if got := find().OnlineSessions; got != 0 {
		t.Errorf("禁用后在线会话 = %d, want 0（这就是「吊销真的发生了」的证据）", got)
	}
}
