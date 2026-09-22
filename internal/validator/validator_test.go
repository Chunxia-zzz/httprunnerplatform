package validator

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/compiler"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// 夹具
// ---------------------------------------------------------------------------

func anyOf(v any) jsonx.Any { return jsonx.Any{Val: v} }

func reqStep(seq int, url string, extra map[string]any) model.TestStep {
	body := map[string]any{"method": "GET", "url": url}
	for k, v := range extra {
		body[k] = v
	}
	return model.TestStep{
		Seq: seq, StepType: model.StepRequest, Name: "步骤",
		Request: anyOf(body), Enabled: true,
	}
}

func newEnv() *model.Environment {
	return &model.Environment{
		Name:          "本地",
		BaseURL:       "http://127.0.0.1:8899",
		Environs:      jsonx.Map{"tenant": "acme"},
		GlobalHeaders: jsonx.Map{},
	}
}

func newCase() *model.TestCase {
	tc := &model.TestCase{Code: "tc_1", Name: "用例一"}
	tc.ID = 1
	return tc
}

func codes(r *Result) []string {
	out := make([]string, 0, len(r.Issues))
	for _, is := range r.Issues {
		out = append(out, is.Code)
	}
	return out
}

func hasCode(r *Result, code string) bool {
	for _, is := range r.Issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

func hasCodeAtLevel(r *Result, code, level string) bool {
	for _, is := range r.Issues {
		if is.Code == code && is.Level == level {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 变量引用
// ---------------------------------------------------------------------------

func TestValidate_未定义变量被拦下(t *testing.T) {
	// 实测：引擎遇到未定义变量直接以退出码 21 中止，错误文本晦涩
	// （`parse request params failed: variable xxx not found`）。
	// 平台在保存前拦下，用户就不用等到运行时才发现。
	res := Validate(&Input{
		Env: newEnv(),
		Cases: []CaseSpec{{
			Case:  newCase(),
			Steps: []model.TestStep{reqStep(1, "$base_url/get", map[string]any{"params": map[string]any{"t": "$token"}})},
		}},
	})

	if !hasCodeAtLevel(res, CodeUndefinedVariable, LevelError) {
		t.Fatalf("未定义的 $token 应报 error，实际: %+v", res.Issues)
	}
	// 报错要能明确指出是哪个变量、在哪一步
	found := false
	for _, is := range res.Issues {
		if is.Code == CodeUndefinedVariable && strings.Contains(is.Message, "token") && is.Seq == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("报错信息未指出变量名与步骤: %+v", res.Issues)
	}
}

func TestValidate_环境与用例变量可用(t *testing.T) {
	tc := newCase()
	tc.Config = anyOf(map[string]any{"variables": map[string]any{"user": "tester"}})

	res := Validate(&Input{
		Env: newEnv(),
		Cases: []CaseSpec{{
			Case: tc,
			Steps: []model.TestStep{reqStep(1, "$base_url/get",
				map[string]any{"params": map[string]any{"u": "$user", "t": "$tenant"}})},
		}},
	})

	if hasCode(res, CodeUndefinedVariable) {
		t.Fatalf("base_url / 环境变量 / 用例变量都应可用，误报: %+v", res.Issues)
	}
}

func TestValidate_前序步骤提取的变量对后续可见(t *testing.T) {
	step1 := reqStep(1, "$base_url/get", nil)
	step1.Extract = jsonx.Slice[model.ExtractItem]{{Name: "token", Object: model.ExtractBody, Expression: "args.token"}}

	step2 := reqStep(2, "$base_url/get", map[string]any{"params": map[string]any{"t": "$token"}})

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step1, step2}}}})
	if hasCode(res, CodeUndefinedVariable) {
		t.Fatalf("步骤2 引用步骤1 提取的 token 应当合法，误报: %+v", res.Issues)
	}
}

func TestValidate_同一步骤内不能用自己的提取结果(t *testing.T) {
	// 引擎的执行顺序是「先解析请求 → 发请求 → 再提取」，
	// 所以本步骤的 extract 结果在本步骤的请求里拿不到。
	step := reqStep(1, "$base_url/get", map[string]any{"params": map[string]any{"t": "$token"}})
	step.Extract = jsonx.Slice[model.ExtractItem]{{Name: "token", Object: model.ExtractBody, Expression: "args.token"}}

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step}}}})
	if !hasCodeAtLevel(res, CodeUndefinedVariable, LevelError) {
		t.Fatalf("本步骤不应看到自己的提取结果，实际: %+v", res.Issues)
	}
}

func TestValidate_步骤局部变量可见(t *testing.T) {
	step := reqStep(1, "$base_url/get", map[string]any{"params": map[string]any{"v": "$local"}})
	step.Variables = jsonx.Map{"local": "x"}

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step}}}})
	if hasCode(res, CodeUndefinedVariable) {
		t.Fatalf("步骤局部变量应可用，误报: %+v", res.Issues)
	}
}

func TestValidate_变量名扫描不会把函数表达式当变量(t *testing.T) {
	// `${ENV(X)}` 里的 ENV 是函数调用，不是变量 `ENV`
	refs := scanString("${ENV(USERNAME)}")
	if len(refs) != 1 || refs[0].kind != refFunction || refs[0].name != "ENV" {
		t.Fatalf("函数表达式扫描错误: %+v", refs)
	}

	refs = scanString("$base_url/a?x=$token&y=${ENV(HOME)}")
	kinds := map[string]string{}
	for _, r := range refs {
		kind := "var"
		if r.kind == refFunction {
			kind = "fn"
		}
		kinds[r.name] = kind
	}
	if kinds["base_url"] != "var" || kinds["token"] != "var" || kinds["ENV"] != "fn" {
		t.Fatalf("混合扫描错误: %+v", kinds)
	}
}

func TestValidate_内置函数不告警_自定义函数告警(t *testing.T) {
	// ENV 是引擎内置函数，不需要 debugtalk.py
	ok := reqStep(1, "$base_url/get", map[string]any{"headers": map[string]any{"X-U": "${ENV(USERNAME)}"}})
	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{ok}}}})
	if hasCode(res, CodeCustomFuncUnsupported) {
		t.Fatalf("${ENV(...)} 是内置函数，不应告警: %+v", res.Issues)
	}

	// 自定义函数需要 debugtalk.py，M1 不支持
	bad := reqStep(1, "$base_url/get", map[string]any{"headers": map[string]any{"X-S": "${sign_request()}"}})
	res = Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{bad}}}})
	if !hasCodeAtLevel(res, CodeCustomFuncUnsupported, LevelWarning) {
		t.Fatalf("自定义函数应告警: %+v", res.Issues)
	}
}

// ---------------------------------------------------------------------------
// 结构类问题
// ---------------------------------------------------------------------------

func TestValidate_空步骤与缺URL(t *testing.T) {
	// 全部禁用
	disabled := reqStep(1, "$base_url/get", nil)
	disabled.Enabled = false
	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{disabled}}}})
	if !hasCodeAtLevel(res, CodeEmptySteps, LevelError) {
		t.Fatalf("全部步骤禁用应报 EMPTY_STEPS: %+v", res.Issues)
	}

	// 缺 url
	res = Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{
		Case:  newCase(),
		Steps: []model.TestStep{{Seq: 1, StepType: model.StepRequest, Name: "无地址", Request: anyOf(map[string]any{"method": "GET"}), Enabled: true}},
	}}})
	if !hasCodeAtLevel(res, CodeMissingURL, LevelError) {
		t.Fatalf("缺 url 应报 MISSING_URL: %+v", res.Issues)
	}
}

func TestValidate_非法断言方法与提取对象(t *testing.T) {
	step := reqStep(1, "$base_url/get", nil)
	step.Validate = jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "equals_loosely"}}
	step.Extract = jsonx.Slice[model.ExtractItem]{{Name: "x", Object: "database", Expression: "a.b"}}

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step}}}})
	if !hasCodeAtLevel(res, CodeInvalidAssertMethod, LevelError) {
		t.Errorf("非法校验方法应报错: %+v", res.Issues)
	}
	if !hasCodeAtLevel(res, CodeInvalidExtractObject, LevelError) {
		t.Errorf("非法提取对象应报错: %+v", res.Issues)
	}
}

func TestValidate_未命名提取项(t *testing.T) {
	step := reqStep(1, "$base_url/get", nil)
	step.Extract = jsonx.Slice[model.ExtractItem]{{Name: "  ", Object: model.ExtractBody, Expression: "a.b"}}

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step}}}})
	if !hasCodeAtLevel(res, CodeInvalidExtractName, LevelError) {
		t.Fatalf("未命名提取项应报错: %+v", res.Issues)
	}
}

func TestValidate_无断言只告警(t *testing.T) {
	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{
		Case:  newCase(),
		Steps: []model.TestStep{reqStep(1, "$base_url/get", nil)},
	}}})
	if !hasCodeAtLevel(res, CodeNoValidate, LevelWarning) {
		t.Fatalf("无断言应告警: %+v", res.Issues)
	}
	// 告警不应阻断
	if !res.OK() {
		t.Errorf("只有 warning 时 OK() 应为 true: %+v", res.Errors())
	}
}

func TestValidate_重名用例(t *testing.T) {
	a := newCase()
	b := newCase()
	b.Code = "tc_2"
	b.Name = a.Name // 同名

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: a}, {Case: b}}})
	if !hasCodeAtLevel(res, CodeDuplicateCaseName, LevelError) {
		t.Fatalf("config.name 重名必须报错（实测 F11）: %+v", res.Issues)
	}

	// 与项目内其他用例重名（保存单个用例的场景）
	res = Validate(&Input{
		Env:              newEnv(),
		Cases:            []CaseSpec{{Case: a}},
		SiblingCaseNames: map[string]string{a.Name: "tc_other"},
	})
	if !hasCodeAtLevel(res, CodeDuplicateCaseName, LevelError) {
		t.Fatalf("与同项目其他用例重名应报错: %+v", res.Issues)
	}
}

func TestValidate_缺名与seq重复(t *testing.T) {
	nameless := newCase()
	nameless.Name = "  "
	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: nameless, Steps: []model.TestStep{reqStep(1, "$base_url/get", nil)}}}})
	if !hasCodeAtLevel(res, CodeMissingCaseName, LevelError) {
		t.Fatalf("缺名应报错: %+v", res.Issues)
	}

	s1, s2 := reqStep(1, "$base_url/get", nil), reqStep(1, "$base_url/get", nil)
	res = Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{s1, s2}}}})
	if !hasCodeAtLevel(res, CodeDuplicateStepSeq, LevelError) {
		t.Fatalf("seq 重复应报错: %+v", res.Issues)
	}
}

func TestValidate_同名步骤只告警(t *testing.T) {
	s1, s2 := reqStep(1, "$base_url/get", nil), reqStep(2, "$base_url/get", nil)
	s1.Name, s2.Name = "登录", "登录"

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{s1, s2}}}})
	if !hasCodeAtLevel(res, CodeDuplicateStepName, LevelWarning) {
		t.Fatalf("同名步骤应告警: %+v", res.Issues)
	}
}

func TestValidate_M1不支持的步骤类型与hooks(t *testing.T) {
	apiStep := reqStep(1, "", nil)
	apiStep.StepType = model.StepAPI
	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{apiStep}}}})
	if !hasCodeAtLevel(res, CodeUnsupportedStepType, LevelError) {
		t.Errorf("M1 未开放的步骤类型应报错: %+v", res.Issues)
	}

	// hooks 需要自定义函数，M1 配了也不会生效 —— 必须报错而不是静默放行
	// （实测 A13：引擎对无效配置是静默忽略，平台若不拦，用户会以为配了）
	hookStep := reqStep(1, "$base_url/get", nil)
	hookStep.Hooks = anyOf(map[string]any{"setup": []any{"${before()}"}})
	res = Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{hookStep}}}})
	if !hasCodeAtLevel(res, CodeCustomFuncUnsupported, LevelError) {
		t.Fatalf("hooks 在 M1 应报错: %+v", res.Issues)
	}
}

func TestValidate_明文密钥告警(t *testing.T) {
	tc := newCase()
	tc.Config = anyOf(map[string]any{"variables": map[string]any{
		"api_token": "sk-abcdef123456", // 疑似硬编码
		"user":      "tester",          // 普通变量
		"password":  "$PWD_FROM_ENV",   // 引用了变量，不算硬编码
	}})

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: tc, Steps: []model.TestStep{reqStep(1, "$base_url/get", nil)}}}})

	count := 0
	for _, is := range res.Issues {
		if is.Code == CodePlaintextSecret {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("应恰好报 1 条硬编码密钥告警，实际 %d 条: %+v", count, res.Issues)
	}
}

func TestValidate_变量会覆盖时提示(t *testing.T) {
	step := reqStep(1, "$base_url/get", nil)
	step.Extract = jsonx.Slice[model.ExtractItem]{{Name: "tenant", Object: model.ExtractBody, Expression: "t"}}

	// tenant 已在环境里定义，步骤又提取同名变量 → 覆盖提示
	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step}}}})
	if !hasCodeAtLevel(res, CodeUndefinedVariable, LevelWarning) {
		t.Fatalf("提取项覆盖已有变量应告警: %+v", res.Issues)
	}
}

// ---------------------------------------------------------------------------
// 字段白名单（实测 A7 的直接产物）
// ---------------------------------------------------------------------------

func TestValidateYAMLFields_未知字段被拦下(t *testing.T) {
	yml := `config:
    name: 测试
    verify: false
    typo_key: 1
teststeps:
    -
        name: s1
        request:
            method: GET
            url: http://x/y
            statuc_code: 200
`
	issues := ValidateYAMLFields("testcases/a.yaml", yml)

	if len(issues) != 2 {
		t.Fatalf("应报 2 条未知字段，实际 %d: %+v", len(issues), issues)
	}
	for _, is := range issues {
		if is.Level != LevelError {
			t.Errorf("未知字段必须是 error 级（引擎会静默忽略，放行等于让用户踩坑）: %+v", is)
		}
		if is.Code != CodeUnknownField {
			t.Errorf("code 错误: %+v", is)
		}
	}
}

func TestValidateYAMLFields_hooks放错层级被拦下(t *testing.T) {
	// ⭐ 这是实测 A13 的直接编码：hooks 放 request 层会被引擎静默忽略。
	// 平台必须在写盘前拦住，否则用户会得到"配了前置钩子但从未执行"的用例。
	yml := `config:
    name: 测试
    verify: false
teststeps:
    -
        name: s1
        request:
            method: GET
            url: http://x/y
            setup_hooks:
                - "${before()}"
`
	issues := ValidateYAMLFields("testcases/a.yaml", yml)
	if len(issues) == 0 {
		t.Fatal("request 层的 setup_hooks 必须被拦下（实测 A13：引擎会静默忽略）")
	}
	if issues[0].Field != "setup_hooks" {
		t.Errorf("应明确指出是 setup_hooks：%+v", issues[0])
	}
}

func TestValidateYAMLFields_合法文件无告警(t *testing.T) {
	yml := `config:
    name: 测试
    variables:
        v: "1"
    parameters:
        p: [1, 2]
    verify: false
    export:
        - token
    weight: 1
teststeps:
    -
        name: s1
        variables:
            local: "x"
        request:
            method: POST
            url: http://x/y
            params:
                a: "1"
            headers:
                Content-Type: application/json
            json:
                k: v
            timeout: 3
        extract:
            token: body.args.token
        validate:
            - eq: [status_code, 200]
`
	if issues := ValidateYAMLFields("testcases/a.yaml", yml); len(issues) != 0 {
		t.Fatalf("合法文件不应报问题: %+v", issues)
	}
}

func TestValidateYAMLFields_废弃字段只告警(t *testing.T) {
	yml := `config:
    name: 测试
    base_url: http://x
teststeps:
    -
        name: s1
        request:
            method: GET
            url: http://x/y
`
	issues := ValidateYAMLFields("testcases/a.yaml", yml)
	if len(issues) != 1 {
		t.Fatalf("应报 1 条废弃字段告警: %+v", issues)
	}
	if issues[0].Level != LevelWarning {
		t.Errorf("废弃字段不应阻断: %+v", issues[0])
	}
}

func TestValidateYAMLFields_非法YAML(t *testing.T) {
	issues := ValidateYAMLFields("testcases/a.yaml", "config: [\n")
	if len(issues) != 1 || issues[0].Level != LevelError {
		t.Fatalf("非法 YAML 应报 error: %+v", issues)
	}
}

// TestValidateYAMLFields_编译器产物必须全绿 是最有价值的一条集成测试。
//
// 它把「编译器实际渲染出来的东西」喂给字段白名单：
// 如果编译器把某个字段放错层级（比如把 timeout 写到 teststep 层），
// 引擎不会报错、单元测试也看不出来，但这里会红。
func TestValidateYAMLFields_编译器产物必须全绿(t *testing.T) {
	tc := &model.TestCase{Code: "tc_login", Name: "登录流程"}
	tc.ID = 10
	tc.Config = anyOf(map[string]any{
		"variables": map[string]any{"user": "tester"},
		"headers":   map[string]any{"X-Case": "1"},
		"export":    []any{"token"},
	})

	steps := []model.TestStep{
		{
			Seq: 1, StepType: model.StepRequest, Name: "步骤1", Enabled: true,
			Request: anyOf(map[string]any{
				"method": "POST", "url": "$base_url/post", "body_type": model.BodyTypeJSON,
				"body":    map[string]any{"u": "$user"},
				"params":  map[string]any{"q": "1"},
				"timeout": 5,
			}),
			Extract:  jsonx.Slice[model.ExtractItem]{{Name: "token", Object: model.ExtractBody, Expression: "json.t"}},
			Validate: jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq", Expect: float64(200)}},
		},
		{
			Seq: 2, StepType: model.StepRequest, Name: "步骤2", Enabled: true,
			Request: anyOf(map[string]any{
				"method": "POST", "url": "$base_url/post", "body_type": model.BodyTypeForm,
				"body": map[string]any{"t": "$token"},
			}),
			Validate: jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq", Expect: float64(200)}},
		},
	}

	out, err := compiler.Render(&compiler.Input{
		Project: &model.Project{Code: "demo"},
		Env:     newEnv(),
		Cases:   []compiler.CaseSpec{{Case: tc, Steps: steps}},
	})
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}

	for _, c := range out.Cases {
		if issues := ValidateYAMLFields(c.FileName, c.YAML); len(issues) != 0 {
			t.Fatalf("编译器产物未通过字段白名单（说明字段层级写错了，引擎会静默忽略）:\n%s\n%+v",
				c.YAML, issues)
		}
	}
}

// TestValidate_编译产物语义也要干净 覆盖同一条链路的语义校验。
func TestValidate_编译产物语义也要干净(t *testing.T) {
	tc := newCase()
	step1 := reqStep(1, "$base_url/post", map[string]any{
		"method": "POST", "body_type": model.BodyTypeJSON,
		"body": map[string]any{"u": "$tenant"}, // 来自环境变量
	})
	step1.Validate = jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq", Expect: float64(200)}}
	step1.Extract = jsonx.Slice[model.ExtractItem]{{Name: "token", Object: model.ExtractBody, Expression: "json.t"}}

	step2 := reqStep(2, "$base_url/get", map[string]any{"params": map[string]any{"t": "$token"}})
	step2.Validate = jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq", Expect: float64(200)}}

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: tc, Steps: []model.TestStep{step1, step2}}}})
	if !res.OK() {
		t.Fatalf("这条用例应当完全干净，实际报错: %+v", res.Errors())
	}
}

// ---------------------------------------------------------------------------
// 引擎补结尾斜杠（实测 A1）
// ---------------------------------------------------------------------------

// assertSlashWarning 断言 TRAILING_SLASH_ADDED 是否存在。
func assertSlashWarning(t *testing.T, name string, want bool, extra map[string]any) {
	t.Helper()
	res := Validate(&Input{
		Env: newEnv(),
		Cases: []CaseSpec{{
			Case:  newCase(),
			Steps: []model.TestStep{reqStep(1, "$base_url/get", extra)},
		}},
	})
	got := hasCode(res, CodeTrailingSlashAdded)
	if got != want {
		t.Fatalf("%s: 期望 TRAILING_SLASH_ADDED=%v，实际 %v（issues=%v）", name, want, got, codes(res))
	}
	// 无论命中与否，这个提示都**不能**升级成 error：
	// 绝大多数服务对结尾斜杠不敏感，做成 error 会挡住大量正常用例。
	if hasCodeAtLevel(res, CodeTrailingSlashAdded, LevelError) {
		t.Fatalf("%s: TRAILING_SLASH_ADDED 不应是 error 级", name)
	}
}

// TestValidate_引擎补结尾斜杠要被预警 覆盖实测 A1 的全部触发条件。
func TestValidate_引擎补结尾斜杠要被预警(t *testing.T) {
	// 1. 没有 params —— 引擎会补成 /get/，必须提示
	assertSlashWarning(t, "无 params", true, nil)
	// 2. params 是空 map —— 实测同样会补，这是最容易误以为「写了就没事」的一种
	assertSlashWarning(t, "params 为空 map", true, map[string]any{"params": map[string]any{}})

	// 3. 有非空 params → 引擎不补
	assertSlashWarning(t, "params 非空", false, map[string]any{"params": map[string]any{"a": 1}})
	// 4. URL 自带 query → 引擎不补
	noParams := Validate(&Input{
		Env:   newEnv(),
		Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{reqStep(1, "$base_url/get?x=1", nil)}}},
	})
	if hasCode(noParams, CodeTrailingSlashAdded) {
		t.Fatalf("URL 自带 query 时不应提示，实际: %v", codes(noParams))
	}
	// 5. 本来就是斜杠结尾 → 补不补都一样，不必提示
	slash := Validate(&Input{
		Env:   newEnv(),
		Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{reqStep(1, "$base_url/get/", nil)}}},
	})
	if hasCode(slash, CodeTrailingSlashAdded) {
		t.Fatalf("URL 已以斜杠结尾时不应提示，实际: %v", codes(slash))
	}
}

// TestValidate_结尾斜杠提示与方法无关 实测确认 POST 也会被补斜杠。
func TestValidate_结尾斜杠提示与方法无关(t *testing.T) {
	step := reqStep(1, "$base_url/anything/xyz", map[string]any{
		"method": "POST", "body_type": model.BodyTypeJSON,
		"body": map[string]any{"a": 1},
	})
	step.Validate = jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq", Expect: float64(200)}}

	res := Validate(&Input{Env: newEnv(), Cases: []CaseSpec{{Case: newCase(), Steps: []model.TestStep{step}}}})
	if !hasCode(res, CodeTrailingSlashAdded) {
		t.Fatalf("带 JSON body 的 POST 也会被引擎补斜杠，应提示；实际: %v", codes(res))
	}
}
