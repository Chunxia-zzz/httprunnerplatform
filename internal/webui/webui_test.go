package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// builtFS 模拟一次成功的 vite build 产物。
func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      &fstest.MapFile{Data: []byte(`<!DOCTYPE html><div id="app"></div>`)},
		"assets/app.js":   &fstest.MapFile{Data: []byte(`console.log("hi")`)},
		"favicon.svg":     &fstest.MapFile{Data: []byte(`<svg/>`)},
		"build-id":        &fstest.MapFile{Data: []byte("2026-09-23 00:20:00\n")},
		"nested/keep.txt": &fstest.MapFile{Data: []byte("x")},
	}
}

// emptyFS 模拟全新克隆：内嵌目录里只有 .gitkeep 占位。
func emptyFS() fstest.MapFS {
	return fstest.MapFS{".gitkeep": &fstest.MapFile{Data: []byte("placeholder")}}
}

// serve 在只挂了 NoRoute 的最小引擎上发一次请求。
func serve(t *testing.T, ui *UI, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.NoRoute(ui.Handler())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func mustNew(t *testing.T, fsys fs.FS) *UI {
	t.Helper()
	ui, err := New(fsys)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return ui
}

// ---------------------------------------------------------------------------
// 未构建：必须降级成一个"看得懂"的提示页，而不是白屏或 404
// ---------------------------------------------------------------------------

func Test未构建时给出提示页而不是白屏(t *testing.T) {
	ui := mustNew(t, emptyFS())

	if ui.Available() {
		t.Fatalf("只有占位文件时 Available() 应为 false")
	}
	if !strings.Contains(ui.Describe(), "未内嵌前端") {
		t.Errorf("Describe 应点明未内嵌，实际: %q", ui.Describe())
	}

	for _, target := range []string{"/", "/runs/8", "/cases/new"} {
		w := serve(t, ui, http.MethodGet, target)
		if w.Code != http.StatusOK {
			t.Errorf("%s 状态码 = %d, want 200", target, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s Content-Type = %q, want text/html", target, ct)
		}
		if !strings.Contains(w.Body.String(), "前端还没有内嵌进来") {
			t.Errorf("%s 应返回构建提示页，实际: %s", target, truncate(w.Body.String()))
		}
		// 不能缓存：用户构建完刷新就要看到真实界面
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s 提示页 Cache-Control = %q, want no-store", target, cc)
		}
	}
}

// ---------------------------------------------------------------------------
// 已构建：静态资源 / SPA fallback / 缓存策略
// ---------------------------------------------------------------------------

func Test已构建时根路径与前端路由都回index(t *testing.T) {
	ui := mustNew(t, builtFS())
	if !ui.Available() {
		t.Fatal("有 index.html 时 Available() 应为 true")
	}
	if !strings.Contains(ui.Describe(), "2026-09-23 00:20:00") {
		t.Errorf("Describe 应带上构建时间，实际: %q", ui.Describe())
	}

	// 前端路由（history 模式深链、刷新）必须能落到 index.html
	for _, target := range []string{"/", "/runs/8", "/cases/new", "/projects"} {
		w := serve(t, ui, http.MethodGet, target)
		if w.Code != http.StatusOK {
			t.Errorf("%s 状态码 = %d, want 200", target, w.Code)
		}
		if !strings.Contains(w.Body.String(), `id="app"`) {
			t.Errorf("%s 未回 index.html，实际: %s", target, truncate(w.Body.String()))
		}
		// index.html 里写着带哈希的资源名，缓存它会导致新版本刷新不出来
		if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s index Cache-Control = %q, want no-cache", target, cc)
		}
	}
}

func Test静态资源带内容哈希可长缓存(t *testing.T) {
	ui := mustNew(t, builtFS())

	w := serve(t, ui, http.MethodGet, "/assets/app.js")
	if w.Code != http.StatusOK {
		t.Fatalf("assets 状态码 = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `console.log`) {
		t.Errorf("assets 内容不对: %s", truncate(w.Body.String()))
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("assets Cache-Control = %q, want 含 immutable", cc)
	}

	// 非 assets/ 下的静态文件不加长缓存（本项目里有 favicon 这类可能变的名字）
	other := serve(t, ui, http.MethodGet, "/favicon.svg")
	if other.Code != http.StatusOK {
		t.Fatalf("favicon 状态码 = %d, want 200", other.Code)
	}
	if cc := other.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("favicon 不该长缓存，实际: %q", cc)
	}
}

// ⭐ 这条是整个 Handler 里最要紧的断言：
// 带扩展名却找不到的资源若回 index.html，浏览器只会报
// 「Unexpected token '<'」，与真实原因（前端换版本/缓存了旧 HTML）毫无关系。
func Test带扩展名却不存在必须真404(t *testing.T) {
	ui := mustNew(t, builtFS())

	for _, target := range []string{"/assets/index-旧哈希.js", "/assets/removed.css", "/missing.png"} {
		w := serve(t, ui, http.MethodGet, target)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d, want 404", target, w.Code)
		}
		if strings.Contains(w.Body.String(), `id="app"`) {
			t.Errorf("%s 不该回 index.html（会变成 'Unexpected token <'）", target)
		}
	}
}

func Test路径穿越被挡(t *testing.T) {
	ui := mustNew(t, builtFS())

	for _, target := range []string{"/../go.mod", "/assets/../../go.mod", "/./../../etc/passwd"} {
		w := serve(t, ui, http.MethodGet, target)
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "module ") {
			t.Errorf("%s 泄漏了仓库文件: %s", target, truncate(w.Body.String()))
		}
	}
}

func Test子目录里的文件也能取到(t *testing.T) {
	ui := mustNew(t, builtFS())
	w := serve(t, ui, http.MethodGet, "/nested/keep.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "x") {
		t.Errorf("内容不对: %s", truncate(w.Body.String()))
	}
}

func TestFileCount与BuildID(t *testing.T) {
	ui := mustNew(t, builtFS())
	// index.html + assets/app.js + favicon.svg + build-id + nested/keep.txt
	if ui.FileCount() != 5 {
		t.Errorf("FileCount = %d, want 5", ui.FileCount())
	}
	if ui.BuildID() != "2026-09-23 00:20:00" {
		t.Errorf("BuildID = %q（应去掉尾部换行）", ui.BuildID())
	}

	// 未构建时不该有构建时间
	if got := mustNew(t, emptyFS()).BuildID(); got != "" {
		t.Errorf("未构建时 BuildID 应为空，实际 %q", got)
	}
}

// 内嵌资源里连 index.html 都没有时，New 不该报错 —— 那是"没构建"，
// 不是"坏了"。真正的故障（embed 目录名写错）由 Load 里的 fs.Sub 负责暴露。
func Test没有index也不算错误(t *testing.T) {
	ui, err := New(fstest.MapFS{})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if ui.Available() {
		t.Error("Available 应为 false")
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
