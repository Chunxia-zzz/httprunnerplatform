// Package compiler 把数据库里的结构化元数据编译成 hrp 工作区。
//
// 设计原则（方案 4.2）：**DB 是唯一真源，YAML 是可再生的编译产物**。
// 编译器是单向的：只做 DB → YAML，永不反向解析。
//
// 两条由引擎实测结论驱动的硬性约束：
//
//   - **`config.name` 必须全局唯一**：引擎的 summary.json 以 config.name 作唯一标识，
//     重名会导致结果无法区分（实测 F11）。编译器在渲染前强制校验。
//
//   - **请求体字段的写法由 body_type 决定**（实测确认）：
//     json  → `json:`（引擎自动补 Content-Type: application/json; charset=utf-8）
//     form  → `data:` + 显式 Content-Type: application/x-www-form-urlencoded
//     （不给 Content-Type 时引擎会把 map 序列化成 JSON，而不是表单）
//     raw   → `data: "<string>"`（Content-Type 由调用方在 headers 里自定）
package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// 工作区内的固定子路径。与方案 4.3 的目录规范一致。
const (
	DirTestcases = "testcases"
	FileEnv      = ".env"
)

// CaseSpec 是一次编译中要处理的单个用例。
type CaseSpec struct {
	Case  *model.TestCase
	Steps []model.TestStep
}

// Input 是一次编译的输入。
type Input struct {
	Project *model.Project
	Env     *model.Environment
	Cases   []CaseSpec
}

// CompiledCase 是单个用例的编译产物。
type CompiledCase struct {
	CaseID     uint64
	Code       string
	ConfigName string
	// FileName 是工作区内的相对路径，形如 testcases/tc_login.yaml
	FileName string
	// YAML 是最终要写盘、也是编辑器要展示的内容。
	// **契约：这份内容必须与实际执行的文件逐字节一致。**
	YAML string
	// Steps 是给解析器用的声明快照（顺序与 YAML 中的 teststeps 一致）。
	Steps []StepDecl
}

// StepDecl 是编译期确定的步骤声明，供解析器对齐引擎事件。
type StepDecl struct {
	Seq        int
	Name       string
	StepType   string
	Method     string
	URL        string
	Assertions []model.AssertItem
}

// Output 是一次编译的整体产物。
type Output struct {
	EnvText string
	Cases   []CompiledCase
}

// ExpectedCaseCount 返回本次编译产出的用例数，用于执行后的用例数对账（方案 6.4）。
func (o *Output) ExpectedCaseCount() int { return len(o.Cases) }

// Compile 渲染并把产物写入工作区目录。
//
// workspaceRoot 是该项目的持久层目录（{root}/workspaces/{project_code}）。
func Compile(in *Input, workspaceRoot string) (*Output, error) {
	out, err := Render(in)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(workspaceRoot, DirTestcases), 0o755); err != nil {
		return nil, fmt.Errorf("创建工作区目录失败: %w", err)
	}

	// .env
	if err := os.WriteFile(filepath.Join(workspaceRoot, FileEnv), []byte(out.EnvText), 0o644); err != nil {
		return nil, fmt.Errorf("写入 .env 失败: %w", err)
	}

	for i := range out.Cases {
		c := &out.Cases[i]
		abs := filepath.Join(workspaceRoot, filepath.FromSlash(c.FileName))
		if err := os.WriteFile(abs, []byte(c.YAML), 0o644); err != nil {
			return nil, fmt.Errorf("写入用例文件 %s 失败: %w", c.FileName, err)
		}
	}
	return out, nil
}

// Render 只做渲染，不落盘。供 YAML 预览接口使用。
//
// **预览与执行必须走同一条渲染路径**，否则「编辑器中看到的」与「实际跑的」
// 会不一致——那是最难排查的一类问题。
func Render(in *Input) (*Output, error) {
	if in == nil || in.Project == nil {
		return nil, fmt.Errorf("编译输入缺少项目信息")
	}

	// config.name 唯一性校验（F11）。
	seen := map[string]string{}
	for _, spec := range in.Cases {
		if spec.Case == nil {
			continue
		}
		name := strings.TrimSpace(spec.Case.Name)
		if name == "" {
			return nil, fmt.Errorf("用例 %s 缺少名称：config.name 是引擎识别用例的唯一标识", spec.Case.Code)
		}
		if prev, dup := seen[name]; dup {
			return nil, fmt.Errorf(
				"用例名称 %q 重复（出现在 %s 与 %s）。引擎的 summary.json 以用例名作唯一标识，"+
					"重名会导致执行结果无法区分，请先改名", name, prev, spec.Case.Code)
		}
		seen[name] = spec.Case.Code
	}

	out := &Output{EnvText: RenderEnv(in.Env)}

	for _, spec := range in.Cases {
		cc, err := renderCase(in, spec)
		if err != nil {
			return nil, err
		}
		out.Cases = append(out.Cases, *cc)
	}
	return out, nil
}

// RenderEnv 渲染 .env 内容。
//
// hrp v4.1 起 base_url 从 config 移到了 .env（方案 3.3），
// 因此环境模块的最终产物就是这一个文件。
//
// 注意：verify_ssl **不写进 .env**，它渲染到用例的 config.verify；
// global_headers 也**不写进 .env**，编译器把它们合并进每个步骤的 headers。
func RenderEnv(env *model.Environment) string {
	var b strings.Builder

	baseURL := "http://127.0.0.1:8899"
	if env != nil && strings.TrimSpace(env.BaseURL) != "" {
		baseURL = strings.TrimSpace(env.BaseURL)
	}
	b.WriteString("base_url=" + envValue(baseURL) + "\n")

	if env != nil {
		// 按 key 排序输出，保证 .env 内容稳定可比对（map 遍历顺序随机）。
		for _, k := range sortedKeys(env.Environs) {
			v := scalarToString(env.Environs[k])
			b.WriteString(k + "=" + envValue(v) + "\n")
		}
	}
	return b.String()
}

// envValue 对 .env 的值做必要转义。
//
// 含空格或 # 时不加引号会被 dotenv 截断（# 被当作注释起始），
// 因此统一用双引号包裹并转义内部引号。
func envValue(v string) string {
	if v == "" {
		return ""
	}
	if !strings.ContainsAny(v, " #\"'\t") {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// renderCase 渲染单个用例。
func renderCase(in *Input, spec CaseSpec) (*CompiledCase, error) {
	tc := spec.Case

	cfg := yamlConfig{
		Name:   strings.TrimSpace(tc.Name),
		Verify: envVerify(in.Env),
	}
	// config 里的变量 / 导出
	if err := applyCaseConfig(&cfg, tc); err != nil {
		return nil, err
	}

	// ---------------------------------------------------------------------
	// ⚠️ M1 刻意**不写 config.headers**，全部下沉到步骤的 request.headers。
	//
	// 原因：引擎合并 config.headers 与 step.request.headers 时是**大小写敏感**的
	// （内部用 omap，键原样保留）。同一个请求头只要在两层分别写成
	// `content-type` 与 `Content-Type`，引擎就会把两个都发出去 —— 服务端
	// 收到的重复头谁生效取决于实现，属于极难排查的问题。
	//
	// 平台改为自己合并「环境全局头 → 用例头 → 步骤头」三层，
	// 用大小写不敏感的方式去重，优先级由合并顺序确定。
	// ---------------------------------------------------------------------
	caseHeaders := cfg.Headers
	cfg.Headers = nil

	steps := enabledSteps(spec.Steps)
	if len(steps) == 0 {
		return nil, fmt.Errorf("用例 %s 没有任何启用的步骤，无法编译", tc.Code)
	}

	doc := yamlTestCase{
		Config:    cfg,
		TestSteps: make([]yamlTestStep, 0, len(steps)),
	}
	decls := make([]StepDecl, 0, len(steps))

	for i, st := range steps {
		ys, decl, err := renderStep(in, st, caseHeaders, i+1)
		if err != nil {
			return nil, fmt.Errorf("用例 %s 的第 %d 步（%s）: %w", tc.Code, st.Seq, st.Name, err)
		}
		doc.TestSteps = append(doc.TestSteps, ys)
		decls = append(decls, decl)
	}

	body, err := marshalYAML(doc)
	if err != nil {
		return nil, fmt.Errorf("用例 %s 序列化 YAML 失败: %w", tc.Code, err)
	}

	return &CompiledCase{
		CaseID:     tc.ID,
		Code:       tc.Code,
		ConfigName: cfg.Name,
		FileName:   DirTestcases + "/" + tc.Code + ".yaml",
		YAML:       body,
		Steps:      decls,
	}, nil
}

// renderStep 渲染单个步骤。
//
// caseHeaders 是用例级请求头（来自 config），优先级低于步骤头、高于环境头。
func renderStep(in *Input, st model.TestStep, caseHeaders map[string]any, ordinal int) (yamlTestStep, StepDecl, error) {
	name := strings.TrimSpace(st.Name)
	if name == "" {
		name = fmt.Sprintf("步骤 %d", ordinal)
	}

	ys := yamlTestStep{
		Name:      name,
		Variables: st.Variables,
	}

	decl := StepDecl{
		Seq:        st.Seq,
		Name:       name,
		StepType:   st.StepType,
		Assertions: []model.AssertItem(st.Validate),
	}

	// 三层请求头合并：环境 → 用例 → 步骤（后者覆盖前者，大小写不敏感）。
	// 合并顺序即优先级，与前端「高级设置」里的层级一致。
	merged := mergeStringMap(in.EnvGlobalHeaders(), caseHeaders)

	switch st.StepType {
	case model.StepRequest, "":
		req, err := decodeRequest(st.Request)
		if err != nil {
			return ys, decl, err
		}
		if strings.TrimSpace(req.URL) == "" {
			return ys, decl, fmt.Errorf("请求步骤缺少 url")
		}
		decl.Method = strings.ToUpper(req.Method)
		decl.URL = req.URL

		yreq := &yamlRequest{
			Method: strings.ToUpper(defaultString(req.Method, "GET")),
			URL:    req.URL,
		}
		yreq.Headers = mergeStringMap(merged, req.Headers)
		if len(req.Params) > 0 {
			yreq.Params = req.Params
		}
		if req.Timeout > 0 {
			// request 层才生效（实测 F13）；teststep 层的 timeout 会被静默忽略（F14）
			yreq.Timeout = float64(req.Timeout)
		}

		if err := applyBody(yreq, req); err != nil {
			return ys, decl, err
		}
		if len(yreq.Headers) == 0 {
			yreq.Headers = nil
		}
		ys.Request = yreq

	case model.StepAPI, model.StepTestCase:
		// M2 才会开放；这里显式拒绝，避免产出引擎无法解析的文件。
		return ys, decl, fmt.Errorf(
			"步骤类型 %q 在 M1 尚未开放（计划在 M2 支持接口/用例引用）", st.StepType)

	default:
		return ys, decl, fmt.Errorf("不支持的步骤类型 %q", st.StepType)
	}

	// extract：渲染成 `name: "<object>.<expression>"`（v4 写法）
	if len(st.Extract) > 0 {
		ex := map[string]any{}
		for _, it := range st.Extract {
			if strings.TrimSpace(it.Name) == "" {
				return ys, decl, fmt.Errorf("存在未命名的提取项")
			}
			ex[it.Name] = renderExtractExpr(it)
		}
		ys.Extract = ex
	}

	// validate：渲染成 `- <method>: [check, expect]`
	if len(st.Validate) > 0 {
		list := make([]yamlAssert, 0, len(st.Validate))
		for _, a := range st.Validate {
			if strings.TrimSpace(a.Check) == "" {
				return ys, decl, fmt.Errorf("存在缺少检查表达式的断言")
			}
			if strings.TrimSpace(a.Assert) == "" {
				return ys, decl, fmt.Errorf("断言 %q 缺少校验方法（如 eq / contains / type_match）", a.Check)
			}
			list = append(list, yamlAssert{
				Method: strings.TrimSpace(a.Assert),
				Args:   flowSeq{a.Check, normalizeScalar(a.Expect)},
			})
		}
		ys.Validate = list
	}

	// hooks：必须渲染到 teststep 层（实测 F15/F16 —— 放在 request 层会被静默忽略）
	h, err := decodeHooks(st.Hooks)
	if err != nil {
		return ys, decl, err
	}
	if h != nil {
		ys.SetupHooks = h.Setup
		ys.TeardownHooks = h.Teardown
	}

	return ys, decl, nil
}

// applyBody 按 body_type 决定使用哪个请求体字段。
func applyBody(dst *yamlRequest, src model.RequestSpec) error {
	switch src.BodyType {
	case "", model.BodyTypeNone:
		return nil

	case model.BodyTypeJSON:
		v := src.Body.Val
		if v == nil {
			return nil
		}
		dst.JSON = &jsonx.Any{Val: normalizeScalar(v)}

	case model.BodyTypeForm:
		v := src.Body.Val
		if v == nil {
			return nil
		}
		// ⚠️ 必须显式给出表单 Content-Type，否则引擎会把 map 序列化成 JSON
		// 而不是 URL 编码（实测确认）。
		if dst.Headers == nil {
			dst.Headers = map[string]any{}
		}
		if !hasHeader(dst.Headers, "Content-Type") {
			dst.Headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
		dst.Data = &jsonx.Any{Val: normalizeScalar(v)}

	case model.BodyTypeRaw:
		v := src.Body.Val
		if v == nil {
			return nil
		}
		dst.Data = &jsonx.Any{Val: normalizeScalar(v)}

	default:
		return fmt.Errorf("不支持的请求体类型 %q（可选 json / form / raw / none）", src.BodyType)
	}
	return nil
}

// renderExtractExpr 把提取配置渲染成引擎的表达式。
//
// 引擎的写法是 `<object>.<path>`，例如 `body.args.token`。
// status_code / proto 这类标量对象没有子路径，直接用对象名。
func renderExtractExpr(it model.ExtractItem) string {
	obj := strings.TrimSpace(it.Object)
	expr := strings.TrimSpace(it.Expression)
	switch obj {
	case model.ExtractStatusCode, model.ExtractProto:
		return obj
	case "":
		return expr
	default:
		if expr == "" {
			return obj
		}
		return obj + "." + strings.TrimPrefix(expr, ".")
	}
}

func hasHeader(h map[string]any, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}
