package parser

import (
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// TestClassify_判别顺序不可调换 是归因模块最核心的测试。
//
// 实测 F8：断言失败会触发 Go panic，退出码固定为 2；
// 而 2 又落在引擎自己的「环境错误」区间里。
// 只看退出码必然把"被测系统的锅"算成"环境的锅"，
// 所以 panic 必须排在 exit_code 之前。
func TestClassify_判别顺序不可调换(t *testing.T) {
	cases := []struct {
		name string
		in   ClassifyInput
		want string
	}{
		{
			name: "通过（用例确实跑过）",
			in:   ClassifyInput{ExitCode: 0, CaseStarted: true},
			want: model.AttrPass,
		},
		{
			name: "假绿：退出码 0 但用例根本没启动",
			in:   ClassifyInput{ExitCode: 0, CaseStarted: false},
			want: model.AttrCaseIssue,
		},
		{
			name: "断言失败 panic，退出码 2 —— 必须归被测系统而不是执行机",
			in:   ClassifyInput{ExitCode: 2, Panic: true, CaseStarted: true, ErrorText: "panic: runtime error: invalid memory address"},
			want: model.AttrSystemUnderTest,
		},
		{
			name: "退出码 2 且无 panic —— 不猜测，交回原始日志",
			in:   ClassifyInput{ExitCode: 2, ErrorText: "something unexplained"},
			want: model.AttrUnknown,
		},
		{
			name: "被终止优先于一切",
			in:   ClassifyInput{ExitCode: 2, Panic: true, Canceled: true},
			want: model.AttrCanceled,
		},
		{
			name: "平台超时优先于 panic",
			in:   ClassifyInput{ExitCode: 2, Panic: true, TimedOut: true},
			want: model.AttrTimeout,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.in); got != c.want {
				t.Errorf("Classify(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestClassify_退出码1的二级分流 覆盖本轮实测新增的判别分支。
//
// ⭐ 核心事实：**请求级超时与连接失败同为退出码 1**（实测 F13），
// 只看退出码无法区分，必须下沉到错误文本。
// 下面两条错误文本是从真实引擎输出里逐字摘录的。
func TestClassify_退出码1的二级分流(t *testing.T) {
	cases := []struct {
		name    string
		errText string
		want    string
	}{
		{
			name: "请求超时",
			errText: `abort running due to failfast setting: do request failed: Get "http://127.0.0.1:8899/delay/6?x=1": ` +
				`context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			want: model.AttrTimeout,
		},
		{
			name: "连接被拒",
			errText: `abort running due to failfast setting: do request failed: Get "http://127.0.0.1:9999/get/": ` +
				`dial tcp 127.0.0.1:9999: connectex: No connection could be made because the target machine actively refused it.`,
			want: model.AttrEnvironment,
		},
		{
			name:    "DNS 解析失败",
			errText: `do request failed: Get "http://no-such-host.invalid/get": dial tcp: lookup no-such-host.invalid: no such host`,
			want:    model.AttrEnvironment,
		},
		{
			name:    "TLS 握手超时（同时含 timeout 与环境特征，必须判成超时）",
			errText: `do request failed: Get "https://slow.example.com/": net/http: TLS handshake timeout`,
			want:    model.AttrTimeout,
		},
		{
			name:    "错误文本缺失时保守归环境",
			errText: "",
			want:    model.AttrEnvironment,
		},
		{
			name:    "无法识别的文本也归环境（比归未知更有指导性）",
			errText: "do request failed: something entirely unexpected",
			want:    model.AttrEnvironment,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(ClassifyInput{ExitCode: 1, ErrorText: c.errText})
			if got != c.want {
				t.Errorf("Classify(exit=1, %q) = %q, want %q", c.errText, got, c.want)
			}
		})
	}
}

func TestClassify_退出码逐码映射(t *testing.T) {
	cases := []struct {
		exit int
		err  string
		want string
	}{
		// 实测确认：hook 调用的函数不存在
		{23, "run setup hooks failed: function no_such_hook is not found: call function failed", model.AttrCaseIssue},
		// 实测确认：引用了未定义的变量
		{21, "parse request params failed: variable this_variable_does_not_exist not found: variable not found", model.AttrCaseIssue},
		{22, "parse function failed", model.AttrCaseIssue},
		{24, "parse variables failed", model.AttrCaseIssue},
		{20, "parse error", model.AttrCaseIssue},
		// loader 区间：用例文件或其引用材料有问题
		{10, "load file error", model.AttrCaseIssue},
		{12, "load yaml error", model.AttrCaseIssue},
		{15, "invalid case format", model.AttrCaseIssue},
		{17, "referenced file not found", model.AttrCaseIssue},
		{18, "invalid plugin file", model.AttrCaseIssue},
		// 执行机环境
		{9, "prepare python3 venv failed", model.AttrOps},
		{31, "init plugin failed", model.AttrOps},
		{32, "build go plugin failed", model.AttrOps},
		{33, "build py plugin failed", model.AttrOps},
		// ⭐ 这两个是旧实现判错的地方：它们住在 runner 区间，
		// 但语义分别是"被中断"与"超时"，绝不能按区间一刀切成 ops。
		{38, "interrupt error", model.AttrCanceled},
		{39, "timeout error", model.AttrTimeout},
		// 移动端 / CV：平台不加载这类用例
		{50, "ios device connection error", model.AttrCaseIssue},
		{70, "mobile UI driver error", model.AttrCaseIssue},
		{85, "loop action not found error", model.AttrCaseIssue},
		// 区间空洞与越界。
		// 注意 2：官方 code.go 的 environment 区间 [2,10) 里只分配了 9，
		// 2-8 是预留未分配；而实测中出现的 2 全部来自 Go panic（已在上一组测试覆盖）。
		// 因此"exit=2 且无 panic"属于无法解释的情况，平台选择不猜。
		{2, "", model.AttrUnknown},
		{5, "", model.AttrUnknown},
		{25, "", model.AttrUnknown},
		{29, "", model.AttrUnknown},
		{34, "", model.AttrUnknown},
		{45, "", model.AttrUnknown},
		{99, "", model.AttrUnknown},
		{-1, "", model.AttrUnknown},
	}
	for _, c := range cases {
		if got := Classify(ClassifyInput{ExitCode: c.exit, ErrorText: c.err}); got != c.want {
			t.Errorf("exit=%d → %q, want %q", c.exit, got, c.want)
		}
	}
}

// TestStepStatus 锁定步骤级状态判据。
//
// 依据是 F8 的一个推论：断言失败会 panic，因此**带 `run step end` 且 success=false
// 的步骤，其失败原因一定不是断言不通过**，而是"步骤没能正常跑完"。
func TestStepStatus(t *testing.T) {
	cases := []struct {
		ended   bool
		success bool
		want    string
	}{
		{true, true, model.StatusPass},
		// 请求失败 / 变量未定义 / hook 抛错：步骤没跑完 → error
		{true, false, model.StatusError},
		// 没有 end ⇒ 走到了断言阶段才 panic → fail（被测行为不符预期）
		{false, false, model.StatusFail},
	}
	for _, c := range cases {
		if got := StepStatus(c.ended, c.success); got != c.want {
			t.Errorf("StepStatus(ended=%v, success=%v) = %q, want %q", c.ended, c.success, got, c.want)
		}
	}
}

// TestParse_被强杀的步骤归error 验证"未结束的步骤"会参考整例归因。
func TestParse_被强杀的步骤归error(t *testing.T) {
	// 构造：引擎停在 run step start，随后进程被平台杀掉
	stderr := `{"level":"info","testcase":"T","message":"run testcase start"}
{"level":"info","step":"s1","type":"request-GET","message":"run step start"}
`
	in := Input{
		ExitCode:     1,
		Stderr:       stderr,
		TimedOut:     true,
		EnabledSteps: []StepDecl{{Seq: 1, Name: "s1", StepType: model.StepRequest, URL: "$base_url/get"}},
	}

	res := Parse(in)
	if res.Attribution != model.AttrTimeout {
		t.Fatalf("归因 = %q, want timeout", res.Attribution)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("步骤数 = %d", len(res.Steps))
	}
	// 步骤虽然也没结束，但根因在平台侧，不能报成"断言不通过"
	if res.Steps[0].Status != model.StatusError {
		t.Errorf("被强杀的步骤状态 = %q, want error", res.Steps[0].Status)
	}
	if res.CleanExit {
		t.Error("被强杀时不应认为进程是主动退出的")
	}
}

// TestStatusFromAttribution 锁定"失败"与"错误"的语义边界。
//
// 这个区分是平台对用户的核心价值：用例跑完了但行为不符预期 = 失败（被测系统的锅）；
// 用例根本没跑完 = 错误（平台侧的锅）。两者混在一起，用户就不知道该找谁。
func TestStatusFromAttribution(t *testing.T) {
	cases := []struct {
		attr string
		want string
	}{
		{model.AttrPass, model.StatusPass},
		{model.AttrSystemUnderTest, model.StatusFail},
		{model.AttrEnvironment, model.StatusError},
		{model.AttrCaseIssue, model.StatusError},
		{model.AttrOps, model.StatusError},
		{model.AttrTimeout, model.StatusError},
		{model.AttrCanceled, model.StatusError},
		{model.AttrUnknown, model.StatusError},
	}
	for _, c := range cases {
		if got := StatusFromAttribution(c.attr); got != c.want {
			t.Errorf("StatusFromAttribution(%q) = %q, want %q", c.attr, got, c.want)
		}
	}
}

// TestAttributionLabel_全覆盖 保证每个归因都有中文标签与判断依据。
func TestAttributionLabel_全覆盖(t *testing.T) {
	all := []string{
		model.AttrPass, model.AttrSystemUnderTest, model.AttrEnvironment,
		model.AttrCaseIssue, model.AttrOps, model.AttrTimeout,
		model.AttrCanceled, model.AttrUnknown,
	}
	for _, a := range all {
		if AttributionLabelIs(a) == "" {
			t.Errorf("归因 %q 缺少中文标签", a)
		}
		if model.AttributionReason(a) == "" {
			t.Errorf("归因 %q 缺少判断依据说明", a)
		}
	}
}

// AttributionLabelIs 只是让上面那个测试读起来顺一点。
func AttributionLabelIs(a string) string { return model.AttributionLabel(a) }
