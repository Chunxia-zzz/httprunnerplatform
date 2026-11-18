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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// WorkspaceRoot 是项目持久工作区的绝对路径（M3 参数化需要）。
	//
	// 只有 CSV 数据集的列名读取与 limit 派生文件生成会用到：
	// CSV 内容以**项目工作区里的文件**为真源，编译期读取它来生成参数键
	// 与派生文件。空值 + 无 CSV 引用时可以不传。
	WorkspaceRoot string
	// Datasets 是本项目全部参数化数据集，按名称索引（M3 参数化）。
	//
	// 由调用方（service 层）在编译前一次性查好传入：compiler 是纯函数包，
	// 不查库。引用了不存在的数据集时渲染会直接报错（resolveParameters）。
	Datasets map[string]*model.ParamDataset
	// APIs 是本项目全部接口定义，按 ID 索引（F1 接口引用）。
	//
	// 与 Datasets 同理：service 层查好传入，compiler 不查库。
	// step_type=api 的步骤会按 ref_api_id 取出定义并**内联展开**为 request 步骤
	// （不用引擎的 api/ 目录引用，保证导出产物自包含、可离线执行）。
	APIs map[uint64]*model.APIDef
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
	// ExtraFiles 是本用例引出的**额外编译产物**（M3 参数化：limit 裁剪后的派生 CSV）。
	//
	// 键是工作区内的相对路径（形如 data/users__l2.csv），值是文件内容。
	// 与 YAML 同理是可再生成的产物：DB 里的数据集定义才是唯一真源。
	ExtraFiles map[string]string
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
		// 参数化派生文件（limit 裁剪版 CSV 等）。同一文件可能被多个用例引用，
		// 内容由同一份数据集定义生成，重复写结果幂等。
		for rel, content := range c.ExtraFiles {
			p := filepath.Join(workspaceRoot, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, fmt.Errorf("创建数据目录失败: %w", err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				return nil, fmt.Errorf("写入参数化文件 %s 失败: %w", rel, err)
			}
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

	// ⭐ 无环境 / base_url 为空时**留空**，绝不兜底地址（BUG-007）。
	// 旧实现兜底 http://127.0.0.1:8899（开发期的 echo 服务地址）：无环境的项目
	// 会静默请求这个无关地址，在本机"恰好成功"，换台机器就打到别人的服务上
	// —— 典型的假绿。留空时引擎报 variable base_url not found，平台归因为
	// environment 并给出可读提示，这才是「未配置」应有的表现。
	var baseURL string
	if env != nil {
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

	// 参数化：把用例 config.datasets 里的数据集引用展开成引擎语法。
	refs, err := datasetRefsOf(tc)
	if err != nil {
		return nil, fmt.Errorf("用例 %s 的参数化配置: %w", tc.Code, err)
	}
	extra, err := resolveParameters(in, refs, &cfg)
	if err != nil {
		return nil, fmt.Errorf("用例 %s 的参数化配置: %w", tc.Code, err)
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
		ExtraFiles: extra,
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

	// 认证（F7）位于优先级最底层：手写请求头 > 用例头 > 环境头 > 认证。
	authHeaders, authParams := authInjection(resolveCompileAuth(in.Env, st))

	switch st.StepType {
	case model.StepRequest, "":
		req, err := decodeRequest(st.Request)
		if err != nil {
			return ys, decl, err
		}
		if err := renderRequestInto(&ys, &decl, req, merged, authHeaders, authParams); err != nil {
			return ys, decl, err
		}

	case model.StepAPI:
		// F1 接口引用：按 ref_api_id 取出接口定义，内联展开为 request。
		//
		// 选择内联而不是引擎的 `api/` 目录引用：导出的 YAML 必须自包含
		// （可离线执行、可单文件带走），且不依赖引擎对 api 文件的查找约定。
		// 代价是 YAML 里看不到「引用了哪个接口」——引用关系是平台层概念，
		// 在用例详情/步骤声明里保留（StepDecl.StepType 仍为 api）。
		if st.RefAPIID == 0 {
			return ys, decl, fmt.Errorf("接口引用步骤缺少 ref_api_id")
		}
		api, ok := in.APIs[st.RefAPIID]
		if !ok || api == nil {
			return ys, decl, fmt.Errorf("接口引用步骤引用的接口不存在（id=%d）", st.RefAPIID)
		}
		if in.Project != nil && api.ProjectID != in.Project.ID {
			return ys, decl, fmt.Errorf("接口引用步骤引用的接口不属于本项目（id=%d）", st.RefAPIID)
		}
		req := model.RequestSpec{
			Method:   api.Method,
			URL:      api.Path,
			Headers:  api.Headers,
			Params:   api.Params,
			Body:     api.Body,
			BodyType: api.BodyType,
		}
		if err := renderRequestInto(&ys, &decl, req, merged, authHeaders, authParams); err != nil {
			return ys, decl, fmt.Errorf("接口 %s 的定义不完整: %w", api.Code, err)
		}

	case model.StepWebSocket:
		// WebSocket（F10）：动作式步骤，断言/提取作用于收到的消息（body）。
		ws, err := renderWebSocket(st)
		if err != nil {
			return ys, decl, err
		}
		ys.WebSocket = ws
		decl.StepType = model.StepWebSocket

	case model.StepGraphql:
		// GraphQL（F10）：编译成标准 HTTP POST，body = {query, variables, operationName}。
		// 引擎无需知道这是 GraphQL —— 服务端契约本来就是这样一个 POST。
		req, err := decodeRequest(st.Request)
		if err != nil {
			return ys, decl, err
		}
		req.Method = "POST"
		req.BodyType = model.BodyTypeJSON
		// ⭐ 实测 A28：GraphQL 的 `$id` 与平台的变量语法同形，不转义会被引擎当变量
		// 替换（变量不存在则直接中止）。引擎的转义写法是 `$$` → 发出时还原成 `$`。
		// 因此查询语句里的 `$` 一律转义；需要平台变量请写在 variables 里。
		body := map[string]any{"query": escapeDollar(st.GraphqlQuery)}
		if st.GraphqlVariables.Val != nil {
			body["variables"] = st.GraphqlVariables.Val
		}
		if strings.TrimSpace(st.GraphqlOperation) != "" {
			body["operationName"] = st.GraphqlOperation
		}
		req.Body = jsonx.Any{Val: body}
		if err := renderRequestInto(&ys, &decl, req, merged, authHeaders, authParams); err != nil {
			return ys, decl, err
		}
		decl.StepType = model.StepGraphql

	case model.StepTestCase:
		// 用例引用尚未开放（F1 只做接口引用，用例引用属后续里程碑）。
		return ys, decl, fmt.Errorf(
			"步骤类型 %q 尚未开放（当前只支持接口引用 api，用例引用待后续里程碑）", st.StepType)

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
				// 断言里的 body 路径同样归一（实测 A25）：`body.json.token`
				// 在引擎里表示「响应体里有个叫 json 的键」，取不到就整条用例中止。
				Args: flowSeq{normalizeCheckExpr(a.Check), normalizeScalar(a.Expect)},
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
// renderRequestInto 把一份请求声明渲染进步骤（同时回填声明信息）。
//
// 直接请求与接口引用共用这一条路径：两者唯一的区别是请求声明来自步骤本身
// 还是来自接口定义——渲染规则必须完全一致，否则「引用」与「直接写」
// 会产出不同的 YAML，引用就不可信了。
func renderRequestInto(ys *yamlTestStep, decl *StepDecl, req model.RequestSpec,
	merged map[string]any, authHeaders map[string]any, authParams jsonx.Map) error {
	if strings.TrimSpace(req.URL) == "" {
		return fmt.Errorf("请求缺少 url")
	}
	decl.Method = strings.ToUpper(req.Method)
	decl.URL = req.URL

	yreq := &yamlRequest{
		Method: strings.ToUpper(defaultString(req.Method, "GET")),
		URL:    req.URL,
	}
	// 认证头作为最低优先级层：先铺认证，再叠环境/用例头，最后叠步骤头
	merged = mergeStringMap(authHeaders, merged)
	yreq.Headers = mergeStringMap(merged, req.Headers)
	params := mergeStringMap(map[string]any(authParams), req.Params)
	if len(params) > 0 {
		yreq.Params = params
	}
	if req.Timeout > 0 {
		// request 层才生效（实测 F13）；teststep 层的 timeout 会被静默忽略（F14）
		yreq.Timeout = float64(req.Timeout)
	}

	if err := applyBody(yreq, req); err != nil {
		return err
	}
	if len(yreq.Headers) == 0 {
		yreq.Headers = nil
	}

	// 动态变量（F7）：编译期展开，因此每次执行重新编译就会得到新值。
	yreq.URL = expandDynamic(yreq.URL)
	for k, v := range yreq.Headers {
		if sv, ok := v.(string); ok {
			yreq.Headers[k] = expandDynamic(sv)
		}
	}
	for k, v := range yreq.Params {
		yreq.Params[k] = expandDynamicAny(v)
	}
	if yreq.JSON != nil {
		yreq.JSON.Val = expandDynamicAny(yreq.JSON.Val)
	}
	if yreq.Data != nil {
		yreq.Data.Val = expandDynamicAny(yreq.Data.Val)
	}

	ys.Request = yreq
	return nil
}

// ---------------------------------------------------------------------------
// 认证（F7）
// ---------------------------------------------------------------------------

// resolveCompileAuth 决定这一步生效的认证：步骤优先，inherit/空沿用环境。
func resolveCompileAuth(env *model.Environment, st model.TestStep) model.AuthConfig {
	t := st.Auth.NormalizedType()
	if t == model.AuthTypeInherit || st.Auth.Type == "" {
		if env == nil {
			return model.AuthConfig{Type: model.AuthTypeNone}
		}
		return env.Auth
	}
	if t == model.AuthTypeNone {
		return model.AuthConfig{Type: model.AuthTypeNone}
	}
	return st.Auth
}

// authInjection 把认证配置转成要注入的请求头与查询参数。
//
// Bearer/API Key 的值允许写 `$var`：这里原样写入，由引擎在运行时替换
// （「登录拿 token → 后续接口自动带」就是靠这个）。
func authInjection(a model.AuthConfig) (map[string]any, jsonx.Map) {
	switch a.NormalizedType() {
	case model.AuthTypeBearer:
		if strings.TrimSpace(a.Token) == "" {
			return nil, nil
		}
		return map[string]any{"Authorization": "Bearer " + a.Token}, nil
	case model.AuthTypeBasic:
		if a.Username == "" && a.Password == "" {
			return nil, nil
		}
		// 字面量编码：base64 在编译期完成，因此不支持变量插值（文档已说明）
		token := base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))
		return map[string]any{"Authorization": "Basic " + token}, nil
	case model.AuthTypeAPIKey:
		if strings.TrimSpace(a.KeyName) == "" || a.KeyValue == "" {
			return nil, nil
		}
		if a.KeyIn == model.AuthInQuery {
			return nil, jsonx.Map{a.KeyName: a.KeyValue}
		}
		return map[string]any{a.KeyName: a.KeyValue}, nil
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// 动态变量（F7）
// ---------------------------------------------------------------------------

// dynamicVarRe 匹配 {{$timestamp}} / {{$timestampMs}} / {{$uuid}}。
//
// 只认这个精确形态：用户变量 `$timestamp` 不受影响，避免误伤。
var dynamicVarRe = regexp.MustCompile(`\{\{\$(timestampMs|timestamp|uuid)\}\}`)

// expandDynamic 展开动态变量（编译期一次性求值）。
func expandDynamic(s string) string {
	if !strings.Contains(s, "{{$") {
		return s
	}
	return dynamicVarRe.ReplaceAllStringFunc(s, func(m string) string {
		switch m {
		case "{{$timestamp}}":
			return strconv.FormatInt(time.Now().Unix(), 10)
		case "{{$timestampMs}}":
			return strconv.FormatInt(time.Now().UnixMilli(), 10)
		case "{{$uuid}}":
			return newUUIDv4()
		}
		return m
	})
}

// expandDynamicAny 递归展开任意 JSON 结构里的动态变量（只动字符串）。
func expandDynamicAny(v any) any {
	switch t := v.(type) {
	case string:
		return expandDynamic(t)
	case []any:
		for i := range t {
			t[i] = expandDynamicAny(t[i])
		}
		return t
	case map[string]any:
		for k, val := range t {
			t[k] = expandDynamicAny(val)
		}
		return t
	default:
		return v
	}
}

// newUUIDv4 生成随机 UUID（v4 格式）。
func newUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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
// normalizeCheckExpr 归一断言里的检查表达式。
//
// 与提取共用同一条引擎事实（A25）：body 的取值路径是 `body.<path>`。
// 历史数据/前端提示里的 `body.json.x`、`json.x` 都吸收了，避免「断言一加
// 整条用例就中止」这种最难查的失败。
func normalizeCheckExpr(expr string) string {
	e := strings.TrimSpace(expr)
	if strings.HasPrefix(e, "body.json.") {
		e = "body." + strings.TrimPrefix(e, "body.json.")
	} else if strings.HasPrefix(e, "json.") {
		e = "body." + strings.TrimPrefix(e, "json.")
	}
	return quoteJmespath(e)
}

// quoteJmespath 给含非标识符字符的 jmespath 段加引号（实测 A29）。
//
// 引擎的**断言**走纯 jmespath 搜索：`body.headers.X-Env` 里的连字符让解析器
// 直接报 SyntaxError 并 nil 指针 panic（提取路径引擎自己容忍，断言不会）。
// 规则：按顶层 `.` 分段（引号内与 [] 内不切分），标识符段若不是
// 字母/下划线开头、后随字母数字下划线，就包上双引号；用户已加引号的段
// 原样保留；含 `$` 的表达式不处理（变量替换发生在 jmespath 之前）。
func quoteJmespath(expr string) string {
	if strings.Contains(expr, "$") {
		return expr
	}
	segs := splitJmespathTop(expr)
	out := make([]string, len(segs))
	changed := false
	for i, seg := range segs {
		out[i] = quoteJmespathSeg(seg)
		if out[i] != seg {
			changed = true
		}
	}
	if !changed {
		return expr
	}
	return strings.Join(out, ".")
}

// splitJmespathTop 按顶层 `.` 切分：引号内与 [] 内的点不切。
func splitJmespathTop(expr string) []string {
	var segs []string
	var cur strings.Builder
	depth := 0
	inQuote := byte(0)
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if inQuote != 0 {
			if ch == '\\' && i+1 < len(expr) {
				cur.WriteByte(ch)
				i++
				cur.WriteByte(expr[i])
				continue
			}
			if ch == inQuote {
				inQuote = 0
			}
			cur.WriteByte(ch)
			continue
		}
		switch ch {
		case '"', '\'':
			inQuote = ch
			cur.WriteByte(ch)
		case '[':
			depth++
			cur.WriteByte(ch)
		case ']':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(ch)
		case '.':
			if depth == 0 {
				segs = append(segs, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(ch)
		default:
			cur.WriteByte(ch)
		}
	}
	segs = append(segs, cur.String())
	return segs
}

// quoteJmespathSeg 处理单个分段：标识符部分不合法就加引号，[..] 下标原样保留。
func quoteJmespathSeg(seg string) string {
	ident, rest := seg, ""
	if i := strings.IndexByte(seg, '['); i >= 0 {
		ident, rest = seg[:i], seg[i:]
	}
	if ident == "" || isPlainJmespathIdent(ident) {
		return seg
	}
	if len(ident) >= 2 &&
		((ident[0] == '"' && ident[len(ident)-1] == '"') || (ident[0] == '\'' && ident[len(ident)-1] == '\'')) {
		return seg
	}
	return `"` + ident + `"` + rest
}

// isPlainJmespathIdent 判断是否为 jmespath 的合法裸标识符。
func isPlainJmespathIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return len(s) > 0
}

// renderExtractExpr 把提取项渲染成引擎的取值表达式。
//
// ⚠️ 实测 A25（hrp v4.3.6）：body 的取值路径是 **`body.<路径>`**，
// 写成 `body.json.token` 会**恒得 null 且不报错**。平台此前把用户填写的
// `json.token` 直接拼在 `body.` 后面，产出的全是取不到值的死表达式。
// 这里统一吸收历史写法（`body.` / `json.` 前缀），让同一份数据在两种写法下
// 都编译成引擎真正认识的形态。
//
// headers / cookies 的写法是 `headers.<名>`（引擎会自动加引号），
// 匹配**大小写敏感**，与 HTTP 头本身的不区分大小写不同（同一实测）。
func renderExtractExpr(it model.ExtractItem) string {
	obj := strings.TrimSpace(it.Object)
	expr := strings.TrimSpace(it.Expression)
	switch obj {
	case model.ExtractStatusCode, model.ExtractProto:
		return obj
	case "":
		return expr
	case model.ExtractBody:
		e := strings.TrimLeft(expr, ".")
		e = strings.TrimPrefix(e, "body.")
		e = strings.TrimPrefix(e, "json.")
		if e == "" {
			return "body"
		}
		return "body." + quoteJmespath(e)
	default:
		if expr == "" {
			return obj
		}
		return obj + "." + quoteJmespath(strings.TrimPrefix(expr, "."))
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

// escapeDollar 把 `$` 转义成 `$$`（引擎的转义语法，实测 A28）。
func escapeDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// renderWebSocket 把 WS 步骤渲染成引擎的 websocket 段（F10 / 实测 A26）。
//
// 三条硬约束都来自实测：
//   - 动作必须显式（open/ping/wr/w/r/close），引擎不提供"一条步骤内自动开连接"；
//   - **每个动作都要带 url**：连接按 URL 查找，缺 url 会报 no connection；
//   - 引擎会把 URL 末尾补成 "/"（/ws → /ws/），服务端需容忍。
func renderWebSocket(st model.TestStep) (*yamlWebSocket, error) {
	op := strings.ToLower(strings.TrimSpace(st.WsOpType))
	switch op {
	case "open", "ping", "wr", "w", "r", "close":
	default:
		return nil, fmt.Errorf("WebSocket 步骤的动作 %q 不合法（只支持 open/ping/wr/w/r/close）", st.WsOpType)
	}
	url := strings.TrimSpace(st.WsURL)
	if url == "" {
		return nil, fmt.Errorf("WebSocket 步骤缺少 url（引擎按 URL 查找连接，每个动作都要带）")
	}
	if (op == "w" || op == "wr") && st.WsPayload.Val == nil {
		return nil, fmt.Errorf("WebSocket 动作 %q 需要填写发送内容（text）", op)
	}

	ws := &yamlWebSocket{Type: op, URL: expandDynamic(url)}
	if len(st.WsHeaders) > 0 {
		h := make(map[string]any, len(st.WsHeaders))
		for k, v := range st.WsHeaders {
			h[k] = expandDynamicAny(v)
		}
		ws.Headers = h
	}
	if st.WsPayload.Val != nil {
		ws.Text = expandDynamicAny(st.WsPayload.Val)
	}
	return ws, nil
}
