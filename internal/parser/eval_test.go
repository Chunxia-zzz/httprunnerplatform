package parser

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// ---------------------------------------------------------------------------
// BUG-001：数组下标断言的"实际值"恒为空
//
// 背景（真实验收复现）：用例写 `body.data[0].name`，期望值故意写错后
// 引擎 panic、不产出 summary.json，断言明细只能由平台用响应快照重建。
// 而重建出的「实际值」是 (nil) —— 真实值是 Bitcoin。
//
// 根因：evalOne 用 strings.Split(path, ".") 切路径，`data[0]` 被切成一个整段
// "data[0]"，在 map 里当然找不到这个键。
// 这恰好击穿平台最核心的承诺：断言失败时告诉你"哪里不符预期"。
//
// 修复：新增 splitPathExpr，按 jmespath 语义切分（方括号下标、引号键、纯数字段）。
// ---------------------------------------------------------------------------

// TestSplitPathExpr_切分规则 锁定取值表达式的分段语义。
//
// 引擎的 check 字段走的是 **jmespath**，所以这里的切分规则必须与 jmespath 对齐，
// 而不是"平台自己看着像就行"。
func TestSplitPathExpr_切分规则(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want []string
	}{
		{"jmespath 方括号下标（引擎唯一接受的形式）", "data[0].name", []string{"data", "0", "name"}},
		{"纯数字段（平台早期写法，保留兼容）", "data.0.name", []string{"data", "0", "name"}},
		{"根节点就是数组", "[0].name", []string{"0", "name"}},
		{"连续下标（二维）", "data[0][1]", []string{"data", "0", "1"}},
		{"纯点号路径不受影响", "data.metadata.total", []string{"data", "metadata", "total"}},
		{"单个键", "status_code", []string{"status_code"}},
		{"末段带下标", "data[2]", []string{"data", "2"}},
		{"空下标容错（不产生空段）", "data[]", []string{"data"}},
		{"引号键内部的点号不是分隔符", `data["a.b"]`, []string{"data", "a.b"}},
		{"引号键 + 下标", `body["data"][0]["name"]`, []string{"body", "data", "0", "name"}},
		{"尾随点号不产生空段", "data.", []string{"data"}},
		{"连续点号不产生空段", "a..b", []string{"a", "b"}},
		{"空表达式", "", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitPathExpr(c.expr)
			if len(got) != len(c.want) {
				t.Fatalf("splitPathExpr(%q) = %v, want %v", c.expr, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("splitPathExpr(%q) = %v, want %v", c.expr, got, c.want)
				}
			}
		})
	}
}

// TestSplitPathExpr_不因畸形输入崩溃：解析器跑在"用户随便写"的输入上，
// 任何切分函数都不允许 panic（断言写错是常态，不是异常）。
func TestSplitPathExpr_不因畸形输入崩溃(t *testing.T) {
	for _, expr := range []string{
		"[", "]", "[[", "]]", "data[", "data]", `data["`, `data["]`, "[0", "0]", ".", "..", "[[]]",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("splitPathExpr(%q) panic: %v", expr, r)
				}
			}()
			_ = splitPathExpr(expr)
		}()
	}
}

// listingBody 是 run 61 真实响应体的缩小版（保留数组下标这个关键形态）。
const listingBody = `{
  "data": [
    {"id": "1", "name": "Bitcoin", "symbol": "BTC"},
    {"id": "2", "name": "Litecoin", "symbol": "LTC"}
  ],
  "metadata": {"num_cryptocurrencies": 2}
}`

// runOneAssert 用给定响应体走一遍断言重建，返回那一条明细。
func runOneAssert(t *testing.T, body string, item model.AssertItem) AssertionOutcome {
	t.Helper()
	out := rebuildAssertions([]model.AssertItem{item}, &ResponseSnapshot{
		StatusCode: 200,
		Body:       body,
	})
	if len(out) != 1 {
		t.Fatalf("断言明细数 = %d, want 1", len(out))
	}
	return out[0]
}

// TestRebuildAssertions_数组下标能取到真实值 是 BUG-001 的核心回归。
func TestRebuildAssertions_数组下标能取到真实值(t *testing.T) {
	cases := []struct {
		name       string
		check      string
		method     string
		expect     any
		wantValue  string
		wantType   string
		wantPassed bool
	}{
		{
			// 这就是验收现场那条断言：期望写错，平台必须报出真实值 Bitcoin
			name: "数组下标 + 期望写错（验收现场场景）", check: "body.data[0].name", method: "eq",
			expect: "BitcoinXXX", wantValue: "Bitcoin", wantType: "string", wantPassed: false,
		},
		{
			name: "数组下标 + 期望正确", check: "body.data[0].name", method: "eq",
			expect: "Bitcoin", wantValue: "Bitcoin", wantType: "string", wantPassed: true,
		},
		{
			name: "取第二个元素", check: "body.data[1].symbol", method: "eq",
			expect: "LTC", wantValue: "LTC", wantType: "string", wantPassed: true,
		},
		{
			name: "纯数字段写法保持兼容", check: "body.data.0.name", method: "eq",
			expect: "Bitcoin", wantValue: "Bitcoin", wantType: "string", wantPassed: true,
		},
		{
			name: "引号键写法（body[ 前缀也要能进解析分支）", check: `body["data"][0]["name"]`, method: "eq",
			expect: "Bitcoin", wantValue: "Bitcoin", wantType: "string", wantPassed: true,
		},
		{
			name: "对数组取长度", check: "body.data", method: "len_eq",
			expect: float64(2), wantValue: `[{"id":"1","name":"Bitcoin","symbol":"BTC"},{"id":"2","name":"Litecoin","symbol":"LTC"}]`,
			wantType: "[]interface {}", wantPassed: true,
		},
		{
			name: "嵌套对象的数字字段", check: "body.metadata.num_cryptocurrencies", method: "eq",
			expect: float64(2), wantValue: "2", wantType: "int64", wantPassed: true,
		},
		{
			// 越界不是"平台无能为力"，而是"路径不存在"：引擎此时也拿 nil 参与比较。
			// 关键是**不能**因此把整条断言标成"无法重建"。
			name: "下标越界按路径不存在处理", check: "body.data[9].name", method: "eq",
			expect: "Bitcoin", wantValue: "", wantType: "nil", wantPassed: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			method := c.method
			if method == "" {
				method = "eq"
			}
			got := runOneAssert(t, listingBody, model.AssertItem{
				Check: c.check, Assert: method, Expect: c.expect,
			})
			if !got.Rebuilt {
				t.Error("平台重建的断言必须标 Rebuilt=true")
			}
			if got.CheckValue != c.wantValue {
				t.Errorf("实际值 = %q, want %q（这就是 BUG-001 的症状：空串代表平台没解析出真实值）",
					got.CheckValue, c.wantValue)
			}
			if got.CheckValueType != c.wantType {
				t.Errorf("实际值类型 = %q, want %q", got.CheckValueType, c.wantType)
			}
			if got.Passed != c.wantPassed {
				t.Errorf("结论 = %v, want %v", got.Passed, c.wantPassed)
			}
			if !c.wantPassed && !strings.Contains(got.Msg, "期望") {
				t.Errorf("失败断言应给出可读原因，实际 %q", got.Msg)
			}
		})
	}
}

// TestRebuildAssertions_根是数组时也能取下标：`[0].name` 这种形态
// （响应体顶层就是数组）以前会被切成 ["[0]", "name"] 而全军覆没。
func TestRebuildAssertions_根是数组时也能取下标(t *testing.T) {
	body := `[{"name": "Bitcoin"}, {"name": "Litecoin"}]`
	got := runOneAssert(t, body, model.AssertItem{
		Check: "body[0].name", Assert: "eq", Expect: "Bitcoin",
	})
	if got.CheckValue != "Bitcoin" {
		t.Fatalf("实际值 = %q, want Bitcoin", got.CheckValue)
	}
	if !got.Passed {
		t.Error("断言应通过")
	}
}

// TestRebuildAssertions_数组下标失败时给出可读原因：失败信息里必须同时出现
// 期望值与实际值，否则用户仍然只能看到"失败了"而看不到"哪里不符预期"。
func TestRebuildAssertions_数组下标失败时给出可读原因(t *testing.T) {
	got := runOneAssert(t, listingBody, model.AssertItem{
		Check: "body.data[0].name", Assert: "eq", Expect: "以太坊",
	})
	if got.Passed {
		t.Fatal("期望值与实际值不同，不应通过")
	}
	if !strings.Contains(got.Msg, "以太坊") || !strings.Contains(got.Msg, "Bitcoin") {
		t.Errorf("失败原因应同时含期望与实际，实际 %q", got.Msg)
	}
}

// TestRebuildAssertions_多断言时数组下标失败会中止后续 锁住"与引擎执行顺序一致"。
//
// 引擎在第一条失败断言处 panic（实测 F8），重建立刻标后续为"未执行到"，
// 否则会给出比引擎更强的判定，误导用户。
func TestRebuildAssertions_多断言时数组下标失败会中止后续(t *testing.T) {
	out := rebuildAssertions([]model.AssertItem{
		{Check: "body.data[0].name", Assert: "eq", Expect: "BitcoinXXX"}, // 这条失败
		{Check: "body.data[1].name", Assert: "eq", Expect: "Litecoin"},   // 不该被求值
	}, &ResponseSnapshot{StatusCode: 200, Body: listingBody})

	if len(out) != 2 {
		t.Fatalf("断言明细数 = %d, want 2", len(out))
	}
	if out[0].Passed {
		t.Error("第 1 条应失败")
	}
	if out[0].CheckValue != "Bitcoin" {
		t.Errorf("第 1 条实际值 = %q, want Bitcoin", out[0].CheckValue)
	}
	if out[1].Passed || out[1].CheckValue != "" {
		t.Errorf("第 2 条应标为未执行到，实际 %+v", out[1])
	}
	if out[1].Msg != noteNotReached {
		t.Errorf("第 2 条的说明 = %q, want %q", out[1].Msg, noteNotReached)
	}
}

// ---------------------------------------------------------------------------
// BUG-001 第二层成因：chunked 响应体的原始报文体不是干净 JSON
//
// httpx 打印的是**原始报文**，chunked 响应体会带上下面的封帧：
//
//	4904          ← 十六进制块长度（这就是"原始"的样子）
//	{ "data": [ ... ] }
//	0             ← 结束块
//
// 危害比"数组下标解析不了"更隐蔽：`json.Decode` 会把首行的 `4904`
// **当成一个裸数字解析成功**，于是 root 变成 json.Number，
// 所有 `body.xxx` 断言全部取不到值，页面上同样是"实际值 (nil)"。
// ---------------------------------------------------------------------------

// TestDecodeBody_剥离chunked封帧：把块长度行和结尾的 0 都剥掉后再解析。
func TestDecodeBody_剥离chunked封帧(t *testing.T) {
	chunked := "4904\n" + strings.TrimSpace(listingBody) + "\n0"

	got := runOneAssert(t, chunked, model.AssertItem{
		Check: "body.data[0].name", Assert: "eq", Expect: "Bitcoin",
	})
	if got.CheckValue != "Bitcoin" {
		t.Fatalf("实际值 = %q, want Bitcoin（chunked 封帧没被剥掉）", got.CheckValue)
	}
	if !got.Passed {
		t.Error("断言应通过")
	}

	// 对照组：确保不是"任何字符串都能解析成功"这种偷懒实现。
	// 一个真正不是 JSON 的响应体必须照旧报"无法按路径取值"。
	html := runOneAssert(t, "<html><body>502 Bad Gateway</body></html>", model.AssertItem{
		Check: "body.data[0].name", Assert: "eq", Expect: "Bitcoin",
	})
	if !strings.Contains(html.Msg, "无法按路径取值") {
		t.Errorf("非 JSON 响应体的说明 = %q, 应指出无法按路径取值", html.Msg)
	}
}

// TestDecodeBody_裸标量响应体仍可解析：剥封帧不能把"体本来就是数字"搞坏。
func TestDecodeBody_裸标量响应体仍可解析(t *testing.T) {
	got := runOneAssert(t, "123", model.AssertItem{
		Check: "body", Assert: "eq", Expect: float64(123),
	})
	if got.CheckValue != "123" || !got.Passed {
		t.Errorf("裸标量响应体解析结果 = %q / passed=%v, want 123 / true", got.CheckValue, got.Passed)
	}
}

// TestExtractJSONValue_抠出最外层JSON：这是剥封帧的核心工具函数。
func TestExtractJSONValue_抠出最外层JSON(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"chunked 封帧", "4904\n{\"a\":1}\n0", `{"a":1}`, true},
		{"前后都有噪声", "noise before {\"a\":1} noise after", `{"a":1}`, true},
		{"数组在最前面", "[1,2]\n{\"a\":1}", "[1,2]", true},
		{"纯文本", "502 Bad Gateway", "", false},
		{"只有起始括号", "{\"a\":1", "", false},
		{"只有结束括号", `"a":1}`, "", false},
		{"空串", "", "", false},
		{"嵌套时取最外层", `{"a":{"b":1}}`, `{"a":{"b":1}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := extractJSONValue(c.in)
			if ok != c.wantOK || got != c.want {
				t.Errorf("extractJSONValue(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BUG-001 × BUG-002 的真实夹具端到端回归
//
// 夹具 assert_fail_https 来自验收现场的一次真实执行（run 61）：
// 用例 `GET https://api.alternative.me/v2/listings/`，断言 `body.data[0].name`
// 故意写成 BitcoinXXX ⇒ 引擎 panic、退出码 2、无 summary.json，
// 且响应是 chunked 传输（体首带 `4904`）—— 两个 bug 的成因在这一个夹具里齐全。
// ---------------------------------------------------------------------------

func TestParse_真实HTTPS失败夹具_同时回归两个bug(t *testing.T) {
	in := loadFixture(t, "assert_fail_https")
	if in.ExitCode != 2 {
		t.Fatalf("夹具前提变了：退出码 = %d, want 2（断言失败是 Go panic）", in.ExitCode)
	}
	in.EnabledSteps = []StepDecl{{
		Seq: 1, Name: "listing", StepType: model.StepRequest,
		Method: "GET", URL: "https://api.alternative.me/v2/listings/",
		Assertions: []model.AssertItem{
			{Check: "body.data[0].name", Assert: "eq", Expect: "BitcoinXXX"},
		},
	}}

	res := Parse(in)

	if !res.Panic || res.HasSummary {
		t.Fatalf("夹具前提变了：断言失败应 panic 且无 summary（panic=%v, hasSummary=%v）",
			res.Panic, res.HasSummary)
	}
	if res.Status != model.StatusFail {
		t.Fatalf("状态 = %s, want fail", res.Status)
	}

	st := res.Steps[0]

	// —— BUG-002：协议不能被篡改成 http ——
	if st.FinalURL != "https://api.alternative.me/v2/listings/" {
		t.Errorf("最终生效 URL = %q, want https://api.alternative.me/v2/listings/"+
			"（BUG-002：旧实现会因请求行是 HTTP/1.1 而错报成 http://）", st.FinalURL)
	}
	if st.FinalURLSource != "reconstructed" {
		t.Errorf("URL 来源 = %q, want reconstructed（无 summary 时只能重建）", st.FinalURLSource)
	}

	// —— BUG-001：数组下标断言必须能报出真实值 ——
	if len(st.Assertions) != 1 {
		t.Fatalf("断言明细数 = %d, want 1", len(st.Assertions))
	}
	a := st.Assertions[0]
	if !a.Rebuilt {
		t.Error("重建的断言必须标 Rebuilt=true")
	}
	if a.CheckValue != "Bitcoin" {
		t.Errorf("实际值 = %q, want Bitcoin（BUG-001：旧实现恒为空串）", a.CheckValue)
	}
	if a.CheckValueType != "string" {
		t.Errorf("实际值类型 = %q, want string", a.CheckValueType)
	}
	if a.Passed {
		t.Error("期望 BitcoinXXX 而实际 Bitcoin，不应通过")
	}
}
