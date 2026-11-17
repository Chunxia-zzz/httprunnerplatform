package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func TestNormalizeMockPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"$base_url/users", "/users"},
		{"$base_url/users/", "/users"},
		{"https://api.example.com/users/1?page=2", "/users/1"},
		{"http://127.0.0.1:8899/", "/"},
		{"users/1", "/users/1"},
		{"/users/1?x=1#f", "/users/1"},
		{"   ", "/"},
		{"$base_url", "/"},
	}
	for _, c := range cases {
		if got := normalizeMockPath(c.in); got != c.want {
			t.Errorf("normalizeMockPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchMockPath(t *testing.T) {
	cases := []struct {
		def, req string
		want     bool
	}{
		{"$base_url/users", "/users", true},
		{"$base_url/users", "/Users", false}, // 大小写敏感
		{"$base_url/users/{id}", "/users/123", true},
		{"$base_url/users/:id", "/users/abc", true},
		{"$base_url/users/$uid", "/users/9", true},
		{"$base_url/users/{id}/orders/{oid}", "/users/1/orders/2", true},
		{"$base_url/users/{id}", "/users/1/orders", false}, // 段数不同
		{"$base_url/users", "/users/1", false},
		{"$base_url/users/{id}", "/users/123?page=2", true}, // 请求带 query
		{"https://x.com/users/{id}", "/users/1", true},      // 定义带 host
	}
	for _, c := range cases {
		if got := matchMockPath(c.def, c.req); got != c.want {
			t.Errorf("matchMockPath(%q, %q) = %v, want %v", c.def, c.req, got, c.want)
		}
	}
}

func TestMockServe生命周期(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	mock := New(d).Mock

	mustAPI(t, d, p.ID, APIReq{
		Code: "get_user", Name: "查用户", Module: "user", Method: "GET",
		Path: "$base_url/users/{id}", Body: jsonx.Any{Val: map[string]any{"id": 1, "name": "张三"}},
		BodyType: "json",
	})

	// 1) 未开启：404 且说明原因
	status, _, body := mock.Serve(p.Code, "GET", "/users/1")
	if status != http.StatusNotFound || !strings.Contains(string(body), "未开启") {
		t.Fatalf("未开启时 = %d %s", status, body)
	}

	// 2) 开启后命中：200 + 定义里的 body
	if err := mock.SetEnabled(p.ID, true); err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	status, headers, body = mock.Serve(p.Code, "GET", "/users/123")
	if status != http.StatusOK {
		t.Fatalf("命中时 status = %d, body = %s", status, body)
	}
	if headers["X-Mock-Source"] != "apidef:get_user" {
		t.Fatalf("X-Mock-Source = %q", headers["X-Mock-Source"])
	}
	if !strings.Contains(string(body), "张三") {
		t.Fatalf("响应体未取自接口定义: %s", body)
	}

	// 3) 方法不匹配：405 且指出定义的方法
	status, _, body = mock.Serve(p.Code, "POST", "/users/1")
	if status != http.StatusMethodNotAllowed || !strings.Contains(string(body), "GET") {
		t.Fatalf("方法不匹配 = %d %s", status, body)
	}

	// 4) 未命中：404 且带已配置数量
	status, _, body = mock.Serve(p.Code, "GET", "/nope")
	if status != http.StatusNotFound || !strings.Contains(string(body), "共 1 条") {
		t.Fatalf("未命中 = %d %s", status, body)
	}

	// 5) 项目不存在：404
	status, _, _ = mock.Serve("no-such-project", "GET", "/users/1")
	if status != http.StatusNotFound {
		t.Fatalf("未知项目 status = %d", status)
	}
}

func TestMockHits上限与倒序(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	mock := New(d).Mock
	if err := mock.SetEnabled(p.ID, true); err != nil {
		t.Fatal(err)
	}
	mustAPI(t, d, p.ID, APIReq{Code: "ping", Method: "GET", Path: "/ping"})

	for i := 0; i < mockHitCap+5; i++ {
		mock.Serve(p.Code, "GET", "/ping")
	}
	hits := mock.Hits(p.ID, mockHitCap)
	if len(hits) != mockHitCap {
		t.Fatalf("保留条数 = %d, want %d", len(hits), mockHitCap)
	}
	// 默认取 50 条
	if got := len(mock.Hits(p.ID, 0)); got != 50 {
		t.Fatalf("默认 limit 条数 = %d, want 50", got)
	}
}

func TestMockInfo列出可mock接口(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	mustAPI(t, d, p.ID, APIReq{Code: "a1", Module: "m", Method: "GET", Path: "/a1",
		Body: jsonx.Any{Val: map[string]any{"ok": true}}, BodyType: "json"})
	mustAPI(t, d, p.ID, APIReq{Code: "a2", Module: "m", Method: "POST", Path: "/a2", BodyType: "none"})

	info, err := New(d).Mock.Info(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.BasePath != "/mock/"+p.Code {
		t.Fatalf("BasePath = %q", info.BasePath)
	}
	if len(info.APIs) != 2 || !info.APIs[0].HasBody || info.APIs[1].HasBody {
		t.Fatalf("接口清单 = %+v", info.APIs)
	}
	if info.SampleCurl == "" {
		t.Fatal("示例 curl 不应为空")
	}
}
