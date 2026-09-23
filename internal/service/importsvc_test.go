package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/decompiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/hrpclient"
)

// ---------------------------------------------------------------------------
// 纯函数
// ---------------------------------------------------------------------------

func TestDetectFormat(t *testing.T) {
	har := `{"log":{"entries":[{"request":{"method":"GET"}}]}}`
	postman := `{"info":{"name":"col","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[]}`
	postman2 := `{"item":[{"name":"r","request":{"url":"http://x"}}]}`
	curl := "curl -H 'a: b' http://x/y"
	cases := []struct {
		name     string
		filename string
		content  string
		want     string
	}{
		{"HAR 内容探测", "any.json", har, "har"},
		{"Postman schema 探测", "any.json", postman, "postman"},
		{"Postman item 探测", "any.json", postman2, "postman"},
		{"curl 前缀探测", "cmd.txt", curl, "curl"},
		{"HAR 扩展名兜底", "c.har", `{"log":{}}`, "har"},
		{"无法识别", "unknown.txt", "hello world", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectFormat(c.filename, []byte(c.content))
			if got != c.want {
				t.Fatalf("detectFormat(%q) = %q, want %q", c.filename, got, c.want)
			}
		})
	}
}

func TestSanitizeIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Mini Capture", "mini_capture"},
		{"ABC-123_x", "abc-123_x"},
		{"  spaced  ", "spaced"},
	}
	for _, c := range cases {
		if got := sanitizeIdent(c.in); got != c.want {
			t.Errorf("sanitizeIdent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShapeImported_占位名与空步骤名(t *testing.T) {
	newFixture := func() *decompiler.CaseReq {
		return &decompiler.CaseReq{
			Steps: []decompiler.StepReq{{
				Seq:      1,
				StepType: "request",
				Request:  &decompiler.Request{Method: "GET", URL: "http://127.0.0.1:8133/login?u=alice"},
			}},
		}
	}

	// HAR 占位名 → 用文件名
	cr := newFixture()
	cr.Name = "testcase description"
	shapeImported(cr, "mini", 0)
	if cr.Name != "mini" {
		t.Fatalf("占位名应被替换为文件名，实际 %q", cr.Name)
	}
	if cr.Steps[0].Name != "GET /login?u=alice" {
		t.Fatalf("空步骤名应生成为 GET /login?u=alice，实际 %q", cr.Steps[0].Name)
	}

	// 非占位名保留
	cr = newFixture()
	cr.Name = "真实名字"
	shapeImported(cr, "mini", 0)
	if cr.Name != "真实名字" {
		t.Fatalf("非占位名应保留，实际 %q", cr.Name)
	}

	// 多文件场景第二个用例带序号
	cr = newFixture()
	cr.Name = "testcase description"
	shapeImported(cr, "mini", 1)
	if cr.Name != "mini_2" {
		t.Fatalf("第 2 个候选应带序号，实际 %q", cr.Name)
	}

	// 有名字的步骤保留
	cr = newFixture()
	cr.Steps[0].Name = "已有名字"
	shapeImported(cr, "mini", 0)
	if cr.Steps[0].Name != "已有名字" {
		t.Fatalf("已有步骤名应保留，实际 %q", cr.Steps[0].Name)
	}
}

// ---------------------------------------------------------------------------
// 集成：真实 hrp convert（引擎不可用时跳过 —— 不让环境决定单测红绿）
// ---------------------------------------------------------------------------

const importTestHAR = `{"log":{"version":"1.2","creator":{"name":"probe","version":"1"},
	"entries":[{"startedDateTime":"2026-09-23T16:00:00+08:00","time":100,
	"request":{"method":"GET","url":"http://127.0.0.1:8133/login?u=alice",
	"httpVersion":"HTTP/1.1","headers":[{"name":"Authorization","value":"Bearer tok"}],
	"queryString":[{"name":"u","value":"alice"}],"cookies":[],"headersSize":60,"bodySize":0},
	"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],
	"cookies":[],"content":{"size":30,"mimeType":"application/json","text":"{}"},
	"redirectURL":"","headersSize":50,"bodySize":30},"cache":{},
	"timings":{"send":1,"wait":90,"receive":9}}]}}`

// findHrpForTest 从当前目录向上查找仓库同级的工具目录，设置 HRP_BINARY_PATH。
// 找不到时返回 false（集成测试随即 skip，不让环境决定红绿）。
func findHrpForTest(t *testing.T) bool {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	for i := 0; i < 6; i++ {
		cand := filepath.Join(dir, "httprunnerplatform-tools", "bin", "hrp.exe")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			t.Setenv("HRP_BINARY_PATH", cand)
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

func TestImport_端到端(t *testing.T) {
	if !findHrpForTest(t) {
		if _, err := hrpclient.Resolve(""); err != nil {
			t.Skipf("hrp 引擎不可用，跳过导入集成测试: %v", err)
		}
	}

	d := testDeps(t)
	svc := NewImportService(d, &CaseService{Deps: d})
	proj := seedProject(t, d)
	req := ImportRequest{Format: ImportFormatAuto, Filename: "mini.har", Content: []byte(importTestHAR)}

	// 预览：不落库
	preview, err := svc.Preview(proj.ID, req)
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if preview.Detected != "har" {
		t.Fatalf("应识别为 har，实际 %q", preview.Detected)
	}
	if len(preview.Cases) != 1 {
		t.Fatalf("应产出 1 个候选用例，实际 %d", len(preview.Cases))
	}
	ic := preview.Cases[0]
	if ic.Name != "mini" {
		t.Fatalf("占位名应替换为 mini，实际 %q", ic.Name)
	}
	if !strings.HasPrefix(ic.Code, "imp_mini") {
		t.Fatalf("code 应为 imp_ 前缀，实际 %q", ic.Code)
	}
	if ic.StepCount != 1 || len(ic.Summary) != 1 || !strings.Contains(ic.Summary[0], "GET /login") {
		t.Fatalf("步骤摘要不符：%+v", ic)
	}

	var count int64
	d.DB.Model(&model.TestCase{}).Where("project_id = ?", proj.ID).Count(&count)
	if count != 0 {
		t.Fatalf("Preview 不应落库，实际已有 %d 条用例", count)
	}

	// 提交：落库
	commit, err := svc.Commit(proj.ID, req, 1)
	if err != nil {
		t.Fatalf("Commit 失败: %v", err)
	}
	if len(commit.Created) != 1 || len(commit.Failed) != 0 {
		t.Fatalf("应创建 1 条，实际 created=%d failed=%d %v", len(commit.Created), len(commit.Failed), commit.Failed)
	}

	// 再导一次：重名自动加后缀
	commit2, err := svc.Commit(proj.ID, req, 1)
	if err != nil {
		t.Fatalf("第二次 Commit 失败: %v", err)
	}
	if len(commit2.Created) != 1 {
		t.Fatalf("第二次导入应创建 1 条，实际 %v", commit2)
	}
	if commit2.Created[0].Name == commit.Created[0].Name {
		t.Fatalf("重名未去重：%q 与 %q", commit2.Created[0].Name, commit.Created[0].Name)
	}

	// 落库内容抽查：HAR 字典形态断言应被正确解析
	detail, err := svc.Case.Get(commit.Created[0].ID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if len(detail.Steps) != 1 || len(detail.Steps[0].Validate) == 0 {
		t.Fatalf("导入的用例应含 1 步 1 断言，实际 %+v", detail.Steps)
	}
	v := detail.Steps[0].Validate[0]
	if v.Check != "status_code" || fmt.Sprint(v.Expect) != "200" {
		t.Fatalf("断言解析错误：%+v", v)
	}
	// HAR 的 query 应进 params，headers 保留（Request 是 jsonx.Any 值类型，取 Val 抽查）
	reqAny := detail.Steps[0].Request
	if reqAny.Val == nil {
		t.Fatal("request 不应为空")
	}
	if m, ok := reqAny.Val.(map[string]any); !ok || len(m) == 0 {
		t.Fatalf("request 应为非空映射，实际 %#v", reqAny.Val)
	}
}

func TestImport_空文件与未知格式(t *testing.T) {
	if !findHrpForTest(t) {
		if _, err := hrpclient.Resolve(""); err != nil {
			t.Skipf("hrp 引擎不可用，跳过: %v", err)
		}
	}
	d := testDeps(t)
	svc := NewImportService(d, &CaseService{Deps: d})
	proj := seedProject(t, d)

	if _, err := svc.Preview(proj.ID, ImportRequest{Format: "auto", Filename: "x.txt", Content: []byte("hello")}); err == nil {
		t.Fatal("无法识别的格式应报错")
	}
	if _, err := svc.Preview(proj.ID, ImportRequest{Format: "auto", Filename: "x.har", Content: nil}); err == nil {
		t.Fatal("空文件应报错")
	}
	if _, err := svc.Preview(proj.ID, ImportRequest{Format: "xml", Filename: "x.xml", Content: []byte("<a/>")}); err == nil {
		t.Fatal("显式不支持的格式应报错")
	}
}
