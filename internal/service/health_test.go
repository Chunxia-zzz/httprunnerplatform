package service

import (
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func mkHealthCase(t *testing.T, d Deps, projectID uint64, code string, lastRunID uint64) *model.TestCase {
	t.Helper()
	c := &model.TestCase{ProjectID: projectID, Code: code, Name: code, LastRunID: lastRunID}
	if err := d.DB.Create(c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func mkHealthStep(t *testing.T, d Deps, caseID uint64, seq int, url string, asserts int, enabled bool) {
	t.Helper()
	var val jsonx.Slice[model.AssertItem]
	for i := 0; i < asserts; i++ {
		val = append(val, model.AssertItem{Check: "status_code", Assert: "eq", Expect: "200"})
	}
	st := &model.TestStep{
		CaseID: caseID, Seq: seq, StepType: model.StepRequest, Enabled: enabled,
		Request: jsonx.Any{Val: map[string]any{"method": "GET", "url": url}}, Validate: val,
	}
	if err := d.DB.Create(st).Error; err != nil {
		t.Fatal(err)
	}
}

func healthCategory(report *HealthReport, key string) HealthCategory {
	for _, c := range report.Categories {
		if c.Key == key {
			return c
		}
	}
	return HealthCategory{}
}

func TestHealth_六类检测(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Health

	// 接口：一个被引用、一个没被引用
	usedAPI := mustAPI(t, d, proj.ID, APIReq{Code: "used", Name: "已引用", Method: "GET", Path: "/used", BodyType: "none"})
	mustAPI(t, d, proj.ID, APIReq{Code: "unused", Name: "未引用", Method: "GET", Path: "/unused", BodyType: "none"})

	// 1) 空壳：只有禁用步骤
	empty := mkHealthCase(t, d, proj.ID, "c_empty", 0)
	mkHealthStep(t, d, empty.ID, 1, "/empty", 1, false)

	// 2) 无断言
	noAssert := mkHealthCase(t, d, proj.ID, "c_noassert", 0)
	mkHealthStep(t, d, noAssert.ID, 1, "/noassert", 0, true)

	// 3) 重复两条（同结构）+ 其中一条引用接口
	dupe1 := mkHealthCase(t, d, proj.ID, "c_dupe1", 0)
	dupe2 := mkHealthCase(t, d, proj.ID, "c_dupe2", 0)
	for _, c := range []*model.TestCase{dupe1, dupe2} {
		st := &model.TestStep{CaseID: c.ID, Seq: 1, StepType: model.StepAPI, Enabled: true, RefAPIID: usedAPI.ID,
			Validate: jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq", Expect: "200"}}}
		if err := d.DB.Create(st).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 4) 长期未执行：40 天前跑过
	oldRun := &model.RunRecord{Base: model.Base{CreatedAt: time.Now().AddDate(0, 0, -40)},
		ProjectID: proj.ID, Status: model.RunSuccess}
	if err := d.DB.Create(oldRun).Error; err != nil {
		t.Fatal(err)
	}
	stale := mkHealthCase(t, d, proj.ID, "c_stale", oldRun.ID)
	mkHealthStep(t, d, stale.ID, 1, "/stale", 1, true)

	report, err := svc.Analyze(proj.ID, HealthOptions{Days: 30})
	if err != nil {
		t.Fatal(err)
	}

	if got := healthCategory(report, HealthEmptyCase); got.Count != 1 || got.Items[0].Key != "c_empty" {
		t.Fatalf("空壳 = %+v", got)
	}
	if got := healthCategory(report, HealthNoAssert); got.Count < 1 {
		t.Fatalf("无断言 = %+v", got)
	}
	dup := healthCategory(report, HealthDuplicateCase)
	if dup.Count != 2 {
		t.Fatalf("重复用例应列出组内 2 条: %+v", dup)
	}
	if got := healthCategory(report, HealthStale); got.Count != 1 || got.Items[0].Key != "c_stale" {
		t.Fatalf("长期未执行 = %+v", got)
	}
	unused := healthCategory(report, HealthUnusedAPI)
	if unused.Count != 1 || unused.Items[0].Key != "unused" {
		t.Fatalf("未引用接口 = %+v", unused)
	}
	// 从未执行：c_empty/c_noassert/c_dupe1/c_dupe2 且不含 c_stale（互斥）
	never := healthCategory(report, HealthNeverRun)
	for _, it := range never.Items {
		if it.Key == "c_stale" {
			t.Fatal("长期未执行不应同时出现在从未执行里")
		}
	}

	// 评分 = 100 - 各类扣分之和（下限 0）
	var sum float64
	for _, dd := range report.Deductions {
		sum += dd.Penalty
	}
	want := 100 - sum
	if want < 0 {
		want = 0
	}
	if report.Score != round1(want) {
		t.Fatalf("评分 = %.1f, want %.1f（扣分 %+v）", report.Score, want, report.Deductions)
	}
}

func TestHealth_天数阈值与空项目(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Health

	// 空项目：100 分，清单为空
	empty, err := svc.Analyze(proj.ID, HealthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Score != 100 || len(empty.Categories) != 6 {
		t.Fatalf("空项目 = %.1f / %d 类", empty.Score, len(empty.Categories))
	}
	for _, c := range empty.Categories {
		if c.Count != 0 {
			t.Fatalf("%s 应为空: %+v", c.Key, c.Items)
		}
	}

	// 10 天前跑过：days=30 不算僵尸，days=5 算
	run := &model.RunRecord{Base: model.Base{CreatedAt: time.Now().AddDate(0, 0, -10)},
		ProjectID: proj.ID, Status: model.RunSuccess}
	if err := d.DB.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	c := mkHealthCase(t, d, proj.ID, "c_recent", run.ID)
	mkHealthStep(t, d, c.ID, 1, "/recent", 1, true)

	r30, _ := svc.Analyze(proj.ID, HealthOptions{Days: 30})
	if got := healthCategory(r30, HealthStale).Count; got != 0 {
		t.Fatalf("30 天内跑过不应算僵尸: %d", got)
	}
	r5, _ := svc.Analyze(proj.ID, HealthOptions{Days: 5})
	if got := healthCategory(r5, HealthStale).Count; got != 1 {
		t.Fatalf("5 天阈值应算僵尸: %d", got)
	}
}
