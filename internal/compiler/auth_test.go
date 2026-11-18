package compiler

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

func envWithAuth(a model.AuthConfig) *model.Environment {
	e := newEnv()
	e.Auth = a
	return e
}

func renderWithAuth(t *testing.T, env *model.Environment, steps []model.TestStep) *Output {
	t.Helper()
	tc, _ := newCase()
	out, err := Render(&Input{
		Project: newProject(), Env: env,
		Cases: []CaseSpec{{Case: tc, Steps: steps}},
	})
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	return out
}

func TestAuth_环境Bearer注入(t *testing.T) {
	tc, steps := newCase()
	out := renderWithAuth(t, envWithAuth(model.AuthConfig{Type: model.AuthTypeBearer, Token: "envtok"}), steps)
	if !strings.Contains(out.Cases[0].YAML, "Authorization: Bearer envtok") {
		t.Fatalf("环境认证未注入:\n%s", out.Cases[0].YAML)
	}
	_ = tc
}

func TestAuth_步骤覆盖与环境继承(t *testing.T) {
	env := envWithAuth(model.AuthConfig{Type: model.AuthTypeBearer, Token: "envtok"})

	// 步骤 Bearer 覆盖
	_, steps := newCase()
	steps[0].Auth = model.AuthConfig{Type: model.AuthTypeBearer, Token: "$token"}
	out := renderWithAuth(t, env, steps)
	if !strings.Contains(out.Cases[0].YAML, "Authorization: Bearer $token") {
		t.Fatalf("步骤认证未覆盖:\n%s", out.Cases[0].YAML)
	}

	// inherit 沿用环境
	_, steps2 := newCase()
	steps2[0].Auth = model.AuthConfig{Type: model.AuthTypeInherit}
	out2 := renderWithAuth(t, env, steps2)
	if !strings.Contains(out2.Cases[0].YAML, "Bearer envtok") {
		t.Fatalf("inherit 未沿用环境:\n%s", out2.Cases[0].YAML)
	}

	// none 去掉认证：注到每一步（否则另一步仍会继承环境）
	_, steps3 := newCase()
	for i := range steps3 {
		steps3[i].Auth = model.AuthConfig{Type: model.AuthTypeNone}
	}
	out3 := renderWithAuth(t, env, steps3)
	if strings.Contains(out3.Cases[0].YAML, "Authorization") {
		t.Fatalf("none 仍注入了认证:\n%s", out3.Cases[0].YAML)
	}
}

func TestAuth_Basic与APIKey(t *testing.T) {
	_, steps := newCase()
	steps[0].Auth = model.AuthConfig{Type: model.AuthTypeBasic, Username: "u", Password: "p"}
	out := renderWithAuth(t, newEnv(), steps)
	// base64("u:p") = dTpw
	if !strings.Contains(out.Cases[0].YAML, "Authorization: Basic dTpw") {
		t.Fatalf("Basic 编码错误:\n%s", out.Cases[0].YAML)
	}

	_, steps2 := newCase()
	steps2[0].Auth = model.AuthConfig{Type: model.AuthTypeAPIKey, KeyName: "X-Api-Key", KeyValue: "k1", KeyIn: model.AuthInHeader}
	out2 := renderWithAuth(t, newEnv(), steps2)
	if !strings.Contains(out2.Cases[0].YAML, "X-Api-Key: k1") {
		t.Fatalf("API Key（header）未注入:\n%s", out2.Cases[0].YAML)
	}

	_, steps3 := newCase()
	steps3[0].Auth = model.AuthConfig{Type: model.AuthTypeAPIKey, KeyName: "ak", KeyValue: "k2", KeyIn: model.AuthInQuery}
	out3 := renderWithAuth(t, newEnv(), steps3)
	if !strings.Contains(out3.Cases[0].YAML, "ak: k2") {
		t.Fatalf("API Key（query）未注入:\n%s", out3.Cases[0].YAML)
	}
}

func TestAuth_手写请求头优先(t *testing.T) {
	_, steps := newCase()
	steps[0].Auth = model.AuthConfig{Type: model.AuthTypeBearer, Token: "authtok"}
	steps[0].Request = anyOf(map[string]any{
		"method": "GET", "url": "$base_url/get",
		"headers": map[string]any{"authorization": "Custom xyz"}, // 大小写不敏感覆盖
	})
	out := renderWithAuth(t, newEnv(), steps)
	y := out.Cases[0].YAML
	if !strings.Contains(y, "Custom xyz") || strings.Contains(y, "Bearer authtok") {
		t.Fatalf("手写头应覆盖认证:\n%s", y)
	}
}

func TestDynamic_编译期展开(t *testing.T) {
	_, steps := newCase()
	steps[0].Request = anyOf(map[string]any{
		"method": "GET",
		"url":    "$base_url/items/{{$uuid}}?ts={{$timestamp}}",
		"headers": map[string]any{
			"X-Req-Id": "{{$uuid}}",
		},
		"body":      map[string]any{"trace": "{{$timestampMs}}"},
		"body_type": model.BodyTypeJSON,
	})
	out := renderWithAuth(t, newEnv(), steps)
	y := out.Cases[0].YAML

	uuidRe := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
	if !uuidRe.MatchString(y) {
		t.Fatalf("UUID 未展开或格式错误:\n%s", y)
	}
	if strings.Contains(y, "{{$") {
		t.Fatalf("仍有未展开的动态变量:\n%s", y)
	}
	// 时间戳：10 位（秒）与 13 位（毫秒）各一处
	if !regexp.MustCompile(`ts=\d{10}`).MatchString(y) {
		t.Fatalf("秒级时间戳未展开:\n%s", y)
	}
	if !regexp.MustCompile(`trace: "?\d{13}"?`).MatchString(y) {
		t.Fatalf("毫秒时间戳未展开:\n%s", y)
	}

	// 两次编译 uuid 不同
	out2 := renderWithAuth(t, newEnv(), steps)
	if out.Cases[0].YAML == out2.Cases[0].YAML {
		t.Fatal("两次编译的 UUID 应不同")
	}
}
