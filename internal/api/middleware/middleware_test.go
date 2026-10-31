package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/auth"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

func init() { gin.SetMode(gin.TestMode) }

// newRouter 构造一个挂了中间件的测试路由，okHandler 模拟受保护的业务端点。
func newRouter(mw ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.GET("/protected", append(mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, response.Body{Code: response.CodeOK})
	})...)
	return r
}

func do(r *gin.Engine, cookie string, header map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(w, req)
	return w
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON：%s", w.Body)
	}
	return body.Code
}

// ---------------------------------------------------------------------------
// RequireAuth
// ---------------------------------------------------------------------------

func TestRequireAuth(t *testing.T) {
	store := auth.NewStore(time.Minute)
	r := newRouter(RequireAuth(store))

	// 未带 cookie。
	w := do(r, "", nil)
	if w.Code != http.StatusUnauthorized || codeOf(t, w) != response.CodeUnauthorized {
		t.Fatalf("无 cookie 应 401/40100，得到 %d", w.Code)
	}

	// cookie 对不上任何会话（伪造或已过期）。
	w = do(r, "forged-token", nil)
	if w.Code != http.StatusUnauthorized || codeOf(t, w) != response.CodeUnauthorized {
		t.Fatalf("伪造 cookie 应 401，得到 %d", w.Code)
	}

	// 合法会话：放行且 Principal 已挂到 context。
	want := auth.Principal{UserID: 7, Username: "u7", Role: model.RoleMember}
	token := store.Create(want)
	w = do(r, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("合法会话应放行，得到 %d：%s", w.Code, w.Body)
	}
}

func TestCurrentPrincipal(t *testing.T) {
	var got auth.Principal
	var ok bool
	store := auth.NewStore(time.Minute)
	r := gin.New()
	r.GET("/p", RequireAuth(store), func(c *gin.Context) {
		got, ok = CurrentPrincipal(c)
		c.Status(http.StatusOK)
	})

	token := store.Create(auth.Principal{UserID: 9, Username: "u9", Role: model.RoleAdmin})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !ok || got.UserID != 9 {
		t.Fatalf("CurrentPrincipal = (%v, %v)", got, ok)
	}
}

// ---------------------------------------------------------------------------
// RequireAdmin
// ---------------------------------------------------------------------------

// withPrincipal 用前置 handler 直接注入 Principal，绕开登录态，
// 只测 RequireAdmin 自身的判定。
func withPrincipal(p auth.Principal) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(PrincipalKey, p)
		c.Next()
	}
}

func TestRequireAdmin(t *testing.T) {
	member := auth.Principal{UserID: 2, Role: model.RoleMember}
	admin := auth.Principal{UserID: 1, Role: model.RoleAdmin}

	w := do(newRouter(withPrincipal(member), RequireAdmin()), "", nil)
	if w.Code != http.StatusForbidden || codeOf(t, w) != response.CodeForbidden {
		t.Fatalf("普通成员应 403/40300，得到 %d", w.Code)
	}

	w = do(newRouter(withPrincipal(admin), RequireAdmin()), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("管理员应放行，得到 %d", w.Code)
	}

	// 没有登录态直接挂 RequireAdmin（漏挂 RequireAuth 的错误接线）也应 403。
	w = do(newRouter(RequireAdmin()), "", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无登录态应 403，得到 %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// RequireToken（CI 令牌）
// ---------------------------------------------------------------------------

func TestRequireToken(t *testing.T) {
	verify := func(raw string) (*model.APIToken, error) {
		if raw == "good-token" {
			return &model.APIToken{Base: model.Base{ID: 5}}, nil
		}
		return nil, response.New(response.CodeUnauthorized, "令牌无效")
	}
	r := newRouter(RequireToken(verify))

	// 无令牌。
	w := do(r, "", nil)
	if w.Code != http.StatusUnauthorized || codeOf(t, w) != response.CodeUnauthorized {
		t.Fatalf("无令牌应 401，得到 %d", w.Code)
	}

	// Bearer 标准携带。
	w = do(r, "", map[string]string{"Authorization": "Bearer good-token"})
	if w.Code != http.StatusOK {
		t.Fatalf("Bearer 应放行，得到 %d：%s", w.Code, w.Body)
	}

	// X-HRP-Token 兜底携带（部分 CI 的 HTTP 步骤会占用 Authorization）。
	w = do(r, "", map[string]string{"X-HRP-Token": "good-token"})
	if w.Code != http.StatusOK {
		t.Fatalf("X-HRP-Token 应放行，得到 %d", w.Code)
	}

	// 错误令牌：401，且回显文案来自 verify 的业务错误。
	w = do(r, "", map[string]string{"Authorization": "Bearer bad"})
	if w.Code != http.StatusUnauthorized || w.Body.String() == "" {
		t.Fatalf("坏令牌应 401 且有响应体，得到 %d", w.Code)
	}

	// CurrentToken 应能取回验证过的令牌。
	var tk *model.APIToken
	var ok bool
	r2 := gin.New()
	r2.GET("/protected", RequireToken(verify), func(c *gin.Context) {
		tk, ok = CurrentToken(c)
		c.Status(http.StatusOK)
	})
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "bearer good-token") // 小写 bearer 也应识别
	r2.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !ok || tk == nil || tk.ID != 5 {
		t.Fatalf("小写 bearer / CurrentToken 判定异常：%d %v %v", w.Code, ok, tk)
	}
}

func TestBearerToken解析(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"},   // 大小写不敏感
		{"BEARER   abc", "abc"}, // 多空格
		{"Bearer", ""},          // 缺令牌
		{"Bearer a b", ""},      // 三段不是 Bearer 形态
		{"Basic xyz", ""},       // 非 Bearer scheme
	}
	for _, tt := range tests {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			c.Request.Header.Set("Authorization", tt.header)
		}
		if got := bearerToken(c); got != tt.want {
			t.Errorf("Authorization %q → %q，期望 %q", tt.header, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

func TestCORS(t *testing.T) {
	r := gin.New()
	r.Use(CORS([]string{"http://127.0.0.1:5173"}))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	get := func(origin string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r.ServeHTTP(w, req)
		return w
	}

	// 白名单来源：带全套 CORS 头。
	w := get("http://127.0.0.1:5173")
	if w.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5173" {
		t.Fatalf("白名单来源应回写 Origin：%v", w.Header())
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("跨域带 cookie 必须允许 credentials")
	}

	// 非白名单：不加任何 CORS 头（默认不放开）。
	w = get("http://evil.example.com")
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("非白名单来源不应有 CORS 头")
	}

	// 预检：204 且直接短路。
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS 预检应 204，得到 %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Recovery
// ---------------------------------------------------------------------------

func TestRecovery(t *testing.T) {
	r := gin.New()
	r.Use(Recovery())
	r.GET("/boom", func(c *gin.Context) {
		panic("用例数据把我干崩了")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	r.ServeHTTP(w, req)

	// 一个用例触发 panic 不能拖垮整个服务：统一 500 + 业务码 50000。
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应被兜成 500，得到 %d", w.Code)
	}
	if codeOf(t, w) != response.CodeInternal {
		t.Fatalf("panic 响应应带 50000，得到 %s", w.Body)
	}
}
