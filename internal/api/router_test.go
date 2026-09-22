package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/webui"
)

func init() { gin.SetMode(gin.TestMode) }

func builtUI(t *testing.T) *webui.UI {
	t.Helper()
	ui, err := webui.New(fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(`<!DOCTYPE html><div id="app"></div>`)},
		"assets/app.js": &fstest.MapFile{Data: []byte(`console.log(1)`)},
	})
	if err != nil {
		t.Fatalf("构造内嵌前端失败: %v", err)
	}
	return ui
}

// serveNoRoute 只挂 NoRoute，用最小引擎验证兜底分流。
func serveNoRoute(t *testing.T, ui *webui.UI, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.NoRoute(noRouteHandler(&Deps{WebUI: ui}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

// ⭐ 这是引入静态托管后最容易被破坏、也最难排查的一条约束。
//
// 前端把接口路径写错时（比如 /api/v1/casses 拼错），如果这类请求落进了
// SPA fallback，前端拿到的是 200 + 一坨 HTML，axios 解析后抛
// 「Unexpected token '<'」——错误信息与真实原因（路径拼错）完全对不上，
// 排查成本极高。所以 /api 前缀必须永远走 JSON。
func TestAPI路径不落进SPAFallback(t *testing.T) {
	ui := builtUI(t)

	for _, target := range []string{
		"/api/v1/casses",        // 拼错的接口
		"/api/v1/runs/9999",     // 不存在的资源（路由内会命中，这里只验证兜底）
		"/api/v1/nested/deep/x", // 任意深度
		"/api",                  // 裸前缀
	} {
		w := serveNoRoute(t, ui, http.MethodGet, target)
		if w.Code == http.StatusOK && !isJSON(w) {
			t.Errorf("%s 返回了非 JSON（会变成 'Unexpected token <'）: %s", target, w.Body.String())
		}
		if isJSON(w) && codeOf(t, w) != 40002 {
			t.Errorf("%s 业务码 = %d, want 40002（不存在）", target, codeOf(t, w))
		}
	}
}

func Test前端路由能回index(t *testing.T) {
	ui := builtUI(t)
	for _, target := range []string{"/", "/runs/8", "/cases/new"} {
		w := serveNoRoute(t, ui, http.MethodGet, target)
		if w.Code != http.StatusOK {
			t.Errorf("%s 状态码 = %d, want 200", target, w.Code)
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("%s Content-Type = %q, want text/html", target, got)
		}
		if !strings.Contains(w.Body.String(), `id="app"`) {
			t.Errorf("%s 未回 index.html: %s", target, w.Body.String())
		}
	}
}

// 非 GET 的未知路径不能落进 SPA（回一坨 HTML 对调用方毫无意义），
// 但**HTTP 状态仍是 200 + 业务码非 0** —— 这是本项目的统一响应约定
// （见 render.go 的 Fail：业务失败走 HTTP 200，由 code 表达语义），
// 这里把既有契约锁住，避免顺手"改成 404"把前端逻辑打散。
func Test非GET未知路径回JSON兜底而非HTML(t *testing.T) {
	ui := builtUI(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		w := serveNoRoute(t, ui, m, "/whatever")
		if !isJSON(w) {
			t.Errorf("%s /whatever 应回 JSON 兜底错误，实际: %s", m, w.Body.String())
		}
		if code := codeOf(t, w); code != 40002 {
			t.Errorf("%s /whatever 业务码 = %d, want 40002", m, code)
		}
		if strings.Contains(w.Body.String(), `id="app"`) {
			t.Errorf("%s /whatever 不该回 index.html", m)
		}
	}
}

// 未接入内嵌前端时（单测 / 只做 API 的部署），兜底仍须是 JSON 而不是 panic。
func Test未接入前端时兜底仍是JSON(t *testing.T) {
	w := serveNoRoute(t, nil, http.MethodGet, "/")
	if !isJSON(w) {
		t.Errorf("应回 JSON，实际: %s", w.Body.String())
	}
	if codeOf(t, w) != 40002 {
		t.Errorf("业务码 = %d, want 40002", codeOf(t, w))
	}
}

func isJSON(w *httptest.ResponseRecorder) bool {
	return len(w.Body.Bytes()) > 0 && w.Body.Bytes()[0] == '{'
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, w.Body.String())
	}
	return body.Code
}
