package service

import (
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// 构造一个带执行历史的内存测试环境，验证统计聚合逻辑。
func statsSeed(t *testing.T, d Deps) uint64 {
	t.Helper()
	proj := seedProject(t, d)
	now := time.Now()

	mkRun := func(daysAgo int, status string, passed, failed, errc int, dur int64) *model.RunRecord {
		r := &model.RunRecord{
			Base:       model.Base{CreatedAt: now.AddDate(0, 0, -daysAgo)},
			ProjectID:  proj.ID,
			Status:     status,
			Passed:     passed,
			Failed:     failed,
			Error:      errc,
			DurationMs: dur,
		}
		if err := d.DB.Create(r).Error; err != nil {
			t.Fatalf("create run: %v", err)
		}
		return r
	}
	mkCase := func(run *model.RunRecord, code string, status string, dur int64) {
		cr := &model.CaseResult{
			Base:       model.Base{CreatedAt: run.CreatedAt},
			RunID:      run.ID,
			CaseCode:   code,
			ConfigName: "case-" + code,
			Status:     status,
			DurationMs: dur,
		}
		if err := d.DB.Create(cr).Error; err != nil {
			t.Fatalf("create case result: %v", err)
		}
	}

	// 今天：1 成功（a）+ 1 失败（b）
	r1 := mkRun(0, "success", 1, 0, 0, 100)
	mkCase(r1, "case_a", "pass", 100)
	r2 := mkRun(0, "failed", 0, 1, 0, 200)
	mkCase(r2, "case_b", "fail", 200)
	// 昨天：a 一次成一次败（不稳定）
	r3 := mkRun(1, "success", 1, 0, 0, 300)
	mkCase(r3, "case_a", "pass", 300)
	r4 := mkRun(1, "failed", 0, 1, 0, 400)
	mkCase(r4, "case_a", "fail", 400)
	// 前天：慢用例 c
	r5 := mkRun(2, "success", 1, 0, 0, 5000)
	mkCase(r5, "case_c", "pass", 5000)

	return proj.ID
}

func TestStats_Trend(t *testing.T) {
	d := testDeps(t)
	pid := statsSeed(t, d)
	svc := NewStatsService(d)

	points, err := svc.Trend(StatsQuery{ProjectID: pid, Days: 3})
	if err != nil {
		t.Fatalf("trend err: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("期望 3 天，实际 %d", len(points))
	}
	// 前天（第 0 点）：1 次成功
	if points[0].Total != 1 || points[0].Passed != 1 {
		t.Fatalf("前天聚合错误：%+v", points[0])
	}
	// 昨天（第 1 点）：2 次执行 1 成 1 败
	if points[1].Total != 2 || points[1].Passed != 1 || points[1].Failed != 1 {
		t.Fatalf("昨天聚合错误：%+v", points[1])
	}
	// 今天（第 2 点）：2 次执行 1 成 1 败 → rate 0.5
	today := points[2]
	if today.Total != 2 || today.Passed != 1 || today.Failed != 1 {
		t.Fatalf("今天聚合错误：%+v", today)
	}
	if today.Rate < 0.49 || today.Rate > 0.51 {
		t.Fatalf("今天通过率应为 0.5，实际 %f", today.Rate)
	}
}

func TestStats_Flaky(t *testing.T) {
	d := testDeps(t)
	pid := statsSeed(t, d)
	svc := NewStatsService(d)

	list, err := svc.Flaky(StatsQuery{ProjectID: pid, Days: 3}, 10)
	if err != nil {
		t.Fatalf("flaky err: %v", err)
	}
	// case_c 全过（rate=1）不进榜单；剩 case_b(0) 与 case_a(0.667)，rate 升序。
	if len(list) != 2 {
		t.Fatalf("期望 2 个用例（全过的 case_c 不进榜单），实际 %d：%+v", len(list), list)
	}
	if list[0].CaseCode != "case_b" {
		t.Fatalf("第 1 应是全败 case_b，实际 %s", list[0].CaseCode)
	}
	if list[1].CaseCode != "case_a" {
		t.Fatalf("第 2 应是不稳定 case_a，实际 %s", list[1].CaseCode)
	}
	if list[1].Passed != 2 || list[1].Runs != 3 {
		t.Fatalf("case_a 聚合错误：%+v", list[1])
	}
}

func TestStats_Slowest(t *testing.T) {
	d := testDeps(t)
	pid := statsSeed(t, d)
	svc := NewStatsService(d)

	list, err := svc.Slowest(StatsQuery{ProjectID: pid, Days: 3}, 10)
	if err != nil {
		t.Fatalf("slowest err: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("期望 3 个用例，实际 %d", len(list))
	}
	// 平均耗时：case_c=5000, case_a=(100+300+400)/3=266, case_b=200
	if list[0].CaseCode != "case_c" {
		t.Fatalf("最慢应是 case_c(5000ms)，实际 %s(avg=%d)", list[0].CaseCode, list[0].AvgMs)
	}
	if list[0].AvgMs != 5000 {
		t.Fatalf("case_c avg 应为 5000，实际 %d", list[0].AvgMs)
	}
	if list[1].CaseCode != "case_a" {
		t.Fatalf("次慢应是 case_a，实际 %s", list[1].CaseCode)
	}
}

func TestStats_Empty(t *testing.T) {
	d := testDeps(t)
	svc := NewStatsService(d)

	flaky, err := svc.Flaky(StatsQuery{ProjectID: 999, Days: 7}, 10)
	if err != nil {
		t.Fatalf("empty flaky err: %v", err)
	}
	if len(flaky) != 0 {
		t.Fatalf("空数据 flaky 应为空，实际 %d", len(flaky))
	}
	slow, err := svc.Slowest(StatsQuery{ProjectID: 999, Days: 7}, 10)
	if err != nil {
		t.Fatalf("empty slowest err: %v", err)
	}
	if len(slow) != 0 {
		t.Fatalf("空数据 slowest 应为空，实际 %d", len(slow))
	}
	trend, err := svc.Trend(StatsQuery{ProjectID: 999, Days: 7})
	if err != nil {
		t.Fatalf("empty trend err: %v", err)
	}
	if len(trend) != 7 {
		t.Fatalf("空数据 trend 应返回补零的 7 天，实际 %d", len(trend))
	}
	for _, p := range trend {
		if p.Total != 0 {
			t.Fatalf("空数据 trend 每个点 total 应为 0，实际 %+v", p)
		}
	}
}
