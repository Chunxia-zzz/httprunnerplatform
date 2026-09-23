// Package decompiler 把编译后的 YAML 反解析回结构化 CaseReq（M3 ③-c 源码视图可编辑）。
//
// 设计原则（与 compiler 的「单向」互补）：
//
//   - compiler 是 DB → YAML（正向、可再生的编译产物）；
//   - decompiler 是 YAML → CaseReq（逆向，仅用于「源码视图可编辑」这条人改路径）。
//
// 二者方向不同，但目标一致：**语义往返等价**。用户改了 YAML 保存后，
// 平台把 YAML 反解析回结构化数据落库，下次正向编译应产出「语义等价」的 YAML。
//
// 已知的信息损失（无法从编译产物还原的 DB 归属信息）：
//
//  1. 请求头三层归属：正向把「环境头 + 用例头 + 步骤头」合并下沉到
//     step.request.headers，反解析只能把这些合并后的头**整体**当作步骤头，
//     环境头/用例头的归属信息不可恢复 —— 这是「直接编辑编译产物」的固有代价。
//  2. verify 来源：正向渲染的 YAML 里 verify 永远是具体布尔值，无法区分
//     是「环境默认」还是「用例显式覆盖」。反解析统一当作「用例显式覆盖」写入，
//     语义上等价（最终生效的 verify 值不变）。
//
// 反解析失败必须带**行号**：用户在源码里改错了，报错要能直接跳到那一行。
package decompiler

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

// CaseReq 是反解析的目标结构，字段与 service.CaseReq 对齐。
//
// 刻意不 import service 包（避免循环依赖），只声明反解析需要填充的子集。
type CaseReq struct {
	Name        string          `json:"name"`
	Module      string          `json:"module"`
	Priority    string          `json:"priority"`
	Tags        string          `json:"tags"`
	Status      string          `json:"status"`
	Description string          `json:"description"`
	Config      jsonx.Any       `json:"config"`
	Steps       []StepReq       `json:"steps"`
	CaseTimeout int             `json:"case_timeout"`
}

// StepReq 是反解析出的单个步骤。
type StepReq struct {
	Seq      int        `json:"seq"`
	Name     string     `json:"name"`
	StepType string     `json:"step_type"`
	Request  *Request   `json:"request,omitempty"`
	Variables jsonx.Map `json:"variables,omitempty"`
	Extract  []ExtractItem `json:"extract"`
	Validate []AssertItem  `json:"validate"`
}

// Request 对应一个请求步骤的 request 段。
type Request struct {
	Method  string    `json:"method"`
	URL     string    `json:"url"`
	Headers jsonx.Map `json:"headers,omitempty"`
	Params  jsonx.Map `json:"params,omitempty"`
	Body    jsonx.Any `json:"body,omitempty"`
	BodyType string   `json:"body_type,omitempty"`
	Timeout int       `json:"timeout,omitempty"`
}

// ExtractItem 是反解析出的提取项。
type ExtractItem struct {
	Name       string `json:"name"`
	Object     string `json:"object"`
	Expression string `json:"expression"`
}

// AssertItem 是反解析出的断言。
type AssertItem struct {
	Check  string `json:"check"`
	Assert string `json:"assert"`
	Expect any    `json:"expect,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

// LineError 是带行号的解析错误，供前端高亮定位。
type LineError struct {
	Line    int
	Message string
}

func (e *LineError) Error() string {
	return fmt.Sprintf("第 %d 行：%s", e.Line, e.Message)
}

// ---------------------------------------------------------------------------
// YAML 中间结构（与 compiler/yaml.go 的 yamlTestCase 对齐，但用 Node 拿行号）
// ---------------------------------------------------------------------------

// Parse 把一段用例 YAML 反解析成 CaseReq。
func Parse(yamlText string) (*CaseReq, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return nil, &LineError{Line: 1, Message: fmt.Sprintf("YAML 语法错误：%v", err)}
	}
	if len(doc.Content) == 0 {
		return nil, &LineError{Line: 1, Message: "YAML 内容为空"}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &LineError{Line: root.Line, Message: "用例根节点必须是映射（config + teststeps）"}
	}

	out := &CaseReq{Steps: []StepReq{}}

	// 遍历根映射，找 config / teststeps
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		val := root.Content[i+1]
		switch key {
		case "config":
			if err := parseConfig(val, out); err != nil {
				return nil, err
			}
		case "teststeps":
			if err := parseSteps(val, out); err != nil {
				return nil, err
			}
		}
	}

	if out.Name == "" {
		return nil, &LineError{Line: root.Line, Message: "config 里缺少 name（引擎以 config.name 作唯一标识）"}
	}
	return out, nil
}

func parseConfig(n *yaml.Node, out *CaseReq) error {
	if n.Kind != yaml.MappingNode {
		return &LineError{Line: n.Line, Message: "config 必须是映射"}
	}
	cfg := map[string]any{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		switch key {
		case "name":
			out.Name = scalarString(val)
		case "variables":
			if val.Kind == yaml.MappingNode {
				cfg["variables"] = nodeToMap(val)
			}
		case "verify":
			cfg["verify"] = scalarBool(val)
		case "export":
			cfg["export"] = nodeToSlice(val)
		case "weight":
			cfg["weight"] = scalarInt(val)
		case "headers":
			// 正向从不写 config.headers（全部下沉到步骤），出现即视为用户手写，
			// 保留但提醒：它会在下次编译时再次下沉（语义可能被步骤头覆盖）。
			if val.Kind == yaml.MappingNode {
				cfg["headers"] = nodeToMap(val)
			}
		case "parameters":
			// 用户可能在源码里直接写了引擎参数化语法（绕开数据集），原样保留。
			if val.Kind == yaml.MappingNode {
				cfg["parameters"] = nodeToMap(val)
			}
		}
	}
	if len(cfg) > 0 {
		out.Config = jsonx.Any{Val: cfg}
	}
	return nil
}

func parseSteps(n *yaml.Node, out *CaseReq) error {
	if n.Kind != yaml.SequenceNode {
		return &LineError{Line: n.Line, Message: "teststeps 必须是序列"}
	}
	for idx, stepNode := range n.Content {
		st, err := parseStep(stepNode, idx+1)
		if err != nil {
			return err
		}
		out.Steps = append(out.Steps, *st)
	}
	if len(out.Steps) == 0 {
		return &LineError{Line: n.Line, Message: "teststeps 不能为空（引擎会静默丢弃空用例）"}
	}
	return nil
}

func parseStep(n *yaml.Node, seq int) (*StepReq, error) {
	if n.Kind != yaml.MappingNode {
		return nil, &LineError{Line: n.Line, Message: "每个步骤必须是映射"}
	}
	st := &StepReq{Seq: seq, StepType: "request", Extract: []ExtractItem{}, Validate: []AssertItem{}}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		switch key {
		case "name":
			st.Name = scalarString(val)
		case "variables":
			if val.Kind == yaml.MappingNode {
				st.Variables = jsonx.Map(nodeToMap(val))
			}
		case "request":
			req, err := parseRequest(val)
			if err != nil {
				return nil, err
			}
			st.Request = req
		case "extract":
			if err := parseExtract(val, st); err != nil {
				return nil, err
			}
		case "validate":
			if err := parseValidate(val, st); err != nil {
				return nil, err
			}
		case "setup_hooks", "teardown_hooks":
			// hooks 反解析为可选能力，M3 不做（编译器也不产出），静默忽略并保留
			// 不报错：用户手写 hooks 属于超范围，保存时会因编译器不支持而报错。
		}
	}
	if st.Request == nil {
		return nil, &LineError{Line: n.Line, Message: fmt.Sprintf("第 %d 步缺少 request 段", seq)}
	}
	if st.Name == "" {
		st.Name = fmt.Sprintf("步骤 %d", seq)
	}
	return st, nil
}

func parseRequest(n *yaml.Node) (*Request, error) {
	if n.Kind != yaml.MappingNode {
		return nil, &LineError{Line: n.Line, Message: "request 必须是映射"}
	}
	req := &Request{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		switch key {
		case "method":
			req.Method = strings.ToUpper(strings.TrimSpace(scalarString(val)))
		case "url":
			req.URL = strings.TrimSpace(scalarString(val))
		case "params":
			if val.Kind == yaml.MappingNode {
				req.Params = jsonx.Map(nodeToMap(val))
			}
		case "headers":
			if val.Kind == yaml.MappingNode {
				req.Headers = jsonx.Map(nodeToMap(val))
			}
		case "timeout":
			req.Timeout = scalarInt(val)
		case "json":
			req.BodyType = model.BodyTypeJSON
			req.Body = jsonx.Any{Val: nodeToAny(val)}
		case "data":
			// data 可能是字符串（raw）也可能是映射（form）。
			// 正向：form → data 映射 + form Content-Type；raw → data 字符串。
			if val.Kind == yaml.ScalarNode && val.Tag == "!!str" {
				req.BodyType = model.BodyTypeRaw
				req.Body = jsonx.Any{Val: val.Value}
			} else {
				req.BodyType = model.BodyTypeForm
				req.Body = jsonx.Any{Val: nodeToAny(val)}
			}
		}
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	return req, nil
}

// parseExtract 把 `name: "object.expr"` 拆回 {name, object, expression}。
func parseExtract(n *yaml.Node, st *StepReq) error {
	if n.Kind != yaml.MappingNode {
		return &LineError{Line: n.Line, Message: "extract 必须是映射（变量名 → 提取表达式）"}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		name := n.Content[i].Value
		expr := scalarString(n.Content[i+1])
		obj, path := splitExtractExpr(expr)
		st.Extract = append(st.Extract, ExtractItem{Name: name, Object: obj, Expression: path})
	}
	return nil
}

// splitExtractExpr 逆向 renderExtractExpr：`body.args.token` → (body, args.token)。
func splitExtractExpr(expr string) (object, path string) {
	expr = strings.TrimSpace(expr)
	// 标量对象（status_code / proto）无子路径
	switch expr {
	case model.ExtractStatusCode, model.ExtractProto:
		return expr, ""
	}
	dot := strings.Index(expr, ".")
	if dot < 0 {
		// 无点：可能是 headers/cookies/body 这类对象名，也可能是自定义
		return expr, ""
	}
	return expr[:dot], expr[dot+1:]
}

func parseValidate(n *yaml.Node, st *StepReq) error {
	if n.Kind != yaml.SequenceNode {
		return &LineError{Line: n.Line, Message: "validate 必须是序列"}
	}
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode || len(item.Content) < 2 {
			return &LineError{Line: item.Line, Message: "每条断言必须是「方法: [检查表达式, 期望值]」的单键映射"}
		}

		// 两种形态（实测 A23）：
		//   紧凑（平台渲染）：- eq: [status_code, 200]        → 单键映射
		//   字典（hrp convert）：- {check: ..., assert: equals, expect: ..., msg: ...}
		// 用「同时含 check 与 assert 键」区分 —— check/assert 都不是合法的
		// 校验器方法名，同时出现只可能是字典形态，不会误伤紧凑写法。
		var check, method, msg string
		var expect any
		hasCheck, hasAssert := false, false
		for i := 0; i+1 < len(item.Content); i += 2 {
			switch item.Content[i].Value {
			case "check":
				hasCheck = true
			case "assert":
				hasAssert = true
			}
		}

		if hasCheck && hasAssert {
			for i := 0; i+1 < len(item.Content); i += 2 {
				key := item.Content[i].Value
				val := item.Content[i+1]
				switch key {
				case "check":
					check = scalarString(val)
				case "assert":
					method = scalarString(val)
				case "expect":
					expect = nodeToAny(val)
				case "msg":
					msg = scalarString(val)
				}
			}
			if method == "" {
				return &LineError{Line: item.Line, Message: "字典形态的断言缺少 assert 方法名"}
			}
			st.Validate = append(st.Validate, AssertItem{Check: check, Assert: method, Expect: expect, Msg: msg})
			continue
		}

		method = item.Content[0].Value
		args := item.Content[1]
		if args.Kind != yaml.SequenceNode || len(args.Content) < 1 || len(args.Content) > 2 {
			return &LineError{Line: item.Line, Message: fmt.Sprintf("断言 %q 的参数必须是 [检查表达式, 期望值]（1~2 个元素）", method)}
		}
		check = scalarString(args.Content[0])
		if len(args.Content) == 2 {
			expect = nodeToAny(args.Content[1])
		}
		st.Validate = append(st.Validate, AssertItem{Check: check, Assert: method, Expect: expect})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Node 辅助
// ---------------------------------------------------------------------------

func scalarString(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func scalarBool(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	switch strings.ToLower(n.Value) {
	case "true", "yes", "on", "1":
		return true
	default:
		return false
	}
}

func scalarInt(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	var v int
	_, _ = fmt.Sscanf(n.Value, "%d", &v)
	return v
}

// nodeToAny 把 yaml.Node 转成 any（保留数字为 int/float，避免全变字符串）。
func nodeToAny(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int":
			var v int64
			_, _ = fmt.Sscanf(n.Value, "%d", &v)
			return v
		case "!!float":
			var v float64
			_, _ = fmt.Sscanf(n.Value, "%f", &v)
			return v
		case "!!bool":
			return scalarBool(n)
		case "!!null":
			return nil
		default:
			return n.Value
		}
	case yaml.MappingNode:
		return nodeToMap(n)
	case yaml.SequenceNode:
		return nodeToSlice(n)
	default:
		return n.Value
	}
}

func nodeToMap(n *yaml.Node) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		m[n.Content[i].Value] = nodeToAny(n.Content[i+1])
	}
	return m
}

func nodeToSlice(n *yaml.Node) []any {
	s := make([]any, 0, len(n.Content))
	for _, c := range n.Content {
		s = append(s, nodeToAny(c))
	}
	return s
}
