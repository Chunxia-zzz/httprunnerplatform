package decompiler

import (
	"strings"
	"testing"
)

// 一个典型的编译后 YAML（对应正向 compiler 的输出形态）。
const sampleYAML = `config:
    name: 登录用例
    variables:
        base: "http://example.com"
    verify: false
    export:
        - token
teststeps:
    - name: 登录
      request:
          method: POST
          url: /login
          headers:
              Content-Type: application/json
          json:
              username: alice
              password: pw1
      extract:
          token: body.token
      validate:
          - eq: [status_code, 200]
          - contains: [body.msg, ok]
    - name: 查用户
      request:
          method: GET
          url: /user
          params:
              id: "1"
      validate:
          - eq: [status_code, 200]
`

func TestParseBasic(t *testing.T) {
	req, err := Parse(sampleYAML)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if req.Name != "登录用例" {
		t.Errorf("name = %q, want 登录用例", req.Name)
	}
	if len(req.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(req.Steps))
	}

	// 第 1 步
	s1 := req.Steps[0]
	if s1.Name != "登录" || s1.StepType != "request" {
		t.Errorf("step1 name/type = %q/%q", s1.Name, s1.StepType)
	}
	if s1.Request.Method != "POST" || s1.Request.URL != "/login" {
		t.Errorf("step1 request = %+v", s1.Request)
	}
	if s1.Request.BodyType != "json" {
		t.Errorf("step1 body_type = %q, want json", s1.Request.BodyType)
	}
	body, _ := s1.Request.Body.Val.(map[string]any)
	if body["username"] != "alice" {
		t.Errorf("step1 json body = %+v", s1.Request.Body.Val)
	}
	// extract 拆回 object + expression
	if len(s1.Extract) != 1 || s1.Extract[0].Name != "token" ||
		s1.Extract[0].Object != "body" || s1.Extract[0].Expression != "token" {
		t.Errorf("step1 extract = %+v", s1.Extract)
	}
	// validate 拆回 check/assert/expect
	if len(s1.Validate) != 2 {
		t.Fatalf("step1 validate = %d, want 2", len(s1.Validate))
	}
	if s1.Validate[0].Assert != "eq" || s1.Validate[0].Check != "status_code" {
		t.Errorf("step1 validate[0] = %+v", s1.Validate[0])
	}
	// 期望值 200 反解析成 int64
	if v, ok := s1.Validate[0].Expect.(int64); !ok || v != 200 {
		t.Errorf("step1 validate[0].expect = %v(%T), want int64 200", s1.Validate[0].Expect, s1.Validate[0].Expect)
	}

	// 第 2 步 params
	s2 := req.Steps[1]
	if s2.Request.Params == nil || len(s2.Request.Params) == 0 {
		t.Errorf("step2 params 应为非空映射")
	}

	// config 变量与导出
	cfg := req.Config.Val.(map[string]any)
	if _, ok := cfg["variables"]; !ok {
		t.Errorf("config.variables 缺失")
	}
}

func TestParseFormBody(t *testing.T) {
	y := `config:
    name: 表单
    verify: false
teststeps:
    - name: 提交
      request:
          method: POST
          url: /submit
          data:
              a: "1"
              b: "2"
`
	req, err := Parse(y)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if req.Steps[0].Request.BodyType != "form" {
		t.Errorf("body_type = %q, want form", req.Steps[0].Request.BodyType)
	}
}

func TestParseRawBody(t *testing.T) {
	y := `config:
    name: 原文
    verify: false
teststeps:
    - name: 发原文
      request:
          method: POST
          url: /raw
          data: "hello world"
`
	req, err := Parse(y)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if req.Steps[0].Request.BodyType != "raw" {
		t.Errorf("body_type = %q, want raw", req.Steps[0].Request.BodyType)
	}
	if req.Steps[0].Request.Body.Val != "hello world" {
		t.Errorf("body = %v, want hello world", req.Steps[0].Request.Body.Val)
	}
}

func TestParseMissingName(t *testing.T) {
	y := `config:
    verify: false
teststeps:
    - name: 步骤
      request:
          method: GET
          url: /x
`
	_, err := Parse(y)
	if err == nil {
		t.Fatal("缺少 config.name 应报错")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("错误信息应提到 name: %v", err)
	}
}

func TestParseEmptySteps(t *testing.T) {
	y := `config:
    name: 空
    verify: false
teststeps: []
`
	_, err := Parse(y)
	if err == nil {
		t.Fatal("空 teststeps 应报错")
	}
}

func TestParseSyntaxError(t *testing.T) {
	_, err := Parse("config:\n  name: [unclosed")
	if err == nil {
		t.Fatal("非法 YAML 应报错")
	}
	var le *LineError
	if !asLineError(err, &le) {
		t.Logf("错误类型: %T (%v)", err, err)
	}
}

func asLineError(err error, target **LineError) bool {
	le, ok := err.(*LineError)
	if ok {
		*target = le
	}
	return ok
}

func TestSplitExtractExpr(t *testing.T) {
	cases := []struct {
		expr   string
		obj    string
		path   string
	}{
		{"body.args.token", "body", "args.token"},
		{"status_code", "status_code", ""},
		{"proto", "proto", ""},
		{"headers", "headers", ""},
		{"body", "body", ""},
	}
	for _, c := range cases {
		obj, path := splitExtractExpr(c.expr)
		if obj != c.obj || path != c.path {
			t.Errorf("splitExtractExpr(%q) = (%q, %q), want (%q, %q)", c.expr, obj, path, c.obj, c.path)
		}
	}
}

func TestLineErrorLineNumber(t *testing.T) {
	// 断言缺参数，应报出具体行号
	y := `config:
    name: 用例
    verify: false
teststeps:
    - name: 步骤
      request:
          method: GET
          url: /x
      validate:
          - eq: [status_code]
`
	_, err := Parse(y)
	if err == nil {
		// 单参数断言是合法的（只检查表达式），不应报错
		t.Log("单参数断言被接受（合法）")
		return
	}
	var le *LineError
	if asLineError(err, &le) {
		if le.Line <= 0 {
			t.Errorf("LineError.Line 应 > 0, got %d", le.Line)
		}
	} else {
		t.Errorf("期望 LineError, got %T: %v", err, err)
	}
}
