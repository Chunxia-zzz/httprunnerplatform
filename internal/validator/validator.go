// Package validator 在保存与执行前做静态校验。
//
// 为什么这个包是**架构必需**而不是锦上添花：
//
//	实测 A7（见 docs/引擎实测记录.md 第 8 节）确认 **hrp 对未知字段完全不校验** ——
//	字段名拼错、字段放错层级，引擎既不报错也不告警，只是**不生效**。
//
// 也就是说：如果不在这里拦下来，用户会得到"配置看起来生效了、实际从未生效"的
// 结果，并且没有任何途径自己发现。这是最难排查的一类问题，只能靠平台事前校验。
//
// 校验分两组：
//
//  1. 语义校验（本文件主体）：变量引用、引用完整性、断言/提取合法性、重名……
//  2. 字段白名单（ValidateYAMLFields）：对**渲染后的 YAML** 再做一遍键名检查。
//     第 2 组看似冗余，但它守的是另一类风险 —— 模型或编译器改动引入的新字段
//     引擎不认识。语义校验只看 DB 模型，看不到最终文件长什么样。
package validator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// 等级与 issue
// ---------------------------------------------------------------------------

// 校验等级。
const (
	LevelError   = "error"
	LevelWarning = "warning"
)

// 校验范围。
const (
	ScopeCase = "case"
	ScopeStep = "step"
)

// issue.code 字典。与 docs/接口契约.md 第 4.6 节逐一对应，改动需同步文档。
const (
	CodeUndefinedVariable     = "UNDEFINED_VARIABLE"
	CodeEmptySteps            = "EMPTY_STEPS"
	CodeMissingURL            = "MISSING_URL"
	CodeInvalidAssertMethod   = "INVALID_ASSERT_METHOD"
	CodeInvalidAssertExpr     = "INVALID_ASSERT_EXPR"
	CodeInvalidExtractObject  = "INVALID_EXTRACT_OBJECT"
	CodeInvalidExtractName    = "INVALID_EXTRACT_NAME"
	CodeUnresolvedRef         = "UNRESOLVED_REF"
	CodeDuplicateCaseName     = "DUPLICATE_CASE_NAME"
	CodeDuplicateStepName     = "DUPLICATE_STEP_NAME"
	CodeDuplicateStepSeq      = "DUPLICATE_STEP_SEQ"
	CodeNoValidate            = "NO_VALIDATE"
	CodePlaintextSecret       = "PLAINTEXT_SECRET"
	CodeUnsupportedStepType   = "UNSUPPORTED_STEP_TYPE"
	CodeInvalidBodyType       = "INVALID_BODY_TYPE"
	CodeMissingCaseName       = "MISSING_CASE_NAME"
	CodeMissingMethod         = "MISSING_METHOD"
	CodeCustomFuncUnsupported = "CUSTOM_FUNCTION_UNSUPPORTED"
	// CodeTrailingSlashAdded 提示引擎会给"没有 query 的 URL"补结尾斜杠（实测 A1）。
	CodeTrailingSlashAdded = "TRAILING_SLASH_ADDED"

	// CodeUnknownField 只在字段白名单校验里出现（实测 A7 的直接产物）。
	CodeUnknownField = "UNKNOWN_FIELD"
)

// Issue 是一条校验结果。
type Issue struct {
	Level   string `json:"level"`
	Scope   string `json:"scope"`
	Seq     int    `json:"seq"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// Result 是一次校验的结果集。
type Result struct {
	Issues []Issue `json:"issues"`
}

// OK 返回是否没有任何 error 级问题。
//
// 约定：**warning 不阻断保存与执行**，只在界面上提示。
// 唯一例外是"字段白名单"，它是 error —— 因为静默失效比直接失败更糟。
func (r *Result) OK() bool { return len(r.Errors()) == 0 }

// Errors 返回全部 error 级问题。
func (r *Result) Errors() []Issue {
	return r.filter(LevelError)
}

// Warnings 返回全部 warning 级问题。
func (r *Result) Warnings() []Issue {
	return r.filter(LevelWarning)
}

func (r *Result) filter(level string) []Issue {
	out := make([]Issue, 0, len(r.Issues))
	for _, is := range r.Issues {
		if is.Level == level {
			out = append(out, is)
		}
	}
	return out
}

func (r *Result) add(is Issue) { r.Issues = append(r.Issues, is) }

func (r *Result) errf(scope string, seq int, field, code, hint, format string, args ...any) {
	r.add(Issue{
		Level: LevelError, Scope: scope, Seq: seq, Field: field,
		Code: code, Message: fmt.Sprintf(format, args...), Hint: hint,
	})
}

func (r *Result) warnf(scope string, seq int, field, code, hint, format string, args ...any) {
	r.add(Issue{
		Level: LevelWarning, Scope: scope, Seq: seq, Field: field,
		Code: code, Message: fmt.Sprintf(format, args...), Hint: hint,
	})
}

// ---------------------------------------------------------------------------
// 输入
// ---------------------------------------------------------------------------

// CaseSpec 是一个待校验的用例。
type CaseSpec struct {
	Case  *model.TestCase
	Steps []model.TestStep
}

// Input 是一次校验的输入。
type Input struct {
	Env *model.Environment
	// Cases 是本次要校验的用例。
	Cases []CaseSpec
	// SiblingCaseNames 是同项目内**其他**用例的 config.name → code 映射。
	//
	// 用途：保存单个用例时也要能发现重名（实测 F11：引擎以 config.name
	// 作唯一标识，重名会让 summary.json 的结果无法区分）。
	SiblingCaseNames map[string]string
}

// Validate 执行全部语义校验。
func Validate(in *Input) *Result {
	r := &Result{Issues: []Issue{}}
	if in == nil {
		return r
	}

	checkCaseNames(r, in)
	for _, spec := range in.Cases {
		validateCase(r, in, spec)
	}
	return r
}

// ---------------------------------------------------------------------------
// 用例级
// ---------------------------------------------------------------------------

// checkCaseNames 检查 config.name 非空且全局唯一（实测 F11）。
func checkCaseNames(r *Result, in *Input) {
	seen := map[string]string{} // name → code
	for name, code := range in.SiblingCaseNames {
		seen[name] = code
	}

	for _, spec := range in.Cases {
		if spec.Case == nil {
			continue
		}
		name := strings.TrimSpace(spec.Case.Name)
		if name == "" {
			r.errf(ScopeCase, 0, "name", CodeMissingCaseName,
				"请为用例填写名称。它是引擎识别用例的唯一标识（实测 F11）",
				"用例 %s 缺少名称", spec.Case.Code)
			continue
		}
		if prev, dup := seen[name]; dup && prev != spec.Case.Code {
			r.errf(ScopeCase, 0, "name", CodeDuplicateCaseName,
				fmt.Sprintf("用例名称在项目内必须唯一，请改名。冲突方：%s", prev),
				"用例名称 %q 与 %s 重复；引擎的 summary.json 以用例名作唯一标识，重名会导致结果无法区分",
				name, prev)
		}
		seen[name] = spec.Case.Code
	}
}

func validateCase(r *Result, in *Input, spec CaseSpec) {
	if spec.Case == nil {
		return
	}
	tc := spec.Case

	steps := enabledSteps(spec.Steps)
	if len(steps) == 0 {
		r.errf(ScopeCase, 0, "steps", CodeEmptySteps,
			"引擎没有「跳过步骤」的语法，平台在编译期剔除禁用步骤；全部禁用等于空用例",
			"用例 %s 没有任何启用的步骤", tc.Code)
		return
	}

	// 变量作用域：环境 → 用例 config → 步骤 locals → 前序步骤 extract
	scope := newVarScope(in.Env, tc)

	// 用例级 config 自带的值也要检查引用（如 variables 里引用另一个变量）
	checkCaseConfigRefs(r, tc, scope)

	checkStepSeqAndNames(r, steps)

	for _, st := range steps {
		validateStep(r, in, tc, st, scope)
		// 前序步骤的 extract 结果对后续步骤可见
		scope.addExtracts(st)
	}
}

// checkStepSeqAndNames 检查 seq 唯一性与步骤重名。
func checkStepSeqAndNames(r *Result, steps []model.TestStep) {
	seqs := map[int]bool{}
	names := map[string]int{}

	for _, st := range steps {
		if seqs[st.Seq] {
			r.errf(ScopeStep, st.Seq, "seq", CodeDuplicateStepSeq,
				"步骤的 seq 决定编译后的书写顺序，重复会让人无法预期执行顺序",
				"步骤序号 %d 重复（步骤「%s」）", st.Seq, st.Name)
		}
		seqs[st.Seq] = true

		name := strings.TrimSpace(st.Name)
		if name == "" {
			continue
		}
		if first, dup := names[name]; dup {
			r.warnf(ScopeStep, st.Seq, "name", CodeDuplicateStepName,
				"同名步骤不影响执行，但日志与报告里会难以区分是哪一步",
				"步骤名 %q 与步骤 %d 重复", name, first)
		} else {
			names[name] = st.Seq
		}
	}
}

// ---------------------------------------------------------------------------
// 步骤级
// ---------------------------------------------------------------------------

func validateStep(r *Result, in *Input, tc *model.TestCase, st model.TestStep, scope *varScope) {
	// 步骤局部变量先入作用域（本步骤的请求可以使用它们）
	stepScope := scope.clone()
	for k, v := range st.Variables {
		stepScope.define(k)
		_ = v
	}
	// 步骤局部变量的值里也可能引用更外层的变量
	checkRefsIn(r, ScopeStep, st.Seq, "variables", st.Variables, scope)

	// 提取项先登记，便于在同一用例里被后续步骤引用（本步骤不可见）
	extractNames := extractNameSet(st.Extract)

	switch st.StepType {
	case model.StepRequest, "":
		validateRequestStep(r, st, stepScope, extractNames)
	case model.StepAPI, model.StepTestCase:
		// 引用型步骤
		if st.StepType == model.StepAPI && st.RefAPIID == 0 {
			r.errf(ScopeStep, st.Seq, "ref_api_id", CodeUnresolvedRef,
				"请选择一个接口定义，或把步骤类型改为「直接请求」",
				"步骤「%s」声明为接口引用，但未选择接口", st.Name)
		}
		if st.StepType == model.StepTestCase && st.RefCaseID == 0 {
			r.errf(ScopeStep, st.Seq, "ref_case_id", CodeUnresolvedRef,
				"请选择一个用例，或把步骤类型改为「直接请求」",
				"步骤「%s」声明为用例引用，但未选择用例", st.Name)
		}
		r.errf(ScopeStep, st.Seq, "step_type", CodeUnsupportedStepType,
			"该步骤类型计划在 M2 支持，M1 请使用「直接请求」",
			"步骤「%s」的类型 %q 在 M1 尚未开放", st.Name, st.StepType)
	default:
		r.errf(ScopeStep, st.Seq, "step_type", CodeUnsupportedStepType,
			"M1 只支持「直接请求」步骤",
			"步骤「%s」的类型 %q 不受支持", st.Name, st.StepType)
	}

	checkHooks(r, st)
}

func validateRequestStep(r *Result, st model.TestStep, scope *varScope, extractNames map[string]bool) {
	req, err := decodeRequest(st.Request)
	if err != nil {
		r.errf(ScopeStep, st.Seq, "request", CodeMissingURL,
			"请求配置无法解析，请重新填写", "步骤「%s」的请求配置不合法：%v", st.Name, err)
		return
	}

	if strings.TrimSpace(req.URL) == "" {
		r.errf(ScopeStep, st.Seq, "url", CodeMissingURL,
			"请填写请求地址，如 $base_url/login（$base_url 来自所选环境）",
			"步骤「%s」缺少请求地址", st.Name)
	}

	switch req.BodyType {
	case "", model.BodyTypeNone, model.BodyTypeJSON, model.BodyTypeForm, model.BodyTypeRaw:
	default:
		r.errf(ScopeStep, st.Seq, "body_type", CodeInvalidBodyType,
			"可选值：json / form / raw / none",
			"步骤「%s」的请求体类型 %q 不受支持", st.Name, req.BodyType)
	}

	checkTrailingSlash(r, st, req)

	// --- 变量引用 ---
	checkRefsIn(r, ScopeStep, st.Seq, "url", req.URL, scope)
	checkRefsIn(r, ScopeStep, st.Seq, "params", req.Params, scope)
	checkRefsIn(r, ScopeStep, st.Seq, "headers", req.Headers, scope)
	checkRefsIn(r, ScopeStep, st.Seq, "body", req.Body.Val, scope)

	// --- 提取项 ---
	for _, it := range st.Extract {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			r.errf(ScopeStep, st.Seq, "extract", CodeInvalidExtractName,
				"每个提取项都要有变量名，否则提取结果无处存放",
				"步骤「%s」存在未命名的提取项", st.Name)
			continue
		}
		if !containsString(model.ValidExtractObjects, it.Object) {
			r.errf(ScopeStep, st.Seq, "extract."+name, CodeInvalidExtractObject,
				"引擎只支持 "+strings.Join(model.ValidExtractObjects, " / "),
				"提取项 %q 的对象 %q 不受支持", name, it.Object)
		}
		if (it.Object == model.ExtractHeaders || it.Object == model.ExtractCookies ||
			it.Object == model.ExtractBody) && strings.TrimSpace(it.Expression) == "" {
			r.warnf(ScopeStep, st.Seq, "extract."+name, CodeInvalidExtractObject,
				"例如 body 提取应写成 json.data.token 或 args.id",
				"提取项 %q 声明了 %s 对象但没有写取值路径", name, it.Object)
		}
	}

	// --- 断言 ---
	if len(st.Validate) == 0 {
		r.warnf(ScopeStep, st.Seq, "validate", CodeNoValidate,
			"没有断言的步骤只能验证「请求发得出去」，无法验证「结果对不对」",
			"步骤「%s」没有任何断言", st.Name)
	}
	for i, a := range st.Validate {
		field := fmt.Sprintf("validate[%d]", i)
		if strings.TrimSpace(a.Check) == "" {
			r.errf(ScopeStep, st.Seq, field, CodeInvalidAssertExpr,
				"检查表达式是被测对象的取值路径，如 status_code、body.json.token",
				"步骤「%s」的第 %d 条断言缺少检查表达式", st.Name, i+1)
		}
		if !containsString(parserAssertMethods(), a.Assert) {
			r.errf(ScopeStep, st.Seq, field+".assert", CodeInvalidAssertMethod,
				"内置校验器："+strings.Join(parserAssertMethods(), " / "),
				"步骤「%s」的第 %d 条断言使用了不支持的校验方法 %q", st.Name, i+1, a.Assert)
		}
		// 期望值里也可能引用变量
		checkRefsIn(r, ScopeStep, st.Seq, field+".expect", a.Expect, scope)
	}

	// 本步骤提取出的变量对后续步骤可见；若与更外层重名，给出提示
	for name := range extractNames {
		if scope.has(name) {
			r.warnf(ScopeStep, st.Seq, "extract."+name, CodeUndefinedVariable,
				"新提取的值会覆盖同名变量，若并非有意，请换个名字以免混淆",
				"提取项 %q 与已有变量重名，会覆盖它", name)
		}
	}
}

// checkTrailingSlash 提示"引擎会给没有 query 的 URL 补结尾斜杠"。
//
// 实测 A1（见 docs/引擎实测记录.md 第 3 节）：hrp v4.3.6 对**最终 URL 不带
// query string** 的请求，会把路径补上结尾斜杠后再发出，与请求方法、是否带
// body 都无关：
//
//	url: $base_url/get                  → GET  /get/
//	url: $base_url/get   params: {}     → GET  /get/      （params 为空照样补）
//	url: $base_url/get   params: null   → GET  /get/      （写 null 也一样）
//	url: $base_url/get   params: {a: 1} → GET  /get?a=1   （有 query 就不补）
//	url: $base_url/get?inline=1         → GET  /get?inline=1
//	url: $base_url/get/                 → GET  /get/      （本来就是斜杠，无变化）
//
// 关键在于**平台无法用改写 YAML 的方式绕开**：`params: {}` / `params: null`
// 都试过，一律补。所以 A1 里那句"用例编辑器应给出提示"只能在这里兑现 ——
// 否则用户会在"被测服务对结尾斜杠敏感"（Spring Boot 默认不做尾斜杠匹配、
// 很多网关与静态服务直接 404）时收到一个看 URL 完全想不通的失败。
//
// 等级是 warning 而不是 error：绝大多数服务对结尾斜杠不敏感，
// 把它做成 error 会挡住大量正常用例。
func checkTrailingSlash(r *Result, st model.TestStep, req model.RequestSpec) {
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return
	}
	// 已经有 query、或本身就是斜杠结尾 —— 引擎不会造成任何改变
	if len(req.Params) > 0 || strings.Contains(url, "?") || strings.HasSuffix(url, "/") {
		return
	}
	r.warnf(ScopeStep, st.Seq, "url", CodeTrailingSlashAdded,
		"需要精确控制路径时，把 query 直接写进 URL（如 $base_url/get?x=1）："+
			"引擎对已带 query 的 URL 不做任何改写",
		"步骤「%s」的请求不带 query 参数，引擎会把路径补上结尾斜杠后再发出"+
			"（实测：url 写 $base_url/get，实际发出的是 GET /get/）。"+
			"若被测服务对结尾斜杠敏感，可能得到意外的 404", st.Name)
}

// checkHooks 检查 hook 配置。
//
// hooks 依赖自定义函数（`${func()}`），而 M1–M4 不支持 debugtalk.py，
// 因此这里给 error 而不是 warning：配了也不会执行（实测 A13 证明错层级会静默失效），
// 用户需要明确知道自己配置的东西不会生效。
func checkHooks(r *Result, st model.TestStep) {
	hooks, err := decodeHooks(st.Hooks)
	if err != nil {
		r.errf(ScopeStep, st.Seq, "hooks", CodeUnknownField,
			"hooks 的格式应为 {setup: [...], teardown: [...]}",
			"步骤「%s」的 hooks 无法解析：%v", st.Name, err)
		return
	}
	if hooks == nil {
		return
	}
	r.errf(ScopeStep, st.Seq, "hooks", CodeCustomFuncUnsupported,
		"hooks 需要注册自定义函数，计划随 M2 的插件能力一起开放",
		"步骤「%s」配置了 hooks，但 M1 不支持自定义函数，hooks 不会被执行", st.Name)
}

// ---------------------------------------------------------------------------
// 变量作用域与引用检查
// ---------------------------------------------------------------------------

// varScope 记录某一位置上可用的变量名。
//
// 作用域分层（与引擎一致）：
//
//	环境（base_url + environs） → 用例 config.variables → 步骤 variables → 前序步骤 extract
type varScope struct {
	vars map[string]bool
}

func newVarScope(env *model.Environment, tc *model.TestCase) *varScope {
	s := &varScope{vars: map[string]bool{}}

	// 引擎把 .env 里的 base_url 注入为变量，这是每个用例都能用的
	s.define("base_url")
	if env != nil {
		for k := range env.Environs {
			s.define(k)
		}
	}
	if tc != nil {
		if cfg, err := decodeCaseConfig(tc.Config); err == nil && cfg != nil {
			for k := range cfg.Variables {
				s.define(k)
			}
			for k := range cfg.Parameters {
				s.define(k)
			}
		}
	}
	return s
}

func (s *varScope) clone() *varScope {
	n := &varScope{vars: make(map[string]bool, len(s.vars))}
	for k := range s.vars {
		n.vars[k] = true
	}
	return n
}

func (s *varScope) define(name string)   { s.vars[name] = true }
func (s *varScope) has(name string) bool { return s.vars[name] }

// addExtracts 把某步骤的提取结果登记进作用域。
func (s *varScope) addExtracts(st model.TestStep) {
	for _, it := range st.Extract {
		if n := strings.TrimSpace(it.Name); n != "" {
			s.define(n)
		}
	}
}

// checkCaseConfigRefs 检查用例 config 各字段里的变量引用。
//
// 注意 config.variables 之间可以互相引用，且顺序不保证，
// 因此这里只用「环境 + config 自身的键」做作用域，避免误报。
func checkCaseConfigRefs(r *Result, tc *model.TestCase, scope *varScope) {
	cfg, err := decodeCaseConfig(tc.Config)
	if err != nil || cfg == nil {
		return
	}
	self := scope.clone()
	for k := range cfg.Variables {
		self.define(k)
	}
	for k := range cfg.Parameters {
		self.define(k)
	}

	checkRefsIn(r, ScopeCase, 0, "config.headers", cfg.Headers, self)
	checkRefsIn(r, ScopeCase, 0, "config.variables", cfg.Variables, self)
	checkRefsIn(r, ScopeCase, 0, "config.parameters", cfg.Parameters, self)
	checkSecretWarnings(r, ScopeCase, 0, "config.variables", cfg.Variables)
}

// checkRefsIn 递归扫描值里的 `$var` 与 `${func()}` 引用。
func checkRefsIn(r *Result, scopeName string, seq int, field string, v any, scope *varScope) {
	for _, ref := range collectRefs(v) {
		switch ref.kind {
		case refVariable:
			if !scope.has(ref.name) {
				r.errf(scopeName, seq, field, CodeUndefinedVariable,
					fmt.Sprintf("请在上一步的「提取」里添加 %s，"+
						"或在用例变量 / 环境变量中定义它。引擎遇到未定义变量会直接以退出码 21 中止（实测）", ref.name),
					"引用了未定义的变量 $%s", ref.name)
			}
		case refFunction:
			if !builtinFunctions[strings.ToUpper(ref.name)] {
				r.warnf(scopeName, seq, field, CodeCustomFuncUnsupported,
					"M1 未启用插件能力，请改用「提取」+ 环境变量传递数据；该表达式会被原样交给引擎",
					"表达式 ${%s} 使用了自定义函数，M1 不支持（引擎会报 function not found，退出码 23）", ref.name)
			}
		}
	}
}

// checkSecretWarnings 提示疑似硬编码的密钥。
//
// 目的不是"安全合规"，而是工程习惯：密钥写死在用例里，换环境就得改用例。
func checkSecretWarnings(r *Result, scopeName string, seq int, field string, m map[string]any) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v, ok := m[k].(string)
		if !ok {
			continue
		}
		if !looksLikeSecretKey(k) {
			continue
		}
		// 引用了变量、或本身就是占位符的，不算硬编码
		if strings.Contains(v, "$") || strings.Contains(v, "${") || v == "" {
			continue
		}
		if len(v) < 6 {
			continue
		}
		r.warnf(scopeName, seq, field+"."+k, CodePlaintextSecret,
			"建议改为环境变量（$xxx），这样切换环境时不用改用例",
			"变量 %q 的值疑似硬编码密钥", k)
	}
}

var secretKeyMarkers = []string{
	"password", "passwd", "pwd", "secret", "token",
	"api_key", "apikey", "access_key", "accesskey",
	"private_key", "privatekey", "credential", "auth",
}

func looksLikeSecretKey(key string) bool {
	low := strings.ToLower(key)
	for _, m := range secretKeyMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 引用扫描
// ---------------------------------------------------------------------------

type refKind int

const (
	refVariable refKind = iota
	refFunction
)

type ref struct {
	kind refKind
	name string
}

// collectRefs 从任意值里抽出全部变量与函数引用。
//
// 手写扫描而不是正则，因为 Go 的 RE2 **不支持负向前瞻**，
// 没法用 `\$(?!\{)` 把 `$var` 与 `${func()}` 区分开。
func collectRefs(v any) []ref {
	var out []ref

	var walk func(any)
	walk = func(node any) {
		switch t := node.(type) {
		case string:
			out = append(out, scanString(t)...)
		case []string:
			for _, s := range t {
				out = append(out, scanString(s)...)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		case jsonx.Map:
			for _, e := range t {
				walk(e)
			}
		case jsonx.Slice[model.ExtractItem]:
			for _, e := range t {
				walk(e.Expression)
			}
		case jsonx.Slice[model.AssertItem]:
			for _, e := range t {
				walk(e.Expect)
			}
		case model.RequestSpec:
			walk(t.URL)
			walk(t.Params)
			walk(t.Headers)
			walk(t.Body.Val)
		case model.CaseConfig:
			walk(t.Variables)
			walk(t.Parameters)
			walk(t.Headers)
		case model.Hooks:
			walk(t.Setup)
			walk(t.Teardown)
		case map[string]string:
			for _, e := range t {
				out = append(out, scanString(e)...)
			}
		default:
			// 标量或不认识的类型：没有引用可抽
		}
	}
	walk(v)
	return out
}

// scanString 扫描一个字符串里的 `$name` 与 `${expr}`。
func scanString(s string) []ref {
	var out []ref
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' {
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '{' {
			// ${...}：函数调用表达式，取最内层函数名
			end := -1
			depth := 0
			for j := i + 1; j < len(runes); j++ {
				switch runes[j] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						end = j
					}
				}
				if end >= 0 {
					break
				}
			}
			if end < 0 {
				continue
			}
			expr := string(runes[i+2 : end])
			out = append(out, ref{kind: refFunction, name: functionNameOf(expr)})
			i = end
			continue
		}
		// $name
		j := i + 1
		for j < len(runes) && isIdentRune(runes[j], j == i+1) {
			j++
		}
		if j == i+1 {
			continue
		}
		out = append(out, ref{kind: refVariable, name: string(runes[i+1 : j])})
		i = j - 1
	}
	return out
}

// functionNameOf 从表达式文本里取函数名：`ENV(USERNAME)` → `ENV`。
func functionNameOf(expr string) string {
	expr = strings.TrimSpace(expr)
	if idx := strings.IndexAny(expr, "(."); idx > 0 {
		return strings.TrimSpace(expr[:idx])
	}
	return expr
}

func isIdentRune(r rune, first bool) bool {
	if r == '_' {
		return true
	}
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
		return true
	}
	if first {
		return false
	}
	return r >= '0' && r <= '9'
}

// builtinFunctions 是引擎自带、**不需要 debugtalk.py** 的函数。
//
// 白名单很短：httprunner v4 的内置函数主要是这两个，
// 其余（`get_user_agent` / `sum_two_int` 之类）都来自用户项目里的 debugtalk.py。
// 因此不在白名单里的函数一律提示"需要插件"。
var builtinFunctions = map[string]bool{
	"ENV": true,
	"P":   true,
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// enabledSteps 过滤出启用的步骤并按 seq 升序。
//
// 与 compiler 包的同名函数保持同一语义：引擎没有"跳过步骤"的语法，
// 所以校验必须只看**会被真正执行**的步骤。
func enabledSteps(steps []model.TestStep) []model.TestStep {
	out := make([]model.TestStep, 0, len(steps))
	for _, s := range steps {
		if s.Enabled {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func extractNameSet(items jsonx.Slice[model.ExtractItem]) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		if n := strings.TrimSpace(it.Name); n != "" {
			out[n] = true
		}
	}
	return out
}
