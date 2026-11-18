package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func TestNormalizePerfPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:8899/users/123?x=1", "/users/:id"},
		{"https://api.example.com/v1/orders/9/items", "/v1/orders/:id/items"},
		{"http://h/users/550e8400-e29b-41d4-a716-446655440000", "/users/:id"},
		{"http://h/users/", "/users"},
		{"http://h/", "/"},
		{"/users/42", "/users/:id"},
		// 代价：年份/版本号这类纯数字段也会被归一（/v1/2024/report 与 /v1/2023/report 合并）。
		// 这是有意取舍：宁可少一个维度，也不能让主键把同一接口拆成无数条。
		{"http://h/v1/2024/report", "/v1/:id/report"},
	}
	for _, c := range cases {
		if got := normalizePerfPath(c.in); got != c.want {
			t.Errorf("normalizePerfPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMedian(t *testing.T) {
	if got := median(nil); got != 0 {
		t.Fatalf("空集合 = %d", got)
	}
	if got := median([]int64{5}); got != 5 {
		t.Fatalf("单个 = %d", got)
	}
	if got := median([]int64{300, 100, 200}); got != 200 {
		t.Fatalf("奇数 = %d", got)
	}
	if got := median([]int64{100, 200, 300, 400}); got != 250 {
		t.Fatalf("偶数应取中间均值 = %d", got)
	}
}

func TestJudgePerf(t *testing.T) {
	opt := PerfOptions{ThresholdPct: 30, MinDeltaMs: 50, MinSamples: 3}

	// 无基线
	if v, _, _ := judgePerf(100, 0, 0, opt); v != PerfNoBaseline {
		t.Fatalf("无基线 = %s", v)
	}
	// 样本不足
	if v, _, _ := judgePerf(300, 100, 2, opt); v != PerfInsufficient {
		t.Fatalf("样本不足 = %s", v)
	}
	// 回归：+200% 且绝对差达标
	if v, d, p := judgePerf(300, 100, 3, opt); v != PerfRegressed || d != 200 || p != 200 {
		t.Fatalf("回归 = %s %d %.1f", v, d, p)
	}
	// 绝对差不够：100 → 120（+20%）不报
	if v, _, _ := judgePerf(120, 100, 3, opt); v != PerfStable {
		t.Fatalf("+20%% 应为稳定 = %s", v)
	}
	// 百分比够但绝对差 3ms：应稳定（误报控制）
	tiny := PerfOptions{ThresholdPct: 30, MinDeltaMs: 50, MinSamples: 1}
	if v, _, _ := judgePerf(103, 100, 3, tiny); v != PerfStable {
		t.Fatalf("3ms 差异应稳定 = %s", v)
	}
	// 变快
	if v, _, _ := judgePerf(50, 200, 3, opt); v != PerfImproved {
		t.Fatalf("变快 = %s", v)
	}
}

// perfSeed 造「N 次执行 + 每接口一条步骤」的数据。
func perfSeed(t *testing.T, d Deps) (*model.Project, []*model.RunRecord) {
	t.Helper()
	proj := seedProject(t, d)
	now := time.Now()
	var runs []*model.RunRecord
	for i := 0; i < 4; i++ {
		r := &model.RunRecord{
			Base:      model.Base{CreatedAt: now.Add(-time.Duration(4-i) * time.Minute)},
			ProjectID: proj.ID,
			Status:    model.RunSuccess,
		}
		if err := d.DB.Create(r).Error; err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	return proj, runs
}

func addPerfStep(t *testing.T, d Deps, runID uint64, method, url string, ms int64) {
	t.Helper()
	st := &model.StepResult{
		RunID: runID, CaseResultID: 1, CaseCode: "c", Seq: 1,
		StepName: "s", StepType: model.StepRequest, Status: model.StatusPass,
		FinalURL:        url,
		RequestSnapshot: jsonx.Any{Val: map[string]any{"method": method, "url": url}},
		ElapsedMs:       ms,
	}
	if err := d.DB.Create(st).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPerfAnalyze_回归与过滤(t *testing.T) {
	d := testDeps(t)
	proj, runs := perfSeed(t, d)
	svc := New(d).Perf

	// 前三次 100ms（同一接口，带不同主键），第四次 300ms
	for i := 0; i < 3; i++ {
		addPerfStep(t, d, runs[i].ID, "GET", fmt.Sprintf("http://h/users/%d", 100+i), 100)
		addPerfStep(t, d, runs[i].ID, "POST", "http://h/orders", 200)
	}
	addPerfStep(t, d, runs[3].ID, "GET", "http://h/users/999", 300)
	addPerfStep(t, d, runs[3].ID, "POST", "http://h/orders", 210)

	res, err := svc.Analyze(proj.ID, PerfOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("接口数 = %d（不同主键应归一为同一接口）", len(res.Items))
	}
	byEP := map[string]PerfItem{}
	for _, it := range res.Items {
		byEP[it.Endpoint] = it
	}
	users, ok := byEP["GET /users/:id"]
	if !ok {
		t.Fatalf("缺少 /users/:id: %+v", res.Items)
	}
	if users.Verdict != PerfRegressed || users.BaselineMs != 100 || users.LatestMs != 300 ||
		users.DeltaPct != 200 || users.Samples != 3 {
		t.Fatalf("users 对比 = %+v", users)
	}
	orders := byEP["POST /orders"]
	if orders.Verdict != PerfStable {
		t.Fatalf("orders 应为稳定: %+v", orders)
	}
	if res.Summary.Regressed != 1 || res.Summary.Endpoints != 2 {
		t.Fatalf("汇总 = %+v", res.Summary)
	}
	// 回归项排在最前
	if res.Items[0].Verdict != PerfRegressed {
		t.Fatalf("排序错误: %+v", res.Items[0])
	}

	// 指定 run_id：只返回该次执行出现过的接口
	res2, err := svc.Analyze(proj.ID, PerfOptions{RunID: runs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res2.Items {
		if it.LatestRunID != runs[0].ID {
			t.Fatalf("run_id 过滤失效: %+v", it)
		}
	}
	// 第一次执行没有基线 → no_baseline
	if len(res2.Items) == 0 || res2.Items[0].Verdict != PerfNoBaseline {
		t.Fatalf("首个执行应无基线: %+v", res2.Items)
	}
}

func TestPerfAnalyze_样本不足与空项目(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Perf

	// 空项目
	res, err := svc.Analyze(proj.ID, PerfOptions{})
	if err != nil || len(res.Items) != 0 {
		t.Fatalf("空项目 = %+v, %v", res, err)
	}

	// 两次执行（少于默认 min_samples=3）→ insufficient
	now := time.Now()
	var ids []uint64
	for i := 0; i < 2; i++ {
		r := &model.RunRecord{Base: model.Base{CreatedAt: now.Add(-time.Duration(2-i) * time.Minute)},
			ProjectID: proj.ID, Status: model.RunSuccess}
		if err := d.DB.Create(r).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	addPerfStep(t, d, ids[0], "GET", "http://h/a", 100)
	addPerfStep(t, d, ids[1], "GET", "http://h/a", 500)

	res, err = svc.Analyze(proj.ID, PerfOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Verdict != PerfInsufficient {
		t.Fatalf("样本不足应明确标注: %+v", res.Items)
	}
}
