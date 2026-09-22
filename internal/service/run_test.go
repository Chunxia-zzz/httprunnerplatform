package service

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/parser"
)

// TestDecideRun_逐场景裁决 覆盖 M1 最核心的一条正确性规则。
//
// 这条规则决定"一次执行"最终显示成绿色还是红色。它出错的代价极不对称：
// 把失败判成成功会让用户带着"全绿"的假象上线，而把成功判成失败
// 最多是多看一眼日志。因此每一个分支都单独锁一遍。
func TestDecideRun_逐场景裁决(t *testing.T) {
	cases := []struct {
		name      string
		res       *parser.Result
		wantRun   string
		wantAct   int
		wantMism  bool
		wantInMsg string
	}{
		{
			name: "正常通过",
			res: &parser.Result{
				Status: model.StatusPass, Attribution: model.AttrPass,
				ExitCode: 0, CaseStarted: true, CaseEnded: true,
			},
			wantRun:  model.RunSuccess,
			wantAct:  1,
			wantMism: false,
		},
		{
			name: "断言失败（引擎 panic，退出码 2）",
			res: &parser.Result{
				Status: model.StatusFail, Attribution: model.AttrSystemUnderTest,
				ExitCode: 2, Panic: true, CaseStarted: true,
			},
			wantRun:  model.RunFailed,
			wantAct:  1,
			wantMism: false,
		},
		{
			name: "连接被拒（退出码 1）",
			res: &parser.Result{
				Status: model.StatusError, Attribution: model.AttrEnvironment,
				ExitCode: 1, CaseStarted: true,
			},
			wantRun:  model.RunError,
			wantAct:  1,
			wantMism: false,
		},
		{
			name: "请求超时（与连接失败同码，靠错误文本分流）",
			res: &parser.Result{
				Status: model.StatusError, Attribution: model.AttrTimeout,
				ExitCode: 1, CaseStarted: true,
			},
			wantRun:  model.RunError,
			wantAct:  1,
			wantMism: false,
		},
		{
			name: "用户取消",
			res: &parser.Result{
				Status: model.StatusError, Attribution: model.AttrCanceled,
				CaseStarted: true,
			},
			wantRun:  model.RunCanceled,
			wantAct:  1,
			wantMism: false,
		},
		{
			// ⭐ 假绿（实测 F5）：退出码 0、summary.success=true，但一条用例都没跑。
			// 只信退出码的实现会把它记成 success —— 这是全平台最危险的一种错。
			name: "假绿：退出码 0 但用例从未启动",
			res: &parser.Result{
				Status:      model.StatusError,
				Attribution: model.AttrCaseIssue,
				ExitCode:    0,
				CaseStarted: false,
				ErrorMsg:    "引擎以成功退出，但没有任何用例被执行 —— 用例文件很可能被引擎静默丢弃了",
			},
			wantRun:   model.RunError,
			wantAct:   0,
			wantMism:  true,
			wantInMsg: "静默丢弃",
		},
		{
			name: "排队期间被取消（连进程都没起）",
			res: &parser.Result{
				Status: model.StatusError, Attribution: model.AttrCanceled,
				CaseStarted: false,
			},
			// 取消优先于对账：用户主动终止不该被记成"对账不一致"
			wantRun:  model.RunCanceled,
			wantAct:  0,
			wantMism: true,
		},
		{
			name: "执行机 Python 环境异常（退出码 9）",
			res: &parser.Result{
				Status: model.StatusError, Attribution: model.AttrOps,
				ExitCode: 9, CaseStarted: false,
			},
			wantRun:  model.RunError,
			wantAct:  0,
			wantMism: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideRun(tc.res)

			if got.Status != tc.wantRun {
				t.Errorf("执行终态 = %q, want %q", got.Status, tc.wantRun)
			}
			if got.Actual != tc.wantAct {
				t.Errorf("actual_case_count = %d, want %d", got.Actual, tc.wantAct)
			}
			if got.Expected != 1 {
				t.Errorf("expected_case_count = %d, want 1（单用例执行）", got.Expected)
			}
			if got.Mismatch != tc.wantMism {
				t.Errorf("count_mismatch = %v, want %v", got.Mismatch, tc.wantMism)
			}
			if tc.wantMism && !strings.Contains(got.ErrMsg, "对账不一致") {
				t.Errorf("对账不一致时必须写清原因，实际: %q", got.ErrMsg)
			}
			if tc.wantInMsg != "" && !strings.Contains(got.ErrMsg, tc.wantInMsg) {
				t.Errorf("错误摘要应保留引擎/解析器的原始判断，实际: %q", got.ErrMsg)
			}
			if !tc.wantMism && strings.Contains(got.ErrMsg, "对账不一致") {
				t.Errorf("对账一致时不该出现对账文案: %q", got.ErrMsg)
			}
		})
	}
}

// TestDecideRun_假绿优先于退出码 单独锁一条不变式。
//
// 顺序一旦被写成"先看退出码"，假绿就会退化成"通过"，
// 而这个退化在正常用例上完全看不出来 —— 必须由这里挡住。
func TestDecideRun_假绿优先于退出码(t *testing.T) {
	green := &parser.Result{
		Status:      model.StatusError,
		Attribution: model.AttrCaseIssue,
		ExitCode:    0,
		CaseStarted: false,
	}
	if got := decideRun(green); got.Status != model.RunError {
		t.Fatalf("退出码 0 + 未启动 的执行终态 = %q，绝不能是 success", got.Status)
	}

	// 对照组：同样退出码 0，但用例确实跑了 ⇒ 必须判成功。
	// 没有这条对照，"一律判 error" 这种偷懒实现也能让上面那条测试变绿。
	ok := &parser.Result{
		Status:      model.StatusPass,
		Attribution: model.AttrPass,
		ExitCode:    0,
		CaseStarted: true,
	}
	if got := decideRun(ok); got.Status != model.RunSuccess || got.Mismatch {
		t.Fatalf("用例确实跑通时终态 = %q, mismatch = %v", got.Status, got.Mismatch)
	}
}

// TestAnyOf_类型化nil指针不落成null 锁住一个 Go 的经典陷阱。
//
// 把 `(*parser.RequestSnapshot)(nil)` 直接塞进 interface 后它**不等于 nil**，
// json.Marshal 会写出字符串 "null"，落库后前端拿到一个假对象
// —— 用户会看到"有一条请求快照"但点开是空的。
func TestAnyOf_类型化nil指针不落成null(t *testing.T) {
	var nilReq *parser.RequestSnapshot
	if v := anyOf(nilReq); v.Val != nil {
		t.Errorf("类型化 nil 指针应被归一化为空值，实际 %#v", v.Val)
	}

	var nilResp *parser.ResponseSnapshot
	if v := anyOf(nilResp); v.Val != nil {
		t.Errorf("类型化 nil 指针应被归一化为空值，实际 %#v", v.Val)
	}

	live := &parser.RequestSnapshot{Method: "GET", Path: "/get"}
	if v := anyOf(live); v.Val == nil {
		t.Error("非空快照不应被丢掉")
	}
}

// TestClip_按字符截断 保证不超出列宽，也不劈开汉字。
func TestClip_按字符截断(t *testing.T) {
	long := strings.Repeat("中", 50)
	got := clip(long, 10)
	if len([]rune(got)) != 10 {
		t.Errorf("截断后字符数 = %d, want 10", len([]rune(got)))
	}
	if !strings.HasPrefix(long, got) {
		t.Error("截断必须保留前缀，不能出现半个汉字")
	}

	short := "短"
	if clip(short, 10) != short {
		t.Error("未超长时不应改动原值")
	}
}

// TestCaseResultViews_失败与错误分开统计 锁住 step_failed / step_error 的语义。
//
// 这两个数是列表页上的"红点"，混在一起会掩盖真实原因：
//
//	step_failed = 被测行为不符预期（断言没过）
//	step_error  = 步骤根本没跑完（判据见实测 A16）
//
// 把 error 也算进 failed，用户在"环境挂了"和"接口错了"之间就分不出来。
//
// 顺带锁两件事：一次 GROUP BY 必须按 run_id 过滤（别的执行的步骤不能混进来），
// 以及没有步骤的用例结果必须是 0/0 而不是被漏掉。
func TestCaseResultViews_失败与错误分开统计(t *testing.T) {
	d := testDeps(t)
	svc := New(d)
	p := seedProject(t, d)

	// 两条执行，用来验证统计不会跨执行串台
	runA := mustRun(t, d, p.ID)
	runB := mustRun(t, d, p.ID)

	crA := mustCaseResult(t, d, runA.ID, 1)
	// 第二条用例结果刻意不挂任何步骤：它必须算出 0/0 而不是从统计里消失
	mustCaseResult(t, d, runA.ID, 2)

	// runB 下也塞一个 fail 步骤：若 GROUP BY 忘了 run_id 过滤，crA 会多算一步
	crB := mustCaseResult(t, d, runB.ID, 1)

	mustStep(t, d, runA.ID, crA.ID, 1, model.StatusPass)
	mustStep(t, d, runA.ID, crA.ID, 2, model.StatusFail)
	mustStep(t, d, runA.ID, crA.ID, 3, model.StatusError)
	mustStep(t, d, runA.ID, crA.ID, 4, model.StatusError)
	mustStep(t, d, runB.ID, crB.ID, 1, model.StatusFail)

	views, err := svc.Run.CaseResultViews(runA.ID)
	if err != nil {
		t.Fatalf("查询用例结果视图失败: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("用例结果数 = %d, want 2", len(views))
	}

	got := views[0]
	if got.StepFailed != 1 || got.StepError != 2 {
		t.Errorf("step_failed=%d step_error=%d, want 1 / 2（跨执行串台或状态归类错误）",
			got.StepFailed, got.StepError)
	}
	if got.AttributionLabel == "" {
		t.Error("归因标签必须由服务端生成，不能为空")
	}

	// 没有步骤的用例结果：必须是 0/0，而不是因为 map 里没这个 key 就崩掉
	if views[1].StepFailed != 0 || views[1].StepError != 0 {
		t.Errorf("无步骤的用例结果应为 0/0，实际 %d/%d",
			views[1].StepFailed, views[1].StepError)
	}
}

func mustRun(t *testing.T, d Deps, projectID uint64) *model.RunRecord {
	t.Helper()
	r := &model.RunRecord{
		ProjectID: projectID, TargetType: model.TargetCase, TargetID: 1,
		TargetName: "演示用例", TriggerType: model.TriggerManual,
		Status: model.RunFailed, Total: 1, Failed: 1,
		ExpectedCaseCount: 1, ActualCaseCount: 1,
	}
	if err := d.DB.Create(r).Error; err != nil {
		t.Fatalf("创建执行记录失败: %v", err)
	}
	return r
}

func mustCaseResult(t *testing.T, d Deps, runID uint64, seq int) *model.CaseResult {
	t.Helper()
	cr := &model.CaseResult{
		RunID: runID, CaseID: 1, CaseCode: "tc_1", ConfigName: "演示用例",
		Seq: seq, Status: model.StatusFail, ExitCode: 2, Panic: true,
		Attribution: model.AttrSystemUnderTest,
	}
	if err := d.DB.Create(cr).Error; err != nil {
		t.Fatalf("创建用例结果失败: %v", err)
	}
	return cr
}

func mustStep(t *testing.T, d Deps, runID, caseResultID uint64, seq int, status string) *model.StepResult {
	t.Helper()
	s := &model.StepResult{
		RunID: runID, CaseResultID: caseResultID, CaseCode: "tc_1",
		Seq: seq, StepName: "步骤", StepType: model.StepRequest, Status: status,
	}
	if err := d.DB.Create(s).Error; err != nil {
		t.Fatalf("创建步骤结果失败: %v", err)
	}
	return s
}
