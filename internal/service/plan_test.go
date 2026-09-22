package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// 本文件覆盖测试计划的三类规则：
//
//  1. cron 与时区的校验 —— 这两处配错不会报错，只会让计划静默错位，
//     所以必须在保存时就拦住；
//  2. 成员（用例集）的全量替换与跨项目拒绝；
//  3. "下次执行时间"的预览 —— 用户靠它确认自己配对了没有。
//
// 真正"到点触发"的那条路径在 internal/scheduler 里测（那里自带重入保护）。

func mustPlan(t *testing.T, d Deps, projectID uint64, name string) *model.TestPlan {
	t.Helper()
	p, err := New(d).Plan.Create(projectID, CreatePlanReq{Name: name})
	if err != nil {
		t.Fatalf("创建计划 %q 失败: %v", name, err)
	}
	return p
}

// ---------------------------------------------------------------------------
// 创建与校验
// ---------------------------------------------------------------------------

func Test计划创建默认值(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	pl := mustPlan(t, d, p.ID, "冒烟计划")

	if pl.TriggerType != model.TriggerManual {
		t.Errorf("trigger_type = %q, want manual", pl.TriggerType)
	}
	// ⭐ 默认时区是 Asia/Shanghai 而不是 UTC：目标用户填 `0 9 * * *` 时
	// 想的是北京时间早上 9 点。默认 UTC 会让大多数人的第一个定时计划错位 8 小时。
	if pl.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone = %q, want Asia/Shanghai", pl.Timezone)
	}
	if pl.CronExpr != "" {
		t.Errorf("cron_expr = %q, want 空", pl.CronExpr)
	}
	if pl.Enabled {
		t.Errorf("enabled = true, want false（新建计划默认不启用，避免一保存就开始跑）")
	}
}

func Test计划cron在保存时就校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	// 6 段（带秒）要拒绝：平台只接受 5 段，混着用会让"为什么它在跑"更难解释。
	bad := []struct {
		name string
		cron string
		tz   string
		hint string
	}{
		{"6 段带秒", "0 0 9 * * *", "", "5 段"},
		{"不是 cron", "每天九点", "", ""},
		{"空表达式", "", "", ""},
		{"分钟越界", "99 * * * *", "", ""},
		{"段数不足", "0 9 *", "", ""},
		{"时区非法", "0 9 * * *", "Asia/Beijing", ""},
		{"时区拼错", "0 9 * * *", "Asia/Shangha", ""},
	}
	for _, c := range bad {
		_, err := New(d).Plan.Create(p.ID, CreatePlanReq{
			Name: "定时" + c.name, TriggerType: model.TriggerCron, CronExpr: c.cron, Timezone: c.tz,
		})
		if err == nil {
			t.Errorf("%s（cron=%q tz=%q）应被拒绝", c.name, c.cron, c.tz)
			continue
		}
		if codeOfErr(t, err) != 40000 {
			t.Errorf("%s 的错误码 = %d, want 40000", c.name, codeOfErr(t, err))
		}
		_ = c.hint
	}

	// 合法表达式要能通过
	ok := []string{"0 9 * * *", "*/15 * * * *", "30 9 * * 1-5", "@daily", "@every 30m"}
	for _, expr := range ok {
		pl, err := New(d).Plan.Create(p.ID, CreatePlanReq{
			Name: "定时" + expr, TriggerType: model.TriggerCron, CronExpr: expr,
		})
		if err != nil {
			t.Errorf("cron %q 应被接受: %v", expr, err)
		} else if pl.CronExpr != expr {
			t.Errorf("cron %q 落库后变成 %q", expr, pl.CronExpr)
		}
	}
}

func Test计划手动触发时不校验cron(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	// 用户可能先建手动计划、之后再改成定时。此时 cron 为空是合法状态，
	// 强行要求填表达式会让"建个手动计划"这件小事变得很烦。
	pl, err := New(d).Plan.Create(p.ID, CreatePlanReq{Name: "手动计划"})
	if err != nil {
		t.Fatalf("手动计划应能创建: %v", err)
	}
	if pl.CronExpr != "" {
		t.Errorf("cron_expr = %q, want 空", pl.CronExpr)
	}
}

func Test计划触发方式不接受ci(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	// ci 是"由 CI Token 触发时记下的值"，用户手动选它会得到一个
	// 配好了但永远不会自己触发的计划。
	_, err := New(d).Plan.Create(p.ID, CreatePlanReq{
		Name: "ci计划", TriggerType: model.TriggerCI, CronExpr: "0 9 * * *",
	})
	if codeOfErr(t, err) != 40000 {
		t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
	}

	_, err = New(d).Plan.Create(p.ID, CreatePlanReq{Name: "乱填", TriggerType: "weekly"})
	if codeOfErr(t, err) != 40000 {
		t.Errorf("未知触发方式的错误码 = %d, want 40000", codeOfErr(t, err))
	}
}

func Test计划名称必填且项目内唯一(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan

	if _, err := svc.Create(p.ID, CreatePlanReq{}); codeOfErr(t, err) != 40000 {
		t.Errorf("空名称的错误码 = %d, want 40000", codeOfErr(t, err))
	}
	mustPlan(t, d, p.ID, "重名")
	if _, err := svc.Create(p.ID, CreatePlanReq{Name: "重名"}); codeOfErr(t, err) != 40001 {
		t.Errorf("重名的错误码 = %d, want 40001", codeOfErr(t, err))
	}
	// 不同项目下允许同名
	p2 := seedProject(t, d)
	if _, err := svc.Create(p2.ID, CreatePlanReq{Name: "重名"}); err != nil {
		t.Errorf("不同项目下允许同名: %v", err)
	}
}

func Test计划不存在(t *testing.T) {
	d := testDeps(t)
	svc := New(d).Plan
	if _, err := svc.Get(9999); codeOfErr(t, err) != 40002 {
		t.Errorf("错误码 = %d, want 40002", codeOfErr(t, err))
	}
	if _, err := svc.View(9999); codeOfErr(t, err) != 40002 {
		t.Errorf("View 的错误码 = %d, want 40002", codeOfErr(t, err))
	}
	if err := svc.Delete(9999); codeOfErr(t, err) != 40002 {
		t.Errorf("Delete 的错误码 = %d, want 40002", codeOfErr(t, err))
	}
}

// ---------------------------------------------------------------------------
// 下次执行时间与 cron 中文说明
// ---------------------------------------------------------------------------

// ⭐ 这是本文件最重要的一条：时区不生效的话，计划会静默错位 8 小时，
// 而且不会有任何报错 —— 发现它往往要等好几周。
func Test计划下次执行时间按计划时区计算(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("本机没有 Asia/Shanghai 时区数据: %v", err)
	}
	utc, _ := time.LoadLocation("UTC")

	cases := []struct {
		name string
		tz   string
		cron string
		// from 是"现在"，wantLocal 是期望的下次触发时刻（在计划时区下）
		from      time.Time
		wantLocal string
	}{
		{
			name:      "上海时区 0 9 * * *：下一个 9 点是明天",
			tz:        "Asia/Shanghai",
			cron:      "0 9 * * *",
			from:      time.Date(2026, 3, 10, 15, 0, 0, 0, shanghai), // 15:00，已过 9 点
			wantLocal: "2026-03-11 09:00",
		},
		{
			name:      "UTC 时区 0 9 * * *：同样的表达式，落在不同时刻",
			tz:        "UTC",
			cron:      "0 9 * * *",
			from:      time.Date(2026, 3, 10, 15, 0, 0, 0, utc),
			wantLocal: "2026-03-11 09:00",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := nextFireAt(c.cron, c.tz, c.from)
			if err != nil {
				t.Fatalf("nextFireAt 失败: %v", err)
			}
			// 换回计划时区再比较：比的是"当地几点"，不是绝对时刻
			loc := utc
			if c.tz != "" {
				if l, err2 := time.LoadLocation(c.tz); err2 == nil {
					loc = l
				}
			}
			if s := got.In(loc).Format("2006-01-02 15:04"); s != c.wantLocal {
				t.Errorf("下次执行（%s）= %s, want %s", c.tz, s, c.wantLocal)
			}
		})
	}

	// 两个时区算出来的**绝对时刻**必须不同，否则说明时区根本没生效
	a, _ := nextFireAt("0 9 * * *", "Asia/Shanghai", time.Date(2026, 3, 10, 0, 0, 0, 0, utc))
	b, _ := nextFireAt("0 9 * * *", "UTC", time.Date(2026, 3, 10, 0, 0, 0, 0, utc))
	if a.Equal(b) {
		t.Errorf("上海与 UTC 算出的下次执行时刻相同（%v），时区没有生效", a)
	}
	if a.Sub(b) != -8*time.Hour {
		t.Errorf("两地相差 %v, want -8h（北京时间 9 点 = UTC 1 点）", a.Sub(b))
	}

	// 视图里也要带上，用户靠它确认自己配对了
	pl, err := svc.Create(p.ID, CreatePlanReq{
		Name: "每天九点", TriggerType: model.TriggerCron, CronExpr: "0 9 * * *", Timezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("创建计划失败: %v", err)
	}
	v, err := svc.View(pl.ID)
	if err != nil {
		t.Fatalf("View 失败: %v", err)
	}
	if v.NextFireAt == nil || *v.NextFireAt == "" {
		t.Fatal("定时计划必须给出下次执行时间")
	}
	if !strings.Contains(v.CronHuman, "09:00") {
		t.Errorf("cron_human = %q, 应包含 09:00", v.CronHuman)
	}
	t.Logf("cron=%s 人类可读=%q 下次=%s（%s）", v.CronExpr, v.CronHuman, *v.NextFireAt, v.Timezone)
}

func Test计划cron中文说明(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"0 9 * * *", "每天 09:00"},
		{"30 2 * * *", "每天 02:30"},
		{"0 * * * *", "每小时的第 0 分"},
		{"*/15 * * * *", "每 15 分钟"},
		{"* * * * *", "每分钟"},
		{"0 9 * * 1-5", "工作日 9 点"},
		{"30 9 * * 1-5", "工作日 9 点第 30 分"},
		{"@daily", "每天 00:00"},
		{"@hourly", "每小时"},
		{"@every 30m", "每 30m"},
	}
	for _, c := range cases {
		if got := describeCronHuman(c.expr); got != c.want {
			t.Errorf("describeCronHuman(%q) = %q, want %q", c.expr, got, c.want)
		}
	}
	// 覆盖不到的写法原样返回：一句看不懂的原文好过一句意思错了的中文
	if got := describeCronHuman("1,7,13 * * * *"); got == "" {
		t.Error("未覆盖的表达式不应返回空")
	}
}

// ---------------------------------------------------------------------------
// 成员
// ---------------------------------------------------------------------------

func Test计划成员顺序与去重(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan
	pl := mustPlan(t, d, p.ID, "成员计划")

	s1 := mustSuite(t, d, p.ID, "s1")
	s2 := mustSuite(t, d, p.ID, "s2")

	views, err := svc.SetSuites(pl.ID, []uint64{s2.ID, s1.ID})
	if err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}
	if len(views) != 2 || views[0].SuiteCode != "s2" || views[1].SuiteCode != "s1" {
		t.Fatalf("成员顺序 = [%s %s], want [s2 s1]", views[0].SuiteCode, views[1].SuiteCode)
	}

	if _, err := svc.SetSuites(pl.ID, []uint64{s1.ID, s1.ID}); codeOfErr(t, err) != 40001 {
		t.Errorf("重复成员的错误码 = %d, want 40001", codeOfErr(t, err))
	}
	if _, err := svc.SetSuites(pl.ID, []uint64{0}); codeOfErr(t, err) != 40000 {
		t.Errorf("空 ID 的错误码 = %d, want 40000", codeOfErr(t, err))
	}

	// 跨项目要拒绝：放到执行时才发现的话，
	// 用户会看到"计划跑了但少了几个用例集"却不知道原因。
	p2 := seedProject(t, d)
	other := mustSuite(t, d, p2.ID, "other")
	if _, err := svc.SetSuites(pl.ID, []uint64{s1.ID, other.ID}); codeOfErr(t, err) != 40000 {
		t.Errorf("跨项目成员的错误码 = %d, want 40000", codeOfErr(t, err))
	}
	// 失败后成员不能被改成半截：SetSuites 是"先删光再写入"的事务，
	// 校验在事务之前，因此失败时应该还是上一次的 [s2 s1]，且不含 other。
	views, _ = svc.Suites(pl.ID)
	if len(views) != 2 || views[0].SuiteCode != "s2" || views[1].SuiteCode != "s1" {
		codes := make([]string, 0, len(views))
		for _, v := range views {
			codes = append(codes, v.SuiteCode)
		}
		t.Errorf("失败后成员 = %v, 应保持上一次的 [s2 s1]", codes)
	}

	// 清空
	if views, err = svc.SetSuites(pl.ID, []uint64{}); err != nil || len(views) != 0 {
		t.Errorf("清空成员失败: views=%v err=%v", views, err)
	}
}

func Test计划成员标出跑不了的用例集(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan
	pl := mustPlan(t, d, p.ID, "成员可见性")

	empty := mustSuite(t, d, p.ID, "empty") // 没有用例
	del := mustSuite(t, d, p.ID, "deleted")
	svc2 := New(d).Suite
	if _, err := svc2.SetMembers(del.ID, []uint64{}); err != nil {
		t.Fatalf("设置用例集成员失败: %v", err)
	}

	if _, err := svc.SetSuites(pl.ID, []uint64{empty.ID, del.ID}); err != nil {
		t.Fatalf("设置计划成员失败: %v", err)
	}
	// 直接软删用例集：HTTP 层有"被计划引用不许删"的保护，
	// 这条分支靠 DB 层构造（与用例集侧的思路一致）。
	if err := d.DB.Delete(&model.TestSuite{}, del.ID).Error; err != nil {
		t.Fatalf("删除用例集失败: %v", err)
	}

	views, err := svc.Suites(pl.ID)
	if err != nil {
		t.Fatalf("读取成员失败: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("成员 = %d, want 2", len(views))
	}
	if views[0].Runnable {
		t.Errorf("空用例集应标为 runnable=false（跑起来一条用例都不会执行）")
	}
	if views[0].SkipReason == "" {
		t.Error("跑不了的成员必须给出原因")
	}
	if views[1].SuiteCode != "(已删除)" || views[1].Runnable {
		t.Errorf("已删除用例集 = %q / runnable=%v, want (已删除) / false",
			views[1].SuiteCode, views[1].Runnable)
	}
}

func Test计划成员里用例集有成员才算能跑(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan
	pl := mustPlan(t, d, p.ID, "有成员")

	su := mustSuite(t, d, p.ID, "withcase")
	cid := mustCaseOnly(t, d, p.ID, "c1")
	if _, err := New(d).Suite.SetMembers(su.ID, []uint64{cid}); err != nil {
		t.Fatalf("设置用例集成员失败: %v", err)
	}
	if _, err := svc.SetSuites(pl.ID, []uint64{su.ID}); err != nil {
		t.Fatalf("设置计划成员失败: %v", err)
	}

	views, err := svc.Suites(pl.ID)
	if err != nil {
		t.Fatalf("读取成员失败: %v", err)
	}
	if !views[0].Runnable || views[0].CaseCount != 1 {
		t.Errorf("runnable=%v case_count=%d, want true / 1", views[0].Runnable, views[0].CaseCount)
	}
}

// ---------------------------------------------------------------------------
// 更新、开关与删除
// ---------------------------------------------------------------------------

func Test计划更新与开关(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan
	pl := mustPlan(t, d, p.ID, "原计划")

	yes := true
	up, err := svc.Update(pl.ID, UpdatePlanReq{
		Name:        "新计划",
		TriggerType: model.TriggerCron,
		CronExpr:    "0 9 * * *",
		Timezone:    "Asia/Shanghai",
		Enabled:     &yes,
	})
	if err != nil {
		t.Fatalf("更新计划失败: %v", err)
	}
	if up.Name != "新计划" || !up.Enabled || up.TriggerType != model.TriggerCron {
		t.Errorf("更新结果 = %+v", up)
	}
	// Enabled 用指针：false 是合法值，不能跟"没传"混在一起
	no := false
	if up, err = svc.SetEnabled(pl.ID, false); err != nil || up.Enabled {
		t.Errorf("关闭计划失败: enabled=%v err=%v", up.Enabled, err)
	}
	_ = no

	// 改成手动时 cron 被清空：留着一个永远不生效的表达式只会让人困惑
	up, err = svc.Update(pl.ID, UpdatePlanReq{Name: "新计划", TriggerType: model.TriggerManual})
	if err != nil {
		t.Fatalf("改成手动失败: %v", err)
	}
	if up.CronExpr != "" {
		t.Errorf("改成手动后 cron_expr = %q, want 空", up.CronExpr)
	}
	if _, err := svc.Update(pl.ID, UpdatePlanReq{Name: ""}); codeOfErr(t, err) != 40000 {
		t.Errorf("空名称的错误码 = %d, want 40000", codeOfErr(t, err))
	}
}

func Test计划删除会清掉成员(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan
	pl := mustPlan(t, d, p.ID, "待删")
	su := mustSuite(t, d, p.ID, "s1")
	if _, err := svc.SetSuites(pl.ID, []uint64{su.ID}); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}

	if err := svc.Delete(pl.ID); err != nil {
		t.Fatalf("删除计划失败: %v", err)
	}
	var n int64
	if err := d.DB.Model(&model.PlanSuite{}).Where("plan_id = ?", pl.ID).Count(&n).Error; err != nil {
		t.Fatalf("统计成员失败: %v", err)
	}
	if n != 0 {
		t.Errorf("删除计划后仍留下 %d 条成员关联（会变成悬空 ID）", n)
	}
	// 关联清掉之后，用例集应该能删了
	if err := New(d).Suite.Delete(su.ID); err != nil {
		t.Errorf("计划删除后用例集应可删除: %v", err)
	}
}

func Test计划列表带成员数与关键字过滤(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Plan
	pl := mustPlan(t, d, p.ID, "每日回归")
	su := mustSuite(t, d, p.ID, "s1")
	if _, err := svc.SetSuites(pl.ID, []uint64{su.ID}); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}

	list, total, err := svc.List(p.ID, PlanListQuery{}, Page{})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("total=%d len=%d, want 1/1", total, len(list))
	}
	if list[0].SuiteCount != 1 {
		t.Errorf("suite_count = %d, want 1", list[0].SuiteCount)
	}

	empty := mustPlan(t, d, p.ID, "空计划")
	if _, err := svc.SetEnabled(empty.ID, true); err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	yes := true
	no := false
	if _, total, err = svc.List(p.ID, PlanListQuery{Enabled: &yes}, Page{}); err != nil || total != 1 {
		t.Errorf("enabled=true 过滤: total=%d err=%v, want 1", total, err)
	}
	if _, total, err = svc.List(p.ID, PlanListQuery{Enabled: &no}, Page{}); err != nil || total != 1 {
		t.Errorf("enabled=false 过滤: total=%d err=%v, want 1", total, err)
	}
	if _, total, err = svc.List(p.ID, PlanListQuery{Keyword: "回归"}, Page{}); err != nil || total != 1 {
		t.Errorf("关键字过滤: total=%d err=%v, want 1", total, err)
	}
}
