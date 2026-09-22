package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// YAML 渲染结构
//
// 这些结构体是「DB 模型」到「引擎 YAML」的唯一映射层。字段名、层级、
// 以及**哪些字段在哪个层级**都由实测结论决定，改动前请先看 docs/引擎实测记录.md。
//
// ⚠️ 最关键的一条：**引擎对未知字段完全静默忽略**（实测 F12）。
// 也就是说字段放错层级不会报错，只会不生效 ——
// 例如 `setup_hooks` 放在 `request` 下会被静默丢弃（实测 F16），
// 放在 teststep 下才生效（实测 F15）。
// 因此本文件的字段位置不能靠"看起来合理"来推断，必须逐条对齐实测。
// ---------------------------------------------------------------------------

// yamlTestCase 是一个用例文件的根结构。
type yamlTestCase struct {
	Config    yamlConfig     `yaml:"config"`
	TestSteps []yamlTestStep `yaml:"teststeps"`
}

// yamlConfig 对应用例的 config 段。
//
// 字段顺序即 YAML 输出顺序，按「用户最常看的在前」排列。
//
// 注意 verify **不加 omitempty**：引擎默认开启 TLS 校验，本地 HTTP 环境
// 必须显式写 false，省略会导致 `x509: certificate signed by unknown authority`。
//
// Headers 字段虽然在引擎里合法，但 **M1 不会写入**：引擎合并 config.headers
// 与 step.request.headers 时大小写敏感，同一个头在两层写法不同就会重复发送。
// 编译器改为把「环境 → 用例 → 步骤」三层头统一合并进每个步骤，
// 详见 compiler.go 里 renderCase 的说明。
type yamlConfig struct {
	Name       string    `yaml:"name"`
	Variables  jsonx.Map `yaml:"variables,omitempty"`
	Parameters jsonx.Map `yaml:"parameters,omitempty"`
	Headers    jsonx.Map `yaml:"headers,omitempty"`
	Verify     bool      `yaml:"verify"`
	Export     []string  `yaml:"export,omitempty"`
	Weight     int       `yaml:"weight,omitempty"`
}

// yamlTestStep 对应一个 teststep。
//
// ⚠️ Hooks 必须在这一层（实测 F15/F16）。
type yamlTestStep struct {
	Name          string         `yaml:"name"`
	Variables     jsonx.Map      `yaml:"variables,omitempty"`
	SetupHooks    []string       `yaml:"setup_hooks,omitempty"`
	Request       *yamlRequest   `yaml:"request,omitempty"`
	Extract       map[string]any `yaml:"extract,omitempty"`
	Validate      []yamlAssert   `yaml:"validate,omitempty"`
	TeardownHooks []string       `yaml:"teardown_hooks,omitempty"`
}

// yamlRequest 对应 request 段。
//
// `json` / `data` 二者互斥，由 body_type 决定用哪个（实测见 docs/引擎实测记录.md 的体字段实验）：
//
//	json  → 引擎自动补 Content-Type: application/json; charset=utf-8
//	data  → 引擎按请求头决定编码：有 form Content-Type 走 urlencoded，
//	        否则把 map 序列化成 JSON（很容易踩坑，所以 applyBody 会强制补头）
//
// Timeout 放在这一层才生效（实测 F13）；放在 teststep 层会被静默忽略（实测 F14）。
// 单位为秒，引擎接受小数。
type yamlRequest struct {
	Method  string         `yaml:"method"`
	URL     string         `yaml:"url"`
	Params  jsonx.Map      `yaml:"params,omitempty"`
	Headers map[string]any `yaml:"headers,omitempty"`
	JSON    *jsonx.Any     `yaml:"json,omitempty"`
	Data    *jsonx.Any     `yaml:"data,omitempty"`
	Timeout float64        `yaml:"timeout,omitempty"`
}

// yamlAssert 是一条断言。
//
// 引擎要求的形态是「单键映射：校验方法 → [检查表达式, 期望值]」：
//
//   - eq: ["status_code", 200]
//
// 不能用 `- {eq: [...]}` 之外的写法，也不支持自定义 msg。
type yamlAssert struct {
	Method string
	Args   flowSeq
}

// MarshalYAML 输出 `eq: [status_code, 200]` 这一形态。
func (a yamlAssert) MarshalYAML() (any, error) {
	if strings.TrimSpace(a.Method) == "" {
		return nil, fmt.Errorf("断言缺少校验方法")
	}
	// 单键映射：yaml.v3 对 map 会排序键，这里只有一个键，顺序无关。
	return map[string]any{a.Method: a.Args}, nil
}

// flowSeq 是以流式（inline）风格输出的序列。
//
// 为什么需要它：断言必须是 `["status_code", 200]` 这种一行写法。
// 若用普通 []any，yaml.v3 会展开成多行块状序列，语义虽等价但
// 与 httprunner 官方文档、以及用户手工编写的用例风格差异过大。
type flowSeq []any

// MarshalYAML 输出流式序列。
func (s flowSeq) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Tag: "!!seq"}
	for _, v := range s {
		child := &yaml.Node{}
		if err := child.Encode(normalizeScalar(v)); err != nil {
			return nil, fmt.Errorf("断言参数无法序列化: %w", err)
		}
		n.Content = append(n.Content, child)
	}
	return n, nil
}

// normalizeScalar 把 JSON 反序列化出来的数值归一化成引擎期望的整型。
//
// ⭐ 这一步不是可选的美化，而是**正确性**问题：
// 数据库里的 expect 值是 JSON，`encoding/json` 会把所有数字解成 float64。
// 若直接渲染，`200` 会变成 `200`（yaml 会写成 200，看似没事）—— 但
// 一旦是 `2.0` 之类，YAML 解析结果就是 float64，
// 而引擎内部用 reflect.DeepEqual 比较，`int(200) != float64(200)`，
// 断言会**假失败**。因此整数必须还原成 int。
func normalizeScalar(v any) any {
	switch t := v.(type) {
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) && math.Abs(t) < 1e15 {
			return int64(t)
		}
		return t
	case float32:
		return normalizeScalar(float64(t))
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return normalizeScalar(f)
		}
		return t.String()
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeScalar(val)
		}
		return out
	case jsonx.Map:
		return normalizeScalar(map[string]any(t))
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeScalar(val)
		}
		return out
	default:
		return v
	}
}

// ---------------------------------------------------------------------------
// 序列化
// ---------------------------------------------------------------------------

// marshalYAML 以固定缩进（4 空格）序列化，保证同样的输入永远产出逐字节相同的文件。
//
// 稳定性很重要：用例的 YAML 会被写进工作区、也会展示在编辑器里，
// 如果两次渲染结果不同，diff 就失去意义。
func marshalYAML(v any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// 输入辅助
// ---------------------------------------------------------------------------

// EnvGlobalHeaders 返回环境级全局请求头。
//
// 返回副本而非原 map：调用方会往里合并步骤级请求头，
// 直接改原 map 会污染后续用例（同一个 Environment 会被多个用例复用）。
func (in *Input) EnvGlobalHeaders() jsonx.Map {
	if in == nil || in.Env == nil || len(in.Env.GlobalHeaders) == 0 {
		return nil
	}
	out := make(jsonx.Map, len(in.Env.GlobalHeaders))
	for k, v := range in.Env.GlobalHeaders {
		out[k] = v
	}
	return out
}

// envVerify 返回用例的 TLS 校验开关。
//
// 默认值取自引擎实测：hrp 默认**开启**校验，本地 HTTP 环境必须显式关掉。
func envVerify(env *model.Environment) bool {
	if env == nil {
		return model.VerifySSLDefault
	}
	return env.VerifySSL
}

// mergeStringMap 合并两层 map，override 优先。
//
// 覆盖时按**大小写不敏感**匹配已有键：HTTP 头名不区分大小写，
// 若环境里写了 `content-type`、步骤里写了 `Content-Type`，
// 不去重就会同时出现两个头，引擎发出的请求会带重复头。
func mergeStringMap(base, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		for ek := range out {
			if ek != k && strings.EqualFold(ek, k) {
				delete(out, ek)
			}
		}
		out[k] = v
	}
	return out
}

// sortedKeys 返回排序后的键，用于产出稳定顺序的文本（.env）。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scalarToString 把任意标量转成文本，用于 .env 的取值。
func scalarToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// defaultString 返回非空字符串，为空时取默认值。
func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// ---------------------------------------------------------------------------
// 解码
// ---------------------------------------------------------------------------

// applyCaseConfig 把用例的 config JSON 展开到 yamlConfig。
//
// verify 的处理是有意为之：**环境配置优先**，只有用例 config JSON 里
// 显式出现了 verify 键时才允许覆盖。原因是用例 config 由前端表单生成，
// 未填写的字段反序列化后是零值（false），若无条件覆盖，
// 会让环境的 `verify_ssl: true` 被静默改成 false。
func applyCaseConfig(cfg *yamlConfig, tc *model.TestCase) error {
	if tc == nil || tc.Config.Val == nil {
		return nil
	}

	raw, err := json.Marshal(tc.Config.Val)
	if err != nil {
		return fmt.Errorf("用例 %s 的 config 无法序列化: %w", tc.Code, err)
	}

	var cc model.CaseConfig
	if err := json.Unmarshal(raw, &cc); err != nil {
		return fmt.Errorf("用例 %s 的 config 结构不合法: %w", tc.Code, err)
	}

	cfg.Variables = cc.Variables
	cfg.Parameters = cc.Parameters
	cfg.Headers = cc.Headers
	cfg.Export = cc.Export
	if cc.Weight > 0 {
		cfg.Weight = cc.Weight
	}

	// 只有显式给出 verify 才覆盖环境设置（见函数注释）。
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err == nil {
		if v, ok := probe["verify"]; ok {
			if b, ok := v.(bool); ok {
				cfg.Verify = b
			}
		}
	}
	return nil
}

// decodeRequest 把步骤的 request JSON 解成 RequestSpec。
func decodeRequest(a jsonx.Any) (model.RequestSpec, error) {
	var rs model.RequestSpec
	if a.Val == nil {
		return rs, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return rs, fmt.Errorf("request 无法序列化: %w", err)
	}
	if err := json.Unmarshal(b, &rs); err != nil {
		return rs, fmt.Errorf("request 结构不合法: %w", err)
	}
	rs.Method = strings.TrimSpace(rs.Method)
	rs.URL = strings.TrimSpace(rs.URL)
	rs.BodyType = strings.TrimSpace(strings.ToLower(rs.BodyType))
	return rs, nil
}

// decodeHooks 把步骤的 hooks JSON 解成 Hooks，无内容时返回 nil。
func decodeHooks(a jsonx.Any) (*model.Hooks, error) {
	if a.Val == nil {
		return nil, nil
	}
	b, err := json.Marshal(a.Val)
	if err != nil {
		return nil, fmt.Errorf("hooks 无法序列化: %w", err)
	}
	var h model.Hooks
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("hooks 结构不合法: %w", err)
	}
	if len(h.Setup) == 0 && len(h.Teardown) == 0 {
		return nil, nil
	}
	return &h, nil
}

// enabledSteps 过滤出启用的步骤并按 seq 升序排列。
//
// 引擎没有"跳过步骤"的语法，所以「临时禁用」只能在编译期剔除；
// 剔除后文件名与执行顺序都不变，用户看到的就是引擎实际会跑的。
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
