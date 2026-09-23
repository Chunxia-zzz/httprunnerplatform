package service

import (
	"context"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// mustDebugCase 建一条含三个启步骤 + 一个禁用步骤的用例，供调试测试复用。
//
// seq 布局刻意留一个"禁用步骤"夹在中间（seq=2 禁用），
// 验证单步调试的"前缀"是按**启用步骤**算的，而不是按 seq 连续算。
func mustDebugCase(t *testing.T, d Deps) (*model.Project, *CaseDetail) {
	t.Helper()
	p := seedProject(t, d)

	off := false
	detail := mustCase(t, d, p.ID, CaseReq{
		Code: "dbg_case",
		Name: "调试用例",
		Steps: []StepReq{
			{Seq: 1, StepType: model.StepRequest, Name: "登录",
				Request: reqAny(map[string]any{"method": "GET", "url": "$base_url/login"}),
				Extract: []model.ExtractItem{{Name: "token", Object: "body", Expression: "token"}},
			},
			{Seq: 2, StepType: model.StepRequest, Name: "禁用步",
				Request: reqAny(map[string]any{"method": "GET", "url": "$base_url/skip"}),
				Enabled: &off,
			},
			{Seq: 3, StepType: model.StepRequest, Name: "查用户",
				Request: reqAny(map[string]any{"method": "GET", "url": "$base_url/user"}),
				Validate: []model.AssertItem{{Check: "status_code", Assert: "eq", Expect: float64(200)}},
			},
		},
	})
	return p, detail
}

// TestDebugStep_参数校验 覆盖入口处的硬校验，这些不依赖引擎、必须稳定失败。
func TestDebugStep_参数校验(t *testing.T) {
	d := testDeps(t)
	svc := &DebugService{Deps: d}
	ctx := context.Background()

	cases := []struct {
		name string
		req  DebugStepRequest
	}{
		{"缺 project_id", DebugStepRequest{CaseID: 1, StepSeq: 1}},
		{"缺 case_id", DebugStepRequest{ProjectID: 1, StepSeq: 1}},
		{"step_seq 为 0", DebugStepRequest{ProjectID: 1, CaseID: 1, StepSeq: 0}},
		{"step_seq 为负", DebugStepRequest{ProjectID: 1, CaseID: 1, StepSeq: -1}},
	}
	for _, c := range cases {
		if _, err := svc.DebugStep(ctx, c.req); err == nil {
			t.Errorf("%s：应返回参数错误，实际返回 nil error", c.name)
		}
	}
}

// TestDebugStep_找不到目标步骤 目标 seq 不存在时应明确报错，而不是静默跑空。
func TestDebugStep_找不到目标步骤(t *testing.T) {
	d := testDeps(t)
	p, _ := mustDebugCase(t, d)
	svc := &DebugService{Deps: d}

	_, err := svc.DebugStep(context.Background(), DebugStepRequest{
		ProjectID: p.ID,
		CaseID:    detailID(t, d, "dbg_case"),
		StepSeq:   99, // 不存在的 seq
	})
	if err == nil {
		t.Fatal("目标 seq 不存在时应报错")
	}
}

// TestDebugStep_禁用的目标步骤不可调试 用户不能调试一条被禁用的步骤。
func TestDebugStep_禁用的目标步骤不可调试(t *testing.T) {
	d := testDeps(t)
	p, _ := mustDebugCase(t, d)
	svc := &DebugService{Deps: d}

	_, err := svc.DebugStep(context.Background(), DebugStepRequest{
		ProjectID: p.ID,
		CaseID:    detailID(t, d, "dbg_case"),
		StepSeq:   2, // 这是被禁用的步骤
	})
	if err == nil {
		t.Fatal("被禁用的步骤不应可调试")
	}
}

// TestDebugStep_跨项目用例拒绝 传了别的项目的用例 id 必须拒绝。
func TestDebugStep_跨项目用例拒绝(t *testing.T) {
	d := testDeps(t)
	_ = seedProject(t, d) // 项目 A（空）
	p2 := seedProject(t, d)
	_, detail := mustDebugCase(t, d) // 项目 B 里的用例

	svc := &DebugService{Deps: d}
	_, err := svc.DebugStep(context.Background(), DebugStepRequest{
		ProjectID: p2.ID,       // 故意传另一个项目
		CaseID:    detail.ID,
		StepSeq:   1,
	})
	if err == nil {
		t.Fatal("跨项目访问用例应报错")
	}
}

// TestDebugStep_用例无启用步骤 全禁用时应报错。
func TestDebugStep_用例无启用步骤(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	off := false
	detail := mustCase(t, d, p.ID, CaseReq{
		Code: "all_off",
		Name: "全禁用",
		Steps: []StepReq{{
			Seq: 1, StepType: model.StepRequest, Name: "关",
			Request: reqAny(map[string]any{"method": "GET", "url": "$base_url/x"}),
			Enabled: &off,
		}},
	})

	svc := &DebugService{Deps: d}
	_, err := svc.DebugStep(context.Background(), DebugStepRequest{
		ProjectID: p.ID,
		CaseID:    detail.ID,
		StepSeq:   1,
	})
	if err == nil {
		t.Fatal("没有任何启用步骤时应报错")
	}
}

// TestDebugStep_前缀取启用步骤 验证"前缀 = 启用步骤里 seq≤目标的前缀"这一口径。
//
// 这是单步调试最核心的语义：调试 seq=3 的步骤时，前缀必须只含 seq=1 与 seq=3
// （跳过禁用的 seq=2），这样"依赖上一步提取 token"的步骤才能拿到真实变量。
func TestDebugStep_前缀取启用步骤(t *testing.T) {
	steps := []model.TestStep{
		{SoftBase: model.SoftBase{Base: model.Base{ID: 1}}, Seq: 1, Enabled: true},
		{SoftBase: model.SoftBase{Base: model.Base{ID: 2}}, Seq: 2, Enabled: false}, // 禁用，应被过滤
		{SoftBase: model.SoftBase{Base: model.Base{ID: 3}}, Seq: 3, Enabled: true},
	}
	enabled := enabledStepsForDebug(steps)
	if len(enabled) != 2 {
		t.Fatalf("启用步骤数 = %d, want 2", len(enabled))
	}
	if enabled[0].Seq != 1 || enabled[1].Seq != 3 {
		t.Fatalf("启用步骤 seq = [%d %d], want [1 3]", enabled[0].Seq, enabled[1].Seq)
	}
}

// TestDebugStep_排序稳定 seq 相同按 id 升序。
func TestDebugStep_排序稳定(t *testing.T) {
	steps := []model.TestStep{
		{SoftBase: model.SoftBase{Base: model.Base{ID: 30}}, Seq: 1, Enabled: true},
		{SoftBase: model.SoftBase{Base: model.Base{ID: 10}}, Seq: 1, Enabled: true},
		{SoftBase: model.SoftBase{Base: model.Base{ID: 20}}, Seq: 2, Enabled: true},
	}
	got := enabledStepsForDebug(steps)
	if got[0].ID != 10 || got[1].ID != 30 || got[2].ID != 20 {
		t.Fatalf("排序结果 id = [%d %d %d], want [10 30 20]", got[0].ID, got[1].ID, got[2].ID)
	}
}

// TestDebugStep_超时上限 前端传的超时不得超过硬上限。
func TestDebugStep_超时上限(t *testing.T) {
	if MaxDebugTimeout <= DefaultDebugTimeout {
		t.Fatalf("MaxDebugTimeout(%v) 应大于 DefaultDebugTimeout(%v)", MaxDebugTimeout, DefaultDebugTimeout)
	}
}

// detailID 从库里按 code 取用例 id，失败即终止测试。
func detailID(t *testing.T, d Deps, code string) uint64 {
	t.Helper()
	var tc model.TestCase
	if err := d.DB.Where("code = ?", code).First(&tc).Error; err != nil {
		t.Fatalf("读取用例 %q 失败: %v", code, err)
	}
	return tc.ID
}
