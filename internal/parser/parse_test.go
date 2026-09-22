package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// ---------------------------------------------------------------------------
// 夹具加载
//
// testdata/ 下的每个目录都是一次**真实引擎执行**的原始产物：
//
//	exit_code   用例子进程退出码（无换行）
//	stdout.txt  请求/响应明细
//	stderr.txt  引擎日志（panic 时含完整 Go 栈回溯）
//	summary.json 仅在引擎正常走到收尾阶段时存在
//
// 采集方式见 docs/引擎实测记录.md。这些夹具是 parser 包唯一可信的回归基准——
// 任何"引擎行为"的断言，如果不能用这里的原始输出复现，就不该写进代码。
// ---------------------------------------------------------------------------

func loadFixture(t *testing.T, name string) Input {
	t.Helper()
	dir := filepath.Join("testdata", name)

	codeRaw, err := os.ReadFile(filepath.Join(dir, "exit_code"))
	if err != nil {
		t.Fatalf("夹具 %s 不存在: %v", name, err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(codeRaw)))
	if err != nil {
		t.Fatalf("夹具 %s 的 exit_code 非法: %v", name, err)
	}

	in := Input{ExitCode: code}
	if b, err := os.ReadFile(filepath.Join(dir, "stdout.txt")); err == nil {
		in.Stdout = string(b)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "stderr.txt")); err == nil {
		in.Stderr = string(b)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "summary.json")); err == nil {
		in.SummaryJSON = b
	}
	return in
}

func assertStep(t *testing.T, res *Result, i int, wantName string) StepOutcome {
	t.Helper()
	if i >= len(res.Steps) {
		t.Fatalf("期望至少 %d 个步骤，实际 %d", i+1, len(res.Steps))
	}
	got := res.Steps[i]
	if wantName != "" && got.Name != wantName {
		t.Fatalf("步骤 %d 名称 = %q, want %q", i, got.Name, wantName)
	}
	return got
}

// ---------------------------------------------------------------------------
// 通过路径
// ---------------------------------------------------------------------------

func TestParse_全部通过(t *testing.T) {
	in := loadFixture(t, "pass_2steps")
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "步骤1 GET /get 带 query 参数", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get",
			Assertions: []model.AssertItem{
				{Check: "status_code", Assert: "eq", Expect: float64(200)},
				{Check: "body.args.foo1", Assert: "eq", Expect: "bar1"},
			}},
		{Seq: 2, Name: "步骤2 POST /post 带 JSON body", StepType: model.StepRequest, Method: "POST", URL: "$base_url/post",
			Assertions: []model.AssertItem{
				{Check: "status_code", Assert: "eq", Expect: float64(200)},
				{Check: "body.json.name", Assert: "eq", Expect: "tester"},
			}},
	}

	res := Parse(in)

	if res.Status != model.StatusPass || res.Attribution != model.AttrPass {
		t.Fatalf("状态/归因错误: %s / %s", res.Status, res.Attribution)
	}
	if !res.CleanExit {
		t.Error("stdout 里有 `hrp exit 0`，CleanExit 应为 true")
	}
	if res.Panic {
		t.Error("成功的用例不应标记 panic")
	}
	if !res.HasSummary {
		t.Fatal("成功用例应当解析到 summary.json")
	}
	if res.Platform.HttprunnerVersion != "v4.3.6" {
		t.Errorf("引擎版本解析错误: %q", res.Platform.HttprunnerVersion)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("步骤数 = %d, want 2", len(res.Steps))
	}

	for i := range res.Steps {
		st := res.Steps[i]
		if st.Status != model.StatusPass {
			t.Errorf("步骤 %d 状态 = %s", i, st.Status)
		}
		if st.InferredFailed {
			t.Errorf("步骤 %d 不应标记为推断失败", i)
		}
		if st.Response == nil || st.Response.StatusCode != 200 {
			t.Errorf("步骤 %d 未解析到响应快照", i)
		}
		// 断言明细应来自引擎（Rebuilt=false），而不是平台重建
		if len(st.Assertions) != 2 {
			t.Errorf("步骤 %d 断言数 = %d, want 2", i, len(st.Assertions))
		}
		for _, a := range st.Assertions {
			if a.Rebuilt {
				t.Errorf("步骤 %d 的断言被标为平台重建，但引擎已给出明细", i)
			}
			if !a.Passed {
				t.Errorf("步骤 %d 的断言未通过: %+v", i, a)
			}
			// 引擎日志里的类型名是 int64，平台不应报成 float64
			if a.CheckExpr == "status_code" && a.ExpectValueType != "int64" {
				t.Errorf("整数期望值的类型名 = %q, want int64", a.ExpectValueType)
			}
		}
	}

	// 最终 URL：请求行里的 path 是最权威的（F9 会补尾斜杠）
	if got := res.Steps[1].FinalURL; got != "http://127.0.0.1:8899/post/" {
		t.Errorf("步骤2 最终 URL = %q（应体现引擎补的尾斜杠）", got)
	}
	if res.Steps[0].FinalURLSource != "summary" && res.Steps[0].FinalURLSource != "reconstructed" {
		t.Errorf("FinalURLSource 未标注: %q", res.Steps[0].FinalURLSource)
	}
}

func TestParse_extract结果(t *testing.T) {
	in := loadFixture(t, "pass_extract")
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "步骤1 获取 token", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get"},
		{Seq: 2, Name: "步骤2 复用上一步提取的 token", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get"},
	}

	res := Parse(in)
	if res.Status != model.StatusPass {
		t.Fatalf("状态 = %s, err=%s", res.Status, res.ErrorMsg)
	}
	// exportVars 在引擎日志里，用来验证参数关联真的发生了
	if got := res.Steps[0].ExtractResult; got == nil {
		t.Error("步骤1 未解析到提取结果（引擎日志的 exportVars）")
	}
}

// ---------------------------------------------------------------------------
// 断言失败（F8：panic，无 summary，无断言明细）
// ---------------------------------------------------------------------------

func TestParse_断言失败会panic且没有summary(t *testing.T) {
	in := loadFixture(t, "assert_fail")
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "status_code expects 201 but server returns 200", StepType: model.StepRequest,
			Method: "GET", URL: "$base_url/get",
			Assertions: []model.AssertItem{
				{Check: "status_code", Assert: "eq", Expect: float64(201)},
			}},
	}

	res := Parse(in)

	if !res.Panic {
		t.Fatal("stderr 里有 panic 栈，必须识别出来")
	}
	if res.HasSummary {
		t.Fatal("断言失败时引擎不产出 summary.json（实测 F8），不应解析到")
	}
	if res.CleanExit {
		t.Error("panic 会跳过 `hrp exit N`，CleanExit 应为 false")
	}
	if res.Attribution != model.AttrSystemUnderTest || res.Status != model.StatusFail {
		t.Fatalf("归因 = %q, 状态 = %q（应为被测系统 / 失败）", res.Attribution, res.Status)
	}

	st := assertStep(t, res, 0, "")
	// 引擎停在 `run step start`，没有配对的 `run step end`
	if !st.InferredFailed {
		t.Error("步骤启动了但没结束，应标记 InferredFailed（F8 的唯一可靠识别方式）")
	}
	if st.Started != true {
		t.Error("步骤应标记为已启动")
	}

	// 关键补偿：响应快照还在，必须用它重建出"哪里不符预期"
	if st.Response == nil || st.Response.StatusCode != 200 {
		t.Fatalf("未从 stdout 解析到响应快照: %+v", st.Response)
	}
	if len(st.Assertions) != 1 {
		t.Fatalf("断言数 = %d, want 1", len(st.Assertions))
	}
	a := st.Assertions[0]
	if !a.Rebuilt {
		t.Error("平台重建的断言必须标 Rebuilt=true，否则用户会误以为是引擎给的判定")
	}
	if a.Passed {
		t.Error("断言是 201 vs 实际 200，重建结果应为不通过")
	}
	if a.CheckValue != "200" {
		t.Errorf("重建出的实际值 = %q, want 200", a.CheckValue)
	}
	if a.CheckValueType != "int64" {
		t.Errorf("重建出的实际值类型 = %q, want int64（与引擎的叫法保持一致）", a.CheckValueType)
	}
	if a.Msg == "" {
		t.Error("失败断言应给出可读原因")
	}
}

func TestParse_第二步断言失败时第一步仍然完整(t *testing.T) {
	in := loadFixture(t, "assert_fail_2nd")
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "step1 ok", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get",
			Assertions: []model.AssertItem{{Check: "status_code", Assert: "eq", Expect: float64(200)}}},
		{Seq: 2, Name: "step2 status mismatch", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get",
			Assertions: []model.AssertItem{{Check: "status_code", Assert: "eq", Expect: float64(201)}}},
	}

	res := Parse(in)

	if len(res.Steps) != 2 {
		t.Fatalf("步骤数 = %d, want 2（stdout 里确实有两次请求）", len(res.Steps))
	}
	// 第一步必须判为通过 —— 不能因为整例 panic 就把前面的步骤一并算失败
	if s1 := res.Steps[0]; s1.Status != model.StatusPass || s1.InferredFailed {
		t.Errorf("步骤1 状态 = %s, inferred=%v（应当是通过）", s1.Status, s1.InferredFailed)
	}
	// 第二步：请求发出去了、响应也回来了，只是断言不通过
	s2 := res.Steps[1]
	if !s2.InferredFailed {
		t.Error("步骤2 应标记 InferredFailed")
	}
	if s2.Response == nil {
		t.Error("步骤2 的响应快照丢了：两次请求必须分别对应两个报文块，不能错位")
	}
	if len(s2.Assertions) != 1 || s2.Assertions[0].Passed || !s2.Assertions[0].Rebuilt {
		t.Errorf("步骤2 的断言重建结果错误: %+v", s2.Assertions)
	}
	// 第一步的断言来自引擎，不该被覆盖成重建结果
	if len(res.Steps[0].Assertions) != 1 || res.Steps[0].Assertions[0].Rebuilt {
		t.Errorf("步骤1 的断言应由引擎给出: %+v", res.Steps[0].Assertions)
	}
}

// ---------------------------------------------------------------------------
// 请求失败：与超时同码，靠错误文本区分
// ---------------------------------------------------------------------------

func TestParse_连接被拒(t *testing.T) {
	in := loadFixture(t, "conn_refused")
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "connect to a port with nothing listening", StepType: model.StepRequest,
			Method: "GET", URL: "http://127.0.0.1:9999/get"},
	}

	res := Parse(in)

	if res.Attribution != model.AttrEnvironment || res.Status != model.StatusError {
		t.Fatalf("归因 = %q, 状态 = %q（应为环境 / 错误）", res.Attribution, res.Status)
	}
	if !res.CleanExit {
		t.Error("连接失败是正常退出，stdout 里有 `hrp exit 1`")
	}
	if res.Panic {
		t.Error("连接失败不应触发 panic")
	}
	if !strings.Contains(res.ErrorMsg, "actively refused") {
		t.Errorf("错误摘要应保留引擎原文: %q", res.ErrorMsg)
	}

	st := assertStep(t, res, 0, "")
	if st.Status != model.StatusError {
		t.Errorf("步骤状态 = %s, want error", st.Status)
	}
	// ⭐ 请求块必须被消费：用户最想知道"它请求了哪个地址"
	if st.Request == nil {
		t.Fatal("连接被拒的步骤仍会打印请求块，不应丢弃")
	}
	if st.Request.Path != "/get/" || st.Request.Host != "127.0.0.1:9999" {
		t.Errorf("请求快照错误: %+v", st.Request)
	}
	if st.FinalURL != "http://127.0.0.1:9999/get/" {
		t.Errorf("最终 URL 重建错误: %q", st.FinalURL)
	}
	// `hrp exit 1` 不能出现在请求体里
	if strings.Contains(st.Request.Body, "hrp exit") {
		t.Errorf("`hrp exit N` 被误当成请求体: %q", st.Request.Body)
	}
}

func TestParse_请求超时归为超时而不是环境(t *testing.T) {
	in := loadFixture(t, "req_timeout")
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "request timeout 1s against delay 6s", StepType: model.StepRequest,
			Method: "GET", URL: "$base_url/delay/6?x=1"},
	}

	res := Parse(in)

	// 退出码与"连接被拒"完全相同（都是 1），必须靠错误文本分流
	if in.ExitCode != 1 {
		t.Fatalf("夹具前提变了：退出码 = %d", in.ExitCode)
	}
	if res.Attribution != model.AttrTimeout {
		t.Fatalf("归因 = %q, want timeout（退出码同为 1，只能靠 `context deadline exceeded` 区分）",
			res.Attribution)
	}
	if res.Status != model.StatusError {
		t.Errorf("状态 = %q, want error", res.Status)
	}
	if st := res.Steps[0]; st.Request == nil || strings.Contains(st.Request.Body, "hrp exit") {
		t.Errorf("超时步骤的请求快照错误: %+v", st.Request)
	}
}

// ---------------------------------------------------------------------------
// 用例问题：连请求都没发出去
// ---------------------------------------------------------------------------

func TestParse_hook函数不存在(t *testing.T) {
	in := loadFixture(t, "hook_notfound")
	if in.ExitCode != 23 {
		t.Fatalf("夹具前提变了：hook 失败退出码 = %d（实测为 23）", in.ExitCode)
	}
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "step level hooks", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get"},
	}

	res := Parse(in)

	if res.Attribution != model.AttrCaseIssue {
		t.Fatalf("归因 = %q, want case_issue", res.Attribution)
	}
	if !strings.Contains(res.ErrorMsg, "no_such_hook") {
		t.Errorf("错误摘要应指出是哪个函数: %q", res.ErrorMsg)
	}
	if st := res.Steps[0]; st.Request != nil {
		t.Error("hook 在请求前就失败了，不应有请求快照")
	}
}

func TestParse_变量未定义(t *testing.T) {
	in := loadFixture(t, "undef_var")
	if in.ExitCode != 21 {
		t.Fatalf("夹具前提变了：变量未定义退出码 = %d（实测为 21）", in.ExitCode)
	}
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "步骤1 引用了不存在的变量", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get"},
	}

	res := Parse(in)

	if res.Attribution != model.AttrCaseIssue {
		t.Fatalf("归因 = %q, want case_issue", res.Attribution)
	}
	if !strings.Contains(res.ErrorMsg, "this_variable_does_not_exist") {
		t.Errorf("错误摘要应指出是哪个变量: %q", res.ErrorMsg)
	}
}

// ---------------------------------------------------------------------------
// 假绿（F5）：退出码 0、零告警、一条用例都没跑
// ---------------------------------------------------------------------------

// TestParse_假绿必须被识别 是 parser 包里最重要的一条防御性断言。
//
// 引擎对畸形用例文件的表现是"静默丢弃"：不报错、不告警、**退出码 0**，
// summary.json 里还会写 success=true。任何只看退出码或 success 字段的实现
// 都会把一个"一个请求都没发出去"的执行报成绿色 —— 这是全平台最危险的一种错。
func TestParse_假绿必须被识别(t *testing.T) {
	in := loadFixture(t, "false_green")
	if in.ExitCode != 0 {
		t.Fatalf("夹具前提变了：假绿退出码 = %d（须为 0，否则这条测试失去意义）", in.ExitCode)
	}
	// 平台一定会把声明步骤传进来（来源是自己的库），所以 Steps 不会是空切片
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "步骤1 GET /get", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get",
			Assertions: []model.AssertItem{
				{Check: "status_code", Assert: "eq", Expect: float64(200)},
			}},
	}

	res := Parse(in)

	if res.CaseStarted || res.CaseEnded {
		t.Fatalf("夹具前提变了：假绿场景不应出现 run testcase start/end（started=%v ended=%v）",
			res.CaseStarted, res.CaseEnded)
	}
	if res.Panic {
		t.Error("假绿是被静默丢弃，不是崩溃，不应标记 panic")
	}

	// ⭐ 核心断言：绝不能判成 pass
	if res.Status == model.StatusPass {
		t.Fatal("假绿被判成了通过 —— 这正是 F5 要防的那个 bug")
	}
	if res.Attribution != model.AttrCaseIssue || res.Status != model.StatusError {
		t.Fatalf("归因 = %q, 状态 = %q（应上报为用例问题 / 错误）", res.Attribution, res.Status)
	}
	if !res.FalseGreen() {
		t.Error("FalseGreen() 应为 true（判据是 exit==0 && !CaseStarted，与 Steps 数量无关）")
	}
	if !res.MissedRun() {
		t.Error("MissedRun() 应为 true")
	}
	if !strings.Contains(res.ErrorMsg, "静默丢弃") {
		t.Errorf("错误摘要应点明成因（stderr 里没有任何 error 行，不说清楚用户无从下手）: %q", res.ErrorMsg)
	}

	// 声明了 1 步却一步没跑，必须回填成 skipped，前端才有东西可画
	if len(res.Steps) != 1 {
		t.Fatalf("步骤数 = %d, want 1（声明了几步就要回填几步）", len(res.Steps))
	}
	if st := res.Steps[0]; st.Status != model.StatusSkipped || st.Started {
		t.Errorf("步骤状态 = %s, started=%v（应为 skipped / false）", st.Status, st.Started)
	}
	if res.Steps[0].Request != nil || res.Steps[0].Response != nil {
		t.Error("假绿场景不应有任何报文快照")
	}
}

// TestParse_假绿的summary是个骗人的绿色 锁定"为什么不能信 summary.success"。
//
// 引擎在这种情况下确实会产出 summary.json，而且顶层 success=true、
// stat.testcases.total=0。只看 success 字段必然误判，因此平台必须
// 以 stderr 的 `run testcase start` 为准（CaseStarted），而不是 summary。
func TestParse_假绿的summary是个骗人的绿色(t *testing.T) {
	in := loadFixture(t, "false_green")
	res := Parse(in)

	if !res.HasSummary {
		t.Fatal("假绿场景引擎仍然产出 summary.json，应当被解析到")
	}
	var raw struct {
		Success bool `json:"success"`
		Stat    struct {
			Testcases struct {
				Total int `json:"total"`
			} `json:"testcases"`
		} `json:"stat"`
		Details any `json:"details"`
	}
	if err := json.Unmarshal(in.SummaryJSON, &raw); err != nil {
		t.Fatalf("夹具 summary.json 解析失败: %v", err)
	}
	if !raw.Success {
		t.Error("夹具前提变了：假绿的 summary.success 应为 true（正因为它骗人，才要写这条测试）")
	}
	if raw.Stat.Testcases.Total != 0 {
		t.Errorf("夹具前提变了：假绿的 stat.testcases.total = %d, want 0", raw.Stat.Testcases.Total)
	}
	if raw.Details != nil {
		t.Errorf("夹具前提变了：假绿的 details 应为 null，实际 %v", raw.Details)
	}
	if res.Status == model.StatusPass {
		t.Error("summary.success=true 时平台仍必须判为错误 —— 不能以 summary 为准")
	}
}

// ---------------------------------------------------------------------------
// 用例数对账与防御性
// ---------------------------------------------------------------------------

func TestParse_未启动的步骤标为skipped(t *testing.T) {
	in := loadFixture(t, "assert_fail")
	// 声明了 3 步，但引擎在第一步就崩了
	in.EnabledSteps = []StepDecl{
		{Seq: 1, Name: "s1", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get",
			Assertions: []model.AssertItem{{Check: "status_code", Assert: "eq", Expect: float64(201)}}},
		{Seq: 2, Name: "s2", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get"},
		{Seq: 3, Name: "s3", StepType: model.StepRequest, Method: "GET", URL: "$base_url/get"},
	}

	res := Parse(in)
	if len(res.Steps) != 3 {
		t.Fatalf("步骤数 = %d, want 3（声明了几步就要回填几步）", len(res.Steps))
	}
	if res.Steps[0].Status == model.StatusSkipped {
		t.Error("步骤1 确实执行了，不应标 skipped")
	}
	for _, i := range []int{1, 2} {
		if res.Steps[i].Status != model.StatusSkipped {
			t.Errorf("步骤%d 未被执行，应标 skipped，实际 %s", i+1, res.Steps[i].Status)
		}
	}
	if res.Steps[1].Started {
		t.Error("未执行的步骤不应标记 Started")
	}
}

func TestParse_stderr为空时不崩溃(t *testing.T) {
	res := Parse(Input{ExitCode: 1})
	if res.Attribution != model.AttrEnvironment {
		t.Errorf("无任何输出时归因 = %q", res.Attribution)
	}
	if len(res.Steps) != 0 {
		t.Errorf("无输出时不应产出步骤: %+v", res.Steps)
	}
}

// TestParse_stdout里的hrpExit不进入报文块 用最小输入锁定该修复。
func TestParse_stdout里的hrpExit不进入报文块(t *testing.T) {
	stdout := strings.Join([]string{
		"-------------------- request --------------------",
		"GET /get/ HTTP/1.1",
		"Host: 127.0.0.1:9999",
		"",
		"",
		"hrp exit 1",
	}, "\n")

	pairs := parseStdout(stdout)
	if len(pairs) != 1 {
		t.Fatalf("报文块数 = %d, want 1", len(pairs))
	}
	if pairs[0].Request == nil {
		t.Fatal("请求块未解析出来")
	}
	if pairs[0].Request.Body != "" {
		t.Errorf("请求体应为空，实际 %q（`hrp exit N` 被误吞了）", pairs[0].Request.Body)
	}
	if pairs[0].Request.Host != "127.0.0.1:9999" {
		t.Errorf("Host 未解析: %+v", pairs[0].Request)
	}
	if pairs[0].Response != nil {
		t.Error("请求失败时不应有响应块")
	}
}

func TestParse_多报文块按顺序对齐(t *testing.T) {
	stdout := strings.Join([]string{
		"-------------------- request --------------------",
		"GET /a HTTP/1.1",
		"Host: h",
		"",
		"",
		"==================== response ====================",
		"Connected via plaintext",
		"HTTP/1.1 200 OK",
		"",
		"body-a",
		"--------------------------------------------------",
		"-------------------- request --------------------",
		"GET /b HTTP/1.1",
		"Host: h",
		"",
		"",
		"==================== response ====================",
		"Connected via plaintext",
		"HTTP/1.1 404 Not Found",
		"",
		"body-b",
		"--------------------------------------------------",
		"hrp exit 0",
	}, "\n")

	pairs := parseStdout(stdout)
	if len(pairs) != 2 {
		t.Fatalf("报文块数 = %d, want 2", len(pairs))
	}
	if pairs[0].Request.Path != "/a" || pairs[0].Response.StatusCode != 200 {
		t.Errorf("第一块解析错误: %+v / %+v", pairs[0].Request, pairs[0].Response)
	}
	if pairs[1].Request.Path != "/b" || pairs[1].Response.StatusCode != 404 {
		t.Errorf("第二块解析错误: %+v / %+v", pairs[1].Request, pairs[1].Response)
	}
	if pairs[0].Response.Body != "body-a" || pairs[1].Response.Body != "body-b" {
		t.Error("响应体与报文块错位")
	}
}

// TestParse_遥测失败不能冒充用例错误 锁住实测 A20 的第二道防线。
//
// 引擎的 GA4 上报失败与 Sentry 初始化失败都是用 log.Error() 打的，
// 在 stderr 里和"用例失败"完全同形。实测就是这样：
// 一条 status=pass 的用例，error_msg 却是
// `request GA4 failed: ... context deadline exceeded`。
//
// 第一道防线在 executor（默认注入 DISABLE_GA=true 从源头关掉），
// 但用户显式打开遥测时仍会走到这里，所以必须有第二道。
func TestParse_遥测失败不能冒充用例错误(t *testing.T) {
	stderr := strings.Join([]string{
		`{"level":"error","error":"request GA4 failed: Post \"https://www.google-analytics.com/mp/collect?api_secret=x\": context deadline exceeded","message":"send GA4 event failed"}`,
		`{"level":"info","message":"run testcase start","testcase":"演示"}`,
		`{"level":"info","message":"run step start","step":"s1","type":"request-GET"}`,
	}, "\n")
	stdout := "-------------------- request --------------------\nGET /get HTTP/1.1\nHost: 127.0.0.1:8899\n\n" +
		"==================== response ====================\nHTTP/1.1 200 OK\n\n"

	res := Parse(Input{
		ExitCode: 0, Stdout: stdout, Stderr: stderr,
		EnabledSteps: []StepDecl{{Seq: 1, Name: "s1", StepType: model.StepRequest}},
	})

	if res.Status != model.StatusPass {
		t.Fatalf("退出码 0 且用例已启动，应判 pass，实际 %s", res.Status)
	}
	if res.ErrorMsg != "" {
		t.Errorf("成功的执行不该带错误摘要（遥测噪音泄露），实际: %q", res.ErrorMsg)
	}

	// 对照组：真正的用例错误必须**照常**被提取出来。
	// 没有这条对照，"把所有 error 行都丢弃"这种偷懒实现也能让上面变绿。
	real := strings.Join([]string{
		`{"level":"error","error":"request GA4 failed: Post \"https://www.google-analytics.com/\": context deadline exceeded"}`,
		`{"level":"error","error":"do request failed: Get \"http://127.0.0.1:8899/get\": dial tcp 127.0.0.1:8899: connectex: No connection could be made","message":"run step end"}`,
	}, "\n")
	res2 := Parse(Input{
		ExitCode: 1, Stdout: "", Stderr: real,
		EnabledSteps: []StepDecl{{Seq: 1, Name: "s1", StepType: model.StepRequest}},
	})
	if !strings.Contains(res2.ErrorMsg, "dial tcp") {
		t.Errorf("真实错误必须保留，实际: %q", res2.ErrorMsg)
	}
	if strings.Contains(res2.ErrorMsg, "GA4") {
		t.Errorf("错误摘要里不该出现遥测噪音，实际: %q", res2.ErrorMsg)
	}
}
