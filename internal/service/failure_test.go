package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func mkFailRun(t *testing.T, d Deps, projectID uint64, at time.Time) *model.RunRecord {
	t.Helper()
	r := &model.RunRecord{Base: model.Base{CreatedAt: at}, ProjectID: projectID, Status: model.RunFailed}
	if err := d.DB.Create(r).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

// mkFailCase 造一条失败用例；statusCode > 0 时带响应快照，否则带步骤错误。
func mkFailCase(t *testing.T, d Deps, run *model.RunRecord, code string, statusCode int, stepErr string, assertCheck string) *model.CaseResult {
	t.Helper()
	c := &model.CaseResult{
		RunID: run.ID, CaseID: 1, CaseCode: code, ConfigName: code,
		Status: model.StatusFail, Attribution: model.AttrSystemUnderTest,
		ErrorMsg: "step failed", StepTotal: 1,
	}
	if statusCode >= 500 {
		c.Attribution = model.AttrEnvironment
	}
	if err := d.DB.Create(c).Error; err != nil {
		t.Fatal(err)
	}
	snap := jsonx.Any{}
	if statusCode > 0 {
		snap = jsonx.Any{Val: map[string]any{"status_code": statusCode}}
	}
	st := &model.StepResult{
		RunID: run.ID, CaseResultID: c.ID, CaseCode: code, Seq: 1, StepName: "s1",
		StepType: model.StepRequest, Status: model.StatusFail,
		FinalURL: "http://h/orders/42", ErrorMsg: stepErr,
		RequestSnapshot:  jsonx.Any{Val: map[string]any{"method": "GET", "url": "http://h/orders/42"}},
		ResponseSnapshot: snap,
	}
	if err := d.DB.Create(st).Error; err != nil {
		t.Fatal(err)
	}
	if assertCheck != "" {
		a := &model.AssertionResult{
			RunID: run.ID, CaseResultID: c.ID, StepResultID: st.ID, Seq: 1,
			CheckExpr: assertCheck, AssertMethod: "eq", ExpectValue: "200",
			CheckValue: "500", Passed: false,
		}
		if err := d.DB.Create(a).Error; err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestFailureClusters_同因合并与分簇(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	run := mkFailRun(t, d, proj.ID, time.Now())
	svc := New(d).Failure

	// 3 条 502（同接口）+ 2 条断言失败（同断言）
	for _, code := range []string{"c500_1", "c500_2", "c500_3"} {
		mkFailCase(t, d, run, code, 502, "", "")
	}
	mkFailCase(t, d, run, "c_assert_1", 200, "", "body.code")
	mkFailCase(t, d, run, "c_assert_2", 200, "", "body.code")

	res, err := svc.Clusters(proj.ID, run.ID, 30, 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalFailedCases != 5 {
		t.Fatalf("失败用例数 = %d", res.TotalFailedCases)
	}
	if len(res.Clusters) != 2 {
		t.Fatalf("簇数 = %d: %+v", len(res.Clusters), res.Clusters)
	}
	first, second := res.Clusters[0], res.Clusters[1]
	if first.Count != 3 || second.Count != 2 {
		t.Fatalf("簇计数 = %d/%d", first.Count, second.Count)
	}
	if first.Title != "HTTP 502 · GET /orders/:id" {
		t.Fatalf("HTTP 簇标题 = %q", first.Title)
	}
	if second.Title != "断言失败：body.code eq 200" {
		t.Fatalf("断言簇标题 = %q", second.Title)
	}
	if first.Attribution != model.AttrEnvironment {
		t.Fatalf("HTTP 簇归因 = %q", first.Attribution)
	}
	if first.Hint == "" || second.Hint == "" {
		t.Fatal("每簇都应给出建议")
	}
	// 同一用例只出现一次（断言簇里两条各不相同）
	seen := map[string]int{}
	for _, cl := range res.Clusters {
		for _, c := range cl.Cases {
			seen[c.CaseCode]++
		}
	}
	for code, n := range seen {
		if n != 1 {
			t.Fatalf("用例 %s 出现在 %d 个簇里", code, n)
		}
	}
}

func TestFailureClusters_步骤错误与窗口合并(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	svc := New(d).Failure

	// 两次执行各 1 条连接失败（错误信息数字不同，应归一成同簇）
	r1 := mkFailRun(t, d, proj.ID, time.Now().Add(-2*time.Hour))
	r2 := mkFailRun(t, d, proj.ID, time.Now().Add(-1*time.Hour))
	mkFailCase(t, d, r1, "c_conn_1", 0, "dial tcp 10.0.0.1:80: connect: connection refused", "")
	mkFailCase(t, d, r2, "c_conn_2", 0, "dial tcp 10.0.0.2:80: connect: connection refused", "")

	// 单次：各 1 簇
	single, err := svc.Clusters(proj.ID, r1.ID, 30, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Clusters) != 1 || single.Clusters[0].Count != 1 {
		t.Fatalf("单次聚类 = %+v", single.Clusters)
	}
	if !strings.HasPrefix(single.Clusters[0].Title, "步骤失败：") {
		t.Fatalf("标题应为步骤失败: %q", single.Clusters[0].Title)
	}

	// 窗口：同因合并成 1 簇 2 条
	win, err := svc.Clusters(proj.ID, 0, 30, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(win.Clusters) != 1 || win.Clusters[0].Count != 2 {
		t.Fatalf("窗口聚类应合并同因: %+v", win.Clusters)
	}
}

func TestFailureClusters_空窗口与越权(t *testing.T) {
	d := testDeps(t)
	proj := seedProject(t, d)
	other := seedProject(t, d)
	svc := New(d).Failure

	res, err := svc.Clusters(proj.ID, 0, 30, 20)
	if err != nil || res.TotalFailedCases != 0 || len(res.Clusters) != 0 {
		t.Fatalf("空窗口 = %+v, %v", res, err)
	}

	run := mkFailRun(t, d, other.ID, time.Now())
	if _, err := svc.Clusters(proj.ID, run.ID, 30, 20); err == nil {
		t.Fatal("跨项目查询应被拒绝")
	}
}
