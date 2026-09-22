package parser

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// 断言求值的结果说明：平台能重建、还是无能为力。
const (
	noteUnevaluable = "平台无法重建该校验器（仅引擎支持）"
	noteNoSnapshot  = "该步骤没有可用的响应快照，无法重建断言"
	noteNotReached  = "未执行到该断言（引擎在前一条断言失败时中止）"
)

// rebuildAssertions 用「声明的断言 + stdout 响应快照」重建断言明细。
//
// 这是 F8 的补偿机制：引擎在断言失败时会 panic，
// 既不产出 summary.json，stderr 里也没有任何断言明细。
// 若不重建，用户在页面上就只能看到"失败了"而看不到"哪里不符预期"——
// 而这恰恰是平台最核心的价值。
//
// 语义约定：
//   - 逐条按序求值，与引擎的执行顺序一致（引擎在第一条失败处 panic）；
//   - 第一条不通过的即为失败断言；
//   - 其后的断言标为"未执行到"；
//   - 全部标 Rebuilt=true，前端必须显式标注来源。
func rebuildAssertions(items []model.AssertItem, resp *ResponseSnapshot) []AssertionOutcome {
	out := make([]AssertionOutcome, 0, len(items))

	failed := false
	for i, it := range items {
		ao := AssertionOutcome{
			Seq:          i + 1,
			CheckExpr:    it.Check,
			AssertMethod: it.Assert,
			Rebuilt:      true,
		}
		ao.ExpectValue, ao.ExpectValueType = renderValue(it.Expect)

		if failed {
			ao.Passed = false
			ao.Msg = noteNotReached
			out = append(out, ao)
			continue
		}

		if resp == nil {
			ao.Passed = false
			ao.Msg = noteNoSnapshot
			out = append(out, ao)
			failed = true
			continue
		}

		actual, actualType, evaluable, note := evalOne(it, resp)
		if !evaluable {
			ao.Passed = false
			ao.Msg = note
			out = append(out, ao)
			// 无法判定时不再向后推断，避免把后续断言误标为"未执行到"。
			failed = true
			continue
		}

		ao.CheckValue = actual
		ao.CheckValueType = actualType

		passed, err := compare(it.Assert, actual, it.Expect)
		ao.Passed = passed
		switch {
		case err != nil:
			ao.Passed = false
			ao.Msg = "平台重建失败：" + err.Error()
			failed = true
		case !passed:
			if it.Msg != "" {
				ao.Msg = it.Msg
			} else {
				ao.Msg = fmt.Sprintf("期望 %s，实际 %s", ao.ExpectValue, ao.CheckValue)
			}
			failed = true
		}
		out = append(out, ao)
	}
	return out
}

// evalOne 取出一条断言的实际值。
//
// 返回值 ok=false 且 note 非空表示平台无法求值（不是"断言不通过"）。
func evalOne(it model.AssertItem, resp *ResponseSnapshot) (actual string, actualType string, ok bool, note string) {
	check := strings.TrimSpace(it.Check)
	if check == "" {
		return "", "", false, "断言缺少检查表达式"
	}

	switch {
	case check == model.ExtractStatusCode || check == "status_code":
		return renderValueOK(resp.StatusCode)

	case check == model.ExtractProto || check == "proto":
		return renderValueOK(resp.Proto)

	case strings.HasPrefix(check, "headers."):
		name := strings.TrimPrefix(check, "headers.")
		if v, found := lookupHeader(resp.Headers, name); found {
			return renderValueOK(v)
		}
		// 头不存在：与引擎行为一致，交给比较器判断（通常是 ne 会通过）。
		return renderValueOK(nil)

	case strings.HasPrefix(check, "cookies."):
		name := strings.TrimPrefix(check, "cookies.")
		if v, found := lookupCookie(resp.Headers, name); found {
			return renderValueOK(v)
		}
		return renderValueOK(nil)

	case check == model.ExtractBody || check == "body" || strings.HasPrefix(check, "body."):
		root, err := decodeBody(resp)
		if err != nil {
			return "", "", false, "响应体不是合法 JSON，无法按路径取值：" + err.Error()
		}
		path := strings.TrimPrefix(strings.TrimPrefix(check, "body"), ".")
		if path == "" {
			return renderValueOK(root)
		}
		v, found := lookupPath(root, strings.Split(path, "."))
		if !found {
			// 路径不存在：引擎在这种情况下也会给出 nil 值参与比较。
			return renderValueOK(nil)
		}
		return renderValueOK(v)

	default:
		return "", "", false, noteUnevaluable
	}
}

// renderValueOK 是 renderValue 的求值成功包装。
func renderValueOK(v any) (string, string, bool, string) {
	text, typeName := renderValue(v)
	return text, typeName, true, ""
}

// lookupHeader 大小写不敏感地查头。
func lookupHeader(headers map[string]string, name string) (string, bool) {
	if v, ok := headers[name]; ok {
		return v, true
	}
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// lookupCookie 从 Set-Cookie 头里取指定 cookie（简化解析：取 name=value 对）。
func lookupCookie(headers map[string]string, name string) (string, bool) {
	var raw strings.Builder
	for k, v := range headers {
		if strings.EqualFold(k, "Set-Cookie") {
			raw.WriteString(v)
			raw.WriteString("\n")
		}
	}
	for _, seg := range strings.Split(raw.String(), "\n") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		first := strings.SplitN(seg, ";", 2)[0]
		k, v, ok := strings.Cut(first, "=")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// decodeBody 把响应体解析成 JSON。
//
// 引擎在断言 body.* 时也是把 body 当 JSON 解析的；
// 解析失败说明用例的断言方式与服务端返回不匹配，属于用例问题。
func decodeBody(resp *ResponseSnapshot) (any, error) {
	body := strings.TrimSpace(resp.Body)
	if body == "" {
		return nil, fmt.Errorf("响应体为空")
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// lookupPath 沿点号路径取值，支持 map 键与数组下标。
func lookupPath(root any, path []string) (any, bool) {
	cur := root
	for _, seg := range path {
		if seg == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// renderValue 把任意值渲染成展示字符串，并给出引擎风格的类型名。
//
// 类型名刻意与引擎保持一致（int64 / string / []interface{} 等），
// 这样"平台重建"与"引擎给出"的结果在 UI 上不会出现两套类型叫法。
func renderValue(v any) (text string, typeName string) {
	switch t := v.(type) {
	case nil:
		return "", "nil"
	case string:
		return t, "string"
	case bool:
		return strconv.FormatBool(t), "bool"
	case json.Number:
		return t.String(), jsonNumberType(t)
	case float64:
		// 整数型必须报成 int64：引擎把 YAML 里的 `200` 解析为 int64，
		// 日志里的 expectValueType 也是 "int64"（实测）。
		// 若这里报 "float64"，「平台重建」与「引擎给出」的同一断言
		// 在 UI 上会显示成两种类型，用户会以为平台算错了。
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10), "int64"
		}
		return strconv.FormatFloat(t, 'f', -1, 64), "float64"
	case int:
		// 与 int64 统一：引擎侧所有整数都叫 int64，
		// 平台内部用 int 只是 Go 的习惯，不该泄漏到用户可见的类型名里。
		return strconv.Itoa(t), "int64"
	case int64:
		return strconv.FormatInt(t, 10), "int64"
	case []any:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t), "[]interface {}"
		}
		return string(b), "[]interface {}"
	case map[string]any:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t), "map[string]interface {}"
		}
		return string(b), "map[string]interface {}"
	default:
		return fmt.Sprintf("%v", t), fmt.Sprintf("%T", t)
	}
}

// jsonNumberType 推断 json.Number 的引擎侧类型名。
func jsonNumberType(n json.Number) string {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		return "int64"
	}
	return "float64"
}

// compare 按校验器方法比较实际值与期望值。
//
// 覆盖引擎内置的全部校验器（唯一未实现的是 contained_by，语义较偏且实际很少用）。
func compare(method string, actual string, expect any) (bool, error) {
	m := strings.ToLower(strings.TrimSpace(method))

	switch m {
	case "eq", "equals", "equal":
		return looseEqual(actual, expect), nil

	case "ne", "not_equal":
		return !looseEqual(actual, expect), nil

	case "str_eq":
		return actual == plainString(expect), nil

	case "contains":
		return strings.Contains(actual, plainString(expect)), nil

	case "startswith", "start_with":
		return strings.HasPrefix(actual, plainString(expect)), nil

	case "endswith", "end_with":
		return strings.HasSuffix(actual, plainString(expect)), nil

	case "regex_match":
		re, err := regexp.Compile(plainString(expect))
		if err != nil {
			return false, fmt.Errorf("正则表达式非法: %w", err)
		}
		return re.MatchString(actual), nil

	case "type_match":
		// 引擎语义：compare expect 的类型名与 actual 的类型名。
		return typeNameOf(expect) == typeNameOfInferred(actual, expect), nil

	case "lt", "le", "gt", "ge":
		a, aok := toFloat(actual)
		e, eok := toFloat(expect)
		if !aok || !eok {
			return false, fmt.Errorf("数值比较需要两侧都是数字（实际 %q）", actual)
		}
		switch m {
		case "lt":
			return a < e, nil
		case "le":
			return a <= e, nil
		case "gt":
			return a > e, nil
		default:
			return a >= e, nil
		}

	case "len_eq", "len_gt", "len_ge", "len_lt", "len_le":
		n := lengthOf(actual)
		if n < 0 {
			return false, fmt.Errorf("长度比较需要字符串/数组/对象，实际为 %q", actual)
		}
		e, ok := toFloat(expect)
		if !ok {
			return false, fmt.Errorf("长度比较的期望值必须是数字")
		}
		switch m {
		case "len_eq":
			return float64(n) == e, nil
		case "len_gt":
			return float64(n) > e, nil
		case "len_ge":
			return float64(n) >= e, nil
		case "len_lt":
			return float64(n) < e, nil
		default:
			return float64(n) <= e, nil
		}

	default:
		return false, fmt.Errorf("平台不支持重建校验器 %q", method)
	}
}

// ValidAssertMethods 是引擎支持的全部校验器方法，供校验器与前端下拉使用。
var ValidAssertMethods = []string{
	"eq", "equals", "equal",
	"lt", "le", "gt", "ge", "ne",
	"str_eq",
	"len_eq", "len_gt", "len_ge", "len_lt", "len_le",
	"contains", "contained_by",
	"type_match", "regex_match",
	"startswith", "endswith",
}

// looseEqual 做数值容错的相等比较。
//
// 为什么要容错：引擎日志里的 actual 是渲染后的字符串，而 expect 可能是 int64/float64。
// 直接字符串比较会让 `200` vs `200.0` 变成不相等。
func looseEqual(actual string, expect any) bool {
	expStr := plainString(expect)
	if actual == expStr {
		return true
	}

	// 期望值为 nil 时，平台侧渲染为空串，视为相等。
	if expect == nil && actual == "" {
		return true
	}

	af, aok := toFloat(actual)
	ef, eok := toFloat(expect)
	if aok && eok {
		return math.Abs(af-ef) < 1e-9
	}

	// 布尔与字符串的宽松比较
	if b, ok := expect.(bool); ok {
		return strconv.FormatBool(b) == strings.ToLower(actual)
	}
	return false
}

// plainString 把任意值渲染成用于比较的字符串。
func plainString(v any) string {
	s, _ := renderValue(v)
	return s
}

// toFloat 尽力把值转成数字。
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// lengthOf 返回字符串/数组/对象的长度，不支持则返回 -1。
func lengthOf(actual string) int {
	var v any
	dec := json.NewDecoder(strings.NewReader(actual))
	dec.UseNumber()
	if err := dec.Decode(&v); err == nil {
		switch t := v.(type) {
		case []any:
			return len(t)
		case map[string]any:
			return len(t)
		case string:
			return len([]rune(t))
		}
	}
	// 不是合法 JSON 时退化为字符串长度（与引擎对字符串的处理一致）。
	return len([]rune(actual))
}

// typeNameOf 返回值的引擎风格类型名。
func typeNameOf(v any) string {
	_, t := renderValue(v)
	return t
}

// typeNameOfInferred 推断 actual 的类型名。
//
// 因为 actual 已被渲染成字符串，这里做一次还原：
// 能解析成数字则按数字类型，否则按字符串。
func typeNameOfInferred(actual string, expect any) string {
	if _, ok := toFloat(expect); ok {
		if f, ok := toFloat(actual); ok {
			if f == math.Trunc(f) {
				return "int64"
			}
			return "float64"
		}
	}
	if _, ok := expect.(bool); ok {
		return "bool"
	}
	return "string"
}
