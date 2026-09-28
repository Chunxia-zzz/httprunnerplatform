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

	case check == model.ExtractBody || check == "body" ||
		strings.HasPrefix(check, "body.") || strings.HasPrefix(check, "body["):
		root, err := decodeBody(resp)
		if err != nil {
			return "", "", false, "响应体不是合法 JSON，无法按路径取值：" + err.Error()
		}
		// `body.data[0].name` / `body["data"][0]` 都要剥掉 `body` 前缀。
		// 先剥 `body`，再剥可能紧跟的一个点号（`body.` 形态）。
		// 方括号形态（`body[`）剥掉 `body` 后剩下的 `[...]` 正好是下一段的起点。
		path := strings.TrimPrefix(check, "body")
		path = strings.TrimPrefix(path, ".")
		if path == "" {
			return renderValueOK(root)
		}
		v, found := lookupPath(root, splitPathExpr(path))
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
//
// ⚠️ 但"响应体不是干净 JSON"有两种截然不同的成因，必须分开对待：
//   - 服务端返回的本来就不是 JSON（HTML 错误页、纯文本）→ 真的无法按路径取值；
//   - **传输层封帧**：httpx 打印的是原始报文，chunked 响应的体会带上
//     十六进制块长度行（如 `4904`）和结尾的 `0`，JSON 本体被夹在中间。
//
// 第二种在验收现场真实踩到过（BUG-001 的第二层成因，见 docs/验收问题记录.md）：
// 体首的 `4904` 会被 json.Decode **当成一个裸数字解析成功**，
// 于是 root 变成 json.Number，所有 `body.xxx` 路径全部取不到值 ——
// 症状与"数组下标解析失败"完全一致（页面上都是"实际值 (nil)"），
// 但根因完全不同，所以这里必须单独兜住。
//
// 做法：如果响应体不是以 `{` / `[` 开头，就先尝试抠出最外层的 JSON 值。
func decodeBody(resp *ResponseSnapshot) (any, error) {
	raw := strings.TrimSpace(resp.Body)
	if raw == "" {
		return nil, fmt.Errorf("响应体为空")
	}

	// 绝大多数情况：本来就以 JSON 容器的花括号/方括号开头，直接解析。
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		return decodeJSON(raw)
	}

	// 被别的东西包着（chunked 封帧、前缀噪声）：先抠出 JSON 本体。
	if inner, ok := extractJSONValue(raw); ok {
		if v, err := decodeJSON(inner); err == nil {
			return v, nil
		}
	}

	// 兜底：直接解析原文，把真实错误抛出去
	// （例如响应体就是一个裸标量 `123`，或真的不是 JSON）。
	return decodeJSON(raw)
}

// decodeJSON 用 UseNumber 解析一段 JSON 文本。
//
// UseNumber 是刻意的：整型必须与引擎一样报成 int64，
// 否则 `200` 会变成 float64，UI 上「平台重建」和「引擎给出」的类型名会对不上。
func decodeJSON(s string) (any, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// extractJSONValue 从夹杂噪声的文本里抠出最外层的 JSON 对象/数组。
//
// 取「第一个 `{` 或 `[`」到「最后一个配对的 `}` 或 `]`」。
// 这一步对所有"JSON 被前缀/后缀噪声包着"的情况都够用，
// 而不必去实现一个完整的 HTTP chunked 解码器 —— 后者的收益比很低：
// 一是不必关心每个分块的十六进制长度，二是天然容忍开头的封帧行与结尾的 `0`。
//
// 抠不出（没有起始括号，或括号不配对）时返回 ok=false，
// 由调用方决定怎么报错 —— 不在这一层掩盖错误。
func extractJSONValue(raw string) (string, bool) {
	start := -1
	var closer byte

	if i := strings.IndexByte(raw, '{'); i >= 0 {
		start, closer = i, '}'
	}
	if i := strings.IndexByte(raw, '['); i >= 0 && (start < 0 || i < start) {
		start, closer = i, ']'
	}
	if start < 0 {
		return "", false
	}

	end := strings.LastIndexByte(raw, closer)
	if end <= start {
		return "", false
	}
	return raw[start : end+1], true
}

// splitPathExpr 把取值表达式切成「键/下标」段序列。
//
// 为什么不能再用 strings.Split(expr, ".")：
// 断言里的 check 字段最终由**引擎的 jmespath** 求值，而 jmespath 规定
// 数组下标必须写成方括号（`body.data[0].name`）。平台若仍按点号切分，
// 会把它切成 ["data[0]", "name"]，然后在 map 里找键 "data[0]" —— 永远找不到，
// 于是"实际值"恒为 nil。
//
// 这一点是**实测**出来的（见 docs/验收问题记录.md 的 BUG-001）：期望值写错时
// 引擎 panic、没有 summary，断言明细只能由平台用响应快照重建；而重建在数组
// 下标场景下报不出真实值，恰好击穿了平台"不假绿、告诉你哪里不符预期"的核心承诺。
//
// 同时保留对这三种写法的兼容：
//   - `data[0].name`  jmespath 标准写法（引擎唯一接受的数组下标形式）
//   - `data.0.name`   纯数字段（平台早期写法；引擎会语法报错，但解析器不因此崩）
//   - `[0].name`      根节点本身就是数组
//
// 以及 jmespath 的方括号引号键 `data["a.b"]`（内部含点号也能正确当一段）。
func splitPathExpr(expr string) []string {
	segs := make([]string, 0, 4)
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
		}
	}

	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '.':
			flush()
		case '[':
			flush()
			// 找到配对的 ']'；找不到就吃到结尾（容错，不 panic）。
			j := i + 1
			for j < len(expr) && expr[j] != ']' {
				j++
			}
			inner := strings.TrimSpace(expr[i+1 : j])
			if len(inner) >= 2 {
				// `["a.b"]` / `['a.b']`：剥掉引号，内部的点号不再是分隔符。
				if (inner[0] == '"' && inner[len(inner)-1] == '"') ||
					(inner[0] == '\'' && inner[len(inner)-1] == '\'') {
					inner = inner[1 : len(inner)-1]
				}
			}
			if inner != "" {
				segs = append(segs, inner)
			}
			i = j
		case ']':
			// 孤立的 ']' 忽略掉，避免产生空段。
		default:
			cur.WriteByte(expr[i])
		}
	}
	flush()
	return segs
}

// lookupPath 沿切好的段序列取值，支持 map 键与数组下标。
//
// 段是"键"还是"下标"由**当前节点的类型**决定（而不是由段的字面形态决定）：
// map 就按键查，数组就按数字转下标。这样 `["data", "0", "name"]` 与
// `["data", "name"]` 两种切法都能正确落到同一处。
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
