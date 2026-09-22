package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// 夹具
// ---------------------------------------------------------------------------

func anyOf(v any) jsonx.Any { return jsonx.Any{Val: v} }

func newProject() *model.Project {
	p := &model.Project{Code: "demo", Name: "演示项目"}
	p.ID = 1
	return p
}

// newEnv 刻意开满所有会影响渲染的字段：base_url、自定义变量、全局头。
func newEnv() *model.Environment {
	e := &model.Environment{
		ProjectID:     1,
		Name:          "本地联调",
		BaseURL:       "http://127.0.0.1:8899",
		Environs:      jsonx.Map{"tenant": "acme", "flag": true, "n": float64(3)},
		GlobalHeaders: jsonx.Map{"X-Trace-Id": "trace-001"},
		VerifySSL:     false,
		IsDefault:     true,
	}
	e.ID = 1
	return e
}

// newCase 构造一个典型用例：POST + json body + extract + 断言。
func newCase() (*model.TestCase, []model.TestStep) {
	tc := &model.TestCase{
		ProjectID: 1,
		Code:      "tc_login",
		Name:      "登录流程",
		Priority:  model.PriorityP0,
		Status:    model.CaseStatusActive,
	}
	tc.ID = 10

	step1 := model.TestStep{
		CaseID:   10,
		Seq:      1,
		StepType: model.StepRequest,
		Name:     "步骤1 登录并取 token",
		Request: anyOf(map[string]any{
			"method":    "POST",
			"url":       "$base_url/post",
			"body":      map[string]any{"username": "tester", "age": float64(18)},
			"body_type": model.BodyTypeJSON,
		}),
		Extract: jsonx.Slice[model.ExtractItem]{
			{Name: "token", Object: model.ExtractBody, Expression: "json.token"},
			{Name: "code", Object: model.ExtractStatusCode},
		},
		Validate: jsonx.Slice[model.AssertItem]{
			{Check: "status_code", Assert: "eq", Expect: float64(200)},
			{Check: "body.json.username", Assert: "eq", Expect: "tester"},
		},
		Enabled: true,
	}

	step2 := model.TestStep{
		CaseID:   10,
		Seq:      2,
		StepType: model.StepRequest,
		Name:     "步骤2 用 token",
		Request: anyOf(map[string]any{
			"method": "GET",
			"url":    "$base_url/get",
			"params": map[string]any{"token": "$token"},
		}),
		Validate: jsonx.Slice[model.AssertItem]{
			{Check: "status_code", Assert: "eq", Expect: float64(200)},
		},
		Enabled: true,
	}

	return tc, []model.TestStep{step1, step2}
}

func renderOne(t *testing.T, tc *model.TestCase, steps []model.TestStep) *Output {
	t.Helper()
	out, err := Render(&Input{
		Project: newProject(),
		Env:     newEnv(),
		Cases:   []CaseSpec{{Case: tc, Steps: steps}},
	})
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	return out
}

// ---------------------------------------------------------------------------
// .env
// ---------------------------------------------------------------------------

func TestRenderEnv_稳定且转义正确(t *testing.T) {
	got := RenderEnv(newEnv())

	if !strings.Contains(got, "base_url=http://127.0.0.1:8899\n") {
		t.Fatalf(".env 缺少 base_url:\n%s", got)
	}
	// 变量按 key 排序，保证两次渲染逐字节一致
	if !strings.Contains(got, "flag=true\n") {
		t.Errorf("布尔变量未渲染为 true:\n%s", got)
	}
	// float64(3) 不能渲染成 3.0
	if !strings.Contains(got, "n=3\n") {
		t.Errorf("整数型变量被渲染成浮点:\n%s", got)
	}
	if strings.Index(got, "flag=") > strings.Index(got, "n=") {
		t.Errorf("变量未按 key 排序:\n%s", got)
	}

	if again := RenderEnv(newEnv()); again != got {
		t.Errorf("两次渲染结果不一致，破坏可 diff 性:\n%q\n%q", got, again)
	}
}

func TestRenderEnv_含空格与井号的值必须加引号(t *testing.T) {
	e := newEnv()
	e.Environs = jsonx.Map{"note": "hello world", "sharp": "a#b"}
	got := RenderEnv(e)

	if !strings.Contains(got, `note="hello world"`) {
		t.Errorf("含空格的值未加引号，dotenv 会截断:\n%s", got)
	}
	// 不加引号时 `#` 之后会被当成注释，值就丢了
	if !strings.Contains(got, `sharp="a#b"`) {
		t.Errorf("含 # 的值未加引号:\n%s", got)
	}
}

func TestRenderEnv_环境为空时给可用的默认值(t *testing.T) {
	got := RenderEnv(nil)
	if !strings.Contains(got, "base_url=") {
		t.Fatalf("无环境时未产出 base_url:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

func TestRender_configName唯一性(t *testing.T) {
	tc1, steps := newCase()
	tc2, _ := newCase()
	tc2.Code = "tc_other"
	tc2.Name = tc1.Name // 重名

	_, err := Render(&Input{
		Project: newProject(),
		Env:     newEnv(),
		Cases:   []CaseSpec{{Case: tc1, Steps: steps}, {Case: tc2}},
	})
	if err == nil {
		t.Fatal("重名用例应当报错（实测 F11：引擎以 config.name 为唯一标识）")
	}
	if !strings.Contains(err.Error(), "重复") {
		t.Fatalf("报错信息未说明原因: %v", err)
	}
}

func TestRender_用例缺名必须报错(t *testing.T) {
	tc, steps := newCase()
	tc.Name = "   "

	if _, err := Render(&Input{
		Project: newProject(),
		Env:     newEnv(),
		Cases:   []CaseSpec{{Case: tc, Steps: steps}},
	}); err == nil {
		t.Fatal("用例缺名应当报错")
	}
}

func TestRender_verify默认取环境(t *testing.T) {
	tc, steps := newCase()
	// 用例 config 里没有 verify 键 → 必须沿用环境设置
	tc.Config = anyOf(map[string]any{"variables": map[string]any{"k": "v"}})

	out := renderOne(t, tc, steps)
	if !strings.Contains(out.Cases[0].YAML, "verify: false") {
		t.Fatalf("verify 未取环境值:\n%s", out.Cases[0].YAML)
	}

	// 环境开启校验后，用例未显式声明也必须跟随
	env := newEnv()
	env.VerifySSL = true
	out2, err := Render(&Input{Project: newProject(), Env: env, Cases: []CaseSpec{{Case: tc, Steps: steps}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.Cases[0].YAML, "verify: true") {
		t.Fatalf("环境 verify_ssl=true 未传递到用例:\n%s", out2.Cases[0].YAML)
	}
}

func TestRender_config显式verify可覆盖环境(t *testing.T) {
	tc, steps := newCase()
	tc.Config = anyOf(map[string]any{"verify": false})

	env := newEnv()
	env.VerifySSL = true

	out, err := Render(&Input{Project: newProject(), Env: env, Cases: []CaseSpec{{Case: tc, Steps: steps}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Cases[0].YAML, "verify: false") {
		t.Fatalf("用例显式 verify 未生效（前端表单一旦带上该键就会静默失效）:\n%s", out.Cases[0].YAML)
	}
}

// ---------------------------------------------------------------------------
// request 渲染
// ---------------------------------------------------------------------------

func TestRender_json体不加ContentType也让引擎自己补(t *testing.T) {
	tc, steps := newCase()
	out := renderOne(t, tc, steps)
	yml := out.Cases[0].YAML

	if !strings.Contains(yml, "json:") {
		t.Fatalf("json 体未渲染到 json 字段:\n%s", yml)
	}
	if strings.Contains(yml, "body:") {
		t.Fatalf("渲染出了引擎不认识的 request.body 字段（实测 F12：未知字段会被静默忽略）:\n%s", yml)
	}
}

func TestRender_form体必须显式补ContentType(t *testing.T) {
	tc, steps := newCase()
	steps[0].Request = anyOf(map[string]any{
		"method":    "POST",
		"url":       "$base_url/post",
		"body":      map[string]any{"username": "tester", "password": "123"},
		"body_type": model.BodyTypeForm,
	})

	yml := renderOne(t, tc, steps).Cases[0].YAML

	if !strings.Contains(yml, "data:") {
		t.Fatalf("form 体未渲染到 data 字段:\n%s", yml)
	}
	// 不给这个头，引擎会把 map 序列化成 JSON 而不是表单编码
	if !strings.Contains(yml, "application/x-www-form-urlencoded") {
		t.Fatalf("form 体缺少 Content-Type，引擎会发成 JSON:\n%s", yml)
	}
}

func TestRender_raw体渲染到data字段(t *testing.T) {
	tc, steps := newCase()
	steps[0].Request = anyOf(map[string]any{
		"method":    "POST",
		"url":       "$base_url/post",
		"body":      "username=tester&password=123",
		"body_type": model.BodyTypeRaw,
	})

	yml := renderOne(t, tc, steps).Cases[0].YAML
	if !strings.Contains(yml, "data:") {
		t.Fatalf("raw 体未渲染到 data 字段:\n%s", yml)
	}
}

func TestRender_三层请求头合并且大小写不敏感(t *testing.T) {
	tc, steps := newCase()
	// 用例级头（来自 config）
	tc.Config = anyOf(map[string]any{
		"headers": map[string]any{"X-Case": "case-level"},
	})
	env := newEnv()
	env.GlobalHeaders = jsonx.Map{"content-type": "text/plain", "X-Env": "1"}
	steps[0].Request = anyOf(map[string]any{
		"method": "GET",
		"url":    "$base_url/get",
		"headers": map[string]any{
			"Content-Type": "application/json",
		},
	})

	out, err := Render(&Input{Project: newProject(), Env: env, Cases: []CaseSpec{{Case: tc, Steps: steps}}})
	if err != nil {
		t.Fatal(err)
	}
	yml := out.Cases[0].YAML

	var doc struct {
		Config struct {
			Headers map[string]any `yaml:"headers"`
		} `yaml:"config"`
		TestSteps []struct {
			Request struct {
				Headers map[string]string `yaml:"headers"`
			} `yaml:"request"`
		} `yaml:"teststeps"`
	}
	if err := yaml.Unmarshal([]byte(yml), &doc); err != nil {
		t.Fatalf("渲染结果不是合法 YAML: %v\n%s", err, yml)
	}

	// M1 不写 config.headers：引擎合并两层时大小写敏感，
	// 头名在两层写法不同就会真的发出重复请求头
	if len(doc.Config.Headers) != 0 {
		t.Fatalf("config.headers 应为空，实际 %#v:\n%s", doc.Config.Headers, yml)
	}
	if len(doc.TestSteps) != 2 {
		t.Fatalf("步骤数错误: %d", len(doc.TestSteps))
	}

	ciType := map[string]string{}
	for i, st := range doc.TestSteps {
		seen := map[string]string{} // 小写键 → 原始键
		values := map[string]string{}
		for k, v := range st.Request.Headers {
			lk := strings.ToLower(k)
			if prev, dup := seen[lk]; dup {
				t.Fatalf("步骤 %d 的请求头 %q 与 %q 大小写重复（引擎会发两个头）:\n%s",
					i+1, prev, k, yml)
			}
			seen[lk] = k
			values[lk] = v
		}

		// 环境级与用例级头必须下发到每个步骤
		if values["x-env"] != "1" {
			t.Errorf("步骤 %d 缺少环境全局头:\n%s", i+1, yml)
		}
		if values["x-case"] != "case-level" {
			t.Errorf("步骤 %d 缺少用例级头:\n%s", i+1, yml)
		}
		ciType[seen["content-type"]] = values["content-type"]
	}

	// 步骤1 显式声明了 Content-Type，应覆盖环境里的 text/plain；
	// 步骤2 没声明，应继承环境值。两个步骤互不干扰。
	if got := ciType["Content-Type"]; got != "application/json" {
		t.Errorf("步骤级请求头未覆盖环境级: Content-Type=%q:\n%s", got, yml)
	}
	if got := ciType["content-type"]; got != "text/plain" {
		t.Errorf("未声明请求头的步骤应继承环境值: content-type=%q:\n%s", got, yml)
	}
}

func TestRender_环境全局头不能被用例污染(t *testing.T) {
	tc, steps := newCase()
	env := newEnv()

	in := &Input{Project: newProject(), Env: env, Cases: []CaseSpec{{Case: tc, Steps: steps}}}
	if _, err := Render(in); err != nil {
		t.Fatal(err)
	}
	before := len(env.GlobalHeaders)

	// 再渲染一次，合并过一次头不应该改变环境对象本身
	if _, err := Render(in); err != nil {
		t.Fatal(err)
	}
	if len(env.GlobalHeaders) != before {
		t.Fatalf("环境全局头被渲染过程修改了：%d → %d", before, len(env.GlobalHeaders))
	}
}

func TestRender_请求头大小写不敏感合并(t *testing.T) {
	base := map[string]any{"content-type": "text/plain", "A": "1"}
	got := mergeStringMap(base, map[string]any{"Content-Type": "application/json"})

	if len(got) != 2 {
		t.Fatalf("大小写不敏感去重失败: %#v", got)
	}
	if got["Content-Type"] != "application/json" {
		t.Fatalf("步骤级头未生效: %#v", got)
	}
	if _, dup := got["content-type"]; dup {
		t.Fatalf("保留了重复键: %#v", got)
	}
}

// ---------------------------------------------------------------------------
// 断言渲染
// ---------------------------------------------------------------------------

func TestRender_断言必须带校验方法(t *testing.T) {
	tc, steps := newCase()
	steps[0].Validate = jsonx.Slice[model.AssertItem]{
		{Check: "status_code", Expect: float64(200)}, // 缺 Assert
	}
	if _, err := Render(&Input{
		Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}},
	}); err == nil {
		t.Fatal("缺少校验方法的断言应当被拒绝，否则会产出引擎无法解析的文件")
	}
}

func TestRender_断言整数期望值不能变成浮点(t *testing.T) {
	tc, steps := newCase()
	yml := renderOne(t, tc, steps).Cases[0].YAML

	// 反解回来验证类型：float64(200) 若原样渲染成 200.0，
	// 引擎内部 reflect.DeepEqual(int(200), float64(200)) 为 false → 断言假失败。
	var doc struct {
		TestSteps []struct {
			Validate []map[string][]any `yaml:"validate"`
		} `yaml:"teststeps"`
	}
	if err := yaml.Unmarshal([]byte(yml), &doc); err != nil {
		t.Fatalf("渲染结果不是合法 YAML: %v\n%s", err, yml)
	}
	got := doc.TestSteps[0].Validate[0]["eq"][1]
	if _, ok := got.(int); !ok {
		t.Fatalf("状态码期望值类型是 %T，必须是 int（YAML 原文见下）\n%s", got, yml)
	}
	if got.(int) != 200 {
		t.Fatalf("期望值错误: %v", got)
	}

	// 字符串期望值不能被当成数字
	if s := doc.TestSteps[0].Validate[1]["eq"][1]; s != "tester" {
		t.Fatalf("字符串期望值被破坏: %#v", s)
	}
}

func TestRender_断言为流式一行写法(t *testing.T) {
	tc, steps := newCase()
	yml := renderOne(t, tc, steps).Cases[0].YAML

	if !strings.Contains(yml, "- eq: [status_code, 200]") {
		t.Fatalf("断言未按引擎惯例渲染成流式序列:\n%s", yml)
	}
}

func TestNormalizeScalar(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{float64(200), int64(200)},
		{float64(1.5), 1.5},
		{float32(3), int64(3)},
		{"s", "s"},
		{true, true},
		{nil, nil},
	}
	for _, c := range cases {
		if got := normalizeScalar(c.in); got != c.want {
			t.Errorf("normalizeScalar(%#v) = %#v, want %#v", c.in, got, c.want)
		}
	}

	// 嵌套结构也要归一化，否则 body 里的整数会变成浮点
	nested := normalizeScalar(map[string]any{"age": float64(18), "tags": []any{float64(1)}})
	m := nested.(map[string]any)
	if _, ok := m["age"].(int64); !ok {
		t.Errorf("嵌套 map 未归一化: %#v", m["age"])
	}
	if v, ok := m["tags"].([]any)[0].(int64); !ok {
		t.Errorf("嵌套数组未归一化: %#v", v)
	}
}

// ---------------------------------------------------------------------------
// extract / hooks / 步骤过滤
// ---------------------------------------------------------------------------

func TestRender_extract表达式(t *testing.T) {
	tc, steps := newCase()
	yml := renderOne(t, tc, steps).Cases[0].YAML

	// v4 的写法是 `<object>.<path>`
	if !strings.Contains(yml, "token: body.json.token") {
		t.Fatalf("body 提取表达式渲染错误:\n%s", yml)
	}
	// status_code / proto 是标量对象，没有子路径
	if !strings.Contains(yml, "code: status_code") {
		t.Fatalf("status_code 提取不应带子路径:\n%s", yml)
	}
}

func TestRenderExtractExpr(t *testing.T) {
	cases := []struct {
		it   model.ExtractItem
		want string
	}{
		{model.ExtractItem{Object: "body", Expression: "args.token"}, "body.args.token"},
		{model.ExtractItem{Object: "body", Expression: ".args.token"}, "body.args.token"},
		{model.ExtractItem{Object: "status_code"}, "status_code"},
		{model.ExtractItem{Object: "status_code", Expression: "ignored"}, "status_code"},
		{model.ExtractItem{Object: "proto"}, "proto"},
		{model.ExtractItem{Object: "headers", Expression: "X-A"}, "headers.X-A"},
	}
	for _, c := range cases {
		if got := renderExtractExpr(c.it); got != c.want {
			t.Errorf("renderExtractExpr(%#v) = %q, want %q", c.it, got, c.want)
		}
	}
}

func TestRender_hooks必须渲染在teststep层(t *testing.T) {
	tc, steps := newCase()
	steps[0].Hooks = anyOf(map[string]any{
		"setup":    []any{"${before()}"},
		"teardown": []any{"${after()}"},
	})

	yml := renderOne(t, tc, steps).Cases[0].YAML

	// 实测 F16：放在 request 层会被引擎静默忽略，必须落在 teststep 层
	reqIdx := strings.Index(yml, "request:")
	hookIdx := strings.Index(yml, "setup_hooks:")
	if hookIdx < 0 {
		t.Fatalf("hooks 未渲染:\n%s", yml)
	}
	if hookIdx > reqIdx {
		t.Fatalf("setup_hooks 落在 request 之后，必须与 request 同级（实测 F15/F16）:\n%s", yml)
	}
	if !strings.Contains(yml, "${before()}") {
		t.Fatalf("hook 内容丢失:\n%s", yml)
	}
}

func TestRender_禁用步骤被编译期剔除并按seq排序(t *testing.T) {
	tc, steps := newCase()
	// 打乱顺序 + 禁用中间一步
	disabled := steps[1]
	disabled.Seq = 2
	disabled.Enabled = false

	steps = []model.TestStep{
		{CaseID: 10, Seq: 3, StepType: model.StepRequest, Name: "步骤3", Enabled: true,
			Request: anyOf(map[string]any{"method": "GET", "url": "$base_url/get"})},
		steps[0],
		disabled,
	}

	out := renderOne(t, tc, steps)
	if len(out.Cases[0].Steps) != 2 {
		t.Fatalf("应保留 2 个启用步骤，实际 %d", len(out.Cases[0].Steps))
	}
	// 引擎按文件内的书写顺序执行，因此必须按 seq 升序写入
	if out.Cases[0].Steps[0].Seq != 1 || out.Cases[0].Steps[1].Seq != 3 {
		t.Fatalf("步骤未按 seq 升序排列: %#v", out.Cases[0].Steps)
	}
	if strings.Contains(out.Cases[0].YAML, "步骤2") {
		t.Fatalf("被禁用的步骤仍出现在 YAML 中:\n%s", out.Cases[0].YAML)
	}
}

func TestRender_没有启用步骤时报错(t *testing.T) {
	tc, steps := newCase()
	steps[0].Enabled = false
	steps[1].Enabled = false

	if _, err := Render(&Input{
		Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}},
	}); err == nil {
		t.Fatal("全部步骤被禁用时应当报错，而不是产出空用例")
	}
}

func TestRender_M1不开放的步骤类型要显式拒绝(t *testing.T) {
	tc, steps := newCase()
	steps[0].StepType = model.StepAPI

	if _, err := Render(&Input{
		Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}},
	}); err == nil {
		t.Fatal("M1 尚未开放 api 步骤，应当显式拒绝而不是产出引擎未知的文件")
	}
}

func TestRender_请求缺少url要报错(t *testing.T) {
	tc, steps := newCase()
	steps[0].Request = anyOf(map[string]any{"method": "GET"})

	if _, err := Render(&Input{
		Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}},
	}); err == nil {
		t.Fatal("请求步骤缺少 url 应当报错")
	}
}

// ---------------------------------------------------------------------------
// 文件名与步骤声明
// ---------------------------------------------------------------------------

func TestRender_文件名与步骤声明(t *testing.T) {
	tc, steps := newCase()
	out := renderOne(t, tc, steps)

	c := out.Cases[0]
	if c.FileName != "testcases/tc_login.yaml" {
		t.Errorf("文件名错误: %s", c.FileName)
	}
	if c.ConfigName != "登录流程" || c.CaseID != 10 {
		t.Errorf("用例标识错误: %+v", c)
	}
	if out.ExpectedCaseCount() != 1 {
		t.Errorf("用例数对账基数错误: %d", out.ExpectedCaseCount())
	}

	// 步骤声明要能覆盖到 YAML 里的全部步骤，否则解析器对不齐事件
	if len(c.Steps) != 2 {
		t.Fatalf("步骤声明数 %d != 2", len(c.Steps))
	}
	if c.Steps[0].Method != "POST" || c.Steps[0].URL != "$base_url/post" {
		t.Errorf("步骤声明的方法/URL 错误: %+v", c.Steps[0])
	}
	if len(c.Steps[0].Assertions) != 2 {
		t.Errorf("断言声明丢失，断言失败时无法重建明细: %+v", c.Steps[0])
	}
}

func TestCompile_落盘内容与渲染一致(t *testing.T) {
	tc, steps := newCase()
	root := t.TempDir()

	out, err := Compile(&Input{
		Project: newProject(),
		Env:     newEnv(),
		Cases:   []CaseSpec{{Case: tc, Steps: steps}},
	}, root)
	if err != nil {
		t.Fatal(err)
	}

	// 「编辑器里看到的」必须与「引擎实际执行的」逐字节一致
	disk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Cases[0].FileName)))
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != out.Cases[0].YAML {
		t.Fatal("落盘内容与预览内容不一致")
	}

	envDisk, err := os.ReadFile(filepath.Join(root, FileEnv))
	if err != nil {
		t.Fatal(err)
	}
	if string(envDisk) != out.EnvText {
		t.Fatal(".env 落盘内容与渲染内容不一致")
	}
}

// ---------------------------------------------------------------------------
// 端到端：把编译器产物交给真引擎执行
//
// 默认跳过（需要 hrp 二进制 + 本地 echo 服务在 8899 监听）。
// 开启方式：
//
//	source scripts/dev-env.sh
//	python testdata/local-echo/echo_server.py &
//	HRP_PLATFORM_E2E=1 go test ./internal/compiler/ -run E2E -v
//
// 这个测试是「编译产物是否真的能被引擎吃下去」的唯一权威验证：
// 单元测试只能证明 YAML 合法，证明不了引擎认。
// ---------------------------------------------------------------------------

func TestE2E_编译产物可被引擎执行(t *testing.T) {
	if os.Getenv("HRP_PLATFORM_E2E") != "1" {
		t.Skip("需要 HRP_PLATFORM_E2E=1 且本地 echo 服务在 8899 运行")
	}
	hrp := os.Getenv("HRP_BINARY_PATH")
	if hrp == "" {
		t.Skip("未设置 HRP_BINARY_PATH")
	}

	tc, steps := newCase()
	root := t.TempDir()

	// 覆盖成对本地 echo 服务真实可用的两条用例
	tc.Name = "E2E 编译产物验证"
	steps[0] = model.TestStep{
		CaseID: 10, Seq: 1, StepType: model.StepRequest, Name: "步骤1 POST json 并提取",
		Request: anyOf(map[string]any{
			"method":    "POST",
			"url":       "$base_url/post",
			"body":      map[string]any{"username": "tester", "age": float64(18)},
			"body_type": model.BodyTypeJSON,
		}),
		Extract: jsonx.Slice[model.ExtractItem]{
			{Name: "echoed", Object: model.ExtractBody, Expression: "json.username"},
		},
		Validate: jsonx.Slice[model.AssertItem]{
			{Check: "status_code", Assert: "eq", Expect: float64(200)},
			{Check: "body.json.username", Assert: "eq", Expect: "tester"},
			{Check: "body.json.age", Assert: "eq", Expect: float64(18)},
		},
		Enabled: true,
	}
	steps[1] = model.TestStep{
		CaseID: 10, Seq: 2, StepType: model.StepRequest, Name: "步骤2 form 表单",
		Request: anyOf(map[string]any{
			"method":    "POST",
			"url":       "$base_url/post",
			"body":      map[string]any{"username": "tester", "password": "123"},
			"body_type": model.BodyTypeForm,
		}),
		Validate: jsonx.Slice[model.AssertItem]{
			{Check: "status_code", Assert: "eq", Expect: float64(200)},
			// 只有正确 URL 编码后，服务端才会把它放进 form 而不是 data
			{Check: "body.form.username", Assert: "eq", Expect: "tester"},
		},
		Enabled: true,
	}

	env := newEnv()
	const m1CaseName = "E2E 编译产物验证"
	_ = m1CaseName

	if _, err := Compile(&Input{
		Project: newProject(),
		Env:     env,
		Cases:   []CaseSpec{{Case: tc, Steps: steps}},
	}, root); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(hrp, "run", "testcases", "--log-json")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("引擎超时未退出\nstderr:\n%s", stderr.String())
	}

	if cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("引擎退出码 %d，编译产物不被引擎接受。\nstderr:\n%s\nstdout:\n%s",
			cmd.ProcessState.ExitCode(), tail(stderr.String(), 40), tail(stdout.String(), 30))
	}
}

// tail 只保留末尾 n 行，避免失败时刷屏。
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
