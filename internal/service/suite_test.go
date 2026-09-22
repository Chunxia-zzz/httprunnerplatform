package service

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// ---------------------------------------------------------------------------
// 脚手架
// ---------------------------------------------------------------------------

// mustSuite 建一个用例集，失败即终止测试。
func mustSuite(t *testing.T, d Deps, projectID uint64, code string) *model.TestSuite {
	t.Helper()
	su, err := New(d).Suite.Create(projectID, CreateSuiteReq{Code: code, Name: "用例集" + code})
	if err != nil {
		t.Fatalf("创建用例集 %q 失败: %v", code, err)
	}
	return su
}

// mustCaseOnly 建一个最简用例（不关心步骤），返回 ID。
func mustCaseOnly(t *testing.T, d Deps, projectID uint64, code string) uint64 {
	t.Helper()
	tc := &model.TestCase{
		ProjectID: projectID,
		Code:      code,
		Name:      "用例" + code,
		Status:    model.CaseStatusActive,
	}
	if err := d.DB.Create(tc).Error; err != nil {
		t.Fatalf("创建用例 %q 失败: %v", code, err)
	}
	return tc.ID
}

// ---------------------------------------------------------------------------
// 创建
// ---------------------------------------------------------------------------

func Test用例集创建默认值为串行与遇错继续(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	su := mustSuite(t, d, p.ID, "smoke")

	// 默认值是设计文档决策 2 的落地：遇错继续，而不是遇错即停。
	if su.ExecuteMode != model.ExecuteSequential {
		t.Errorf("execute_mode = %q, want sequential", su.ExecuteMode)
	}
	if su.OnFailure != model.OnFailureContinue {
		t.Errorf("on_failure = %q, want continue（设计决策 2）", su.OnFailure)
	}
	if su.Timeout != 0 {
		t.Errorf("timeout = %d, want 0（不设限）", su.Timeout)
	}
}

func Test用例集标识非法(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	for _, code := range []string{"", "有中文", "a/b", "a b"} {
		if _, err := New(d).Suite.Create(p.ID, CreateSuiteReq{Code: code, Name: "x"}); err == nil {
			t.Errorf("标识 %q 应被拒绝", code)
		} else if codeOfErr(t, err) != 40004 {
			t.Errorf("标识 %q 的错误码 = %d, want 40004", code, codeOfErr(t, err))
		}
	}
}

func Test用例集标识在项目内重复(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	mustSuite(t, d, p.ID, "dup")

	_, err := New(d).Suite.Create(p.ID, CreateSuiteReq{Code: "dup", Name: "再来一次"})
	if err == nil {
		t.Fatal("同项目下重复标识应被拒绝")
	}
	if codeOfErr(t, err) != 40001 {
		t.Errorf("错误码 = %d, want 40001", codeOfErr(t, err))
	}

	// 但不同项目下可以重名：code 只在项目内唯一。
	p2 := seedProject(t, d)
	if _, err := New(d).Suite.Create(p2.ID, CreateSuiteReq{Code: "dup", Name: "另一个项目"}); err != nil {
		t.Errorf("不同项目下允许同标识: %v", err)
	}
}

func Test用例集名称不能为空(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	if _, err := New(d).Suite.Create(p.ID, CreateSuiteReq{Code: "x1"}); codeOfErr(t, err) != 40000 {
		t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
	}
	// 更新时同样要校验，否则"先建后清空"能绕过
	su := mustSuite(t, d, p.ID, "x2")
	if _, err := New(d).Suite.Update(su.ID, UpdateSuiteReq{Name: "  "}); codeOfErr(t, err) != 40000 {
		t.Errorf("更新为空名的错误码 = %d, want 40000", codeOfErr(t, err))
	}
}

// parallel 必须明确拒绝，而不是悄悄串行执行。
func Test并行执行明确拒绝而不是悄悄降级(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	_, err := New(d).Suite.Create(p.ID, CreateSuiteReq{Code: "par", Name: "并行", ExecuteMode: "parallel"})
	if err == nil {
		t.Fatal("parallel 应被明确拒绝")
	}
	if codeOfErr(t, err) != 40000 {
		t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
	}

	su := mustSuite(t, d, p.ID, "seq")
	if _, err := New(d).Suite.Update(su.ID, UpdateSuiteReq{Name: "x", ExecuteMode: "parallel"}); codeOfErr(t, err) != 40000 {
		t.Errorf("更新为 parallel 的错误码 = %d, want 40000", codeOfErr(t, err))
	}
}

func Test遇错行为只接受两种取值(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	if _, err := New(d).Suite.Create(p.ID, CreateSuiteReq{
		Code: "of", Name: "x", OnFailure: "skip",
	}); codeOfErr(t, err) != 40000 {
		t.Errorf("非法 on_failure 的错误码 = %d, want 40000", codeOfErr(t, err))
	}

	su, err := New(d).Suite.Create(p.ID, CreateSuiteReq{Code: "of2", Name: "x", OnFailure: "abort"})
	if err != nil {
		t.Fatalf("abort 应被接受: %v", err)
	}
	if su.OnFailure != model.OnFailureAbort {
		t.Errorf("on_failure = %q, want abort", su.OnFailure)
	}
}

func Test用例集超时不能为负(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "neg")
	if _, err := New(d).Suite.Update(su.ID, UpdateSuiteReq{Name: "x", Timeout: -1}); codeOfErr(t, err) != 40000 {
		t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
	}
}

// ---------------------------------------------------------------------------
// 成员
// ---------------------------------------------------------------------------

func Test设置成员后顺序即执行顺序(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "ord")

	c1 := mustCaseOnly(t, d, p.ID, "c1")
	c2 := mustCaseOnly(t, d, p.ID, "c2")
	c3 := mustCaseOnly(t, d, p.ID, "c3")

	svc := New(d).Suite
	// 故意按 c3, c1, c2 的顺序提交
	if _, err := svc.SetMembers(su.ID, []uint64{c3, c1, c2}); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}
	members, err := svc.Members(su.ID)
	if err != nil {
		t.Fatalf("读取成员失败: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("成员数 = %d, want 3", len(members))
	}
	want := []uint64{c3, c1, c2}
	for i, m := range members {
		if m.CaseID != want[i] {
			t.Errorf("第 %d 位 = %d, want %d（数组顺序应即执行顺序）", i, m.CaseID, want[i])
		}
		if m.Seq != i+1 {
			t.Errorf("第 %d 位 seq = %d, want %d", i, m.Seq, i+1)
		}
		if !m.Runnable {
			t.Errorf("第 %d 位应可运行", i)
		}
	}

	// 全量替换后旧成员不应残留
	if _, err := svc.SetMembers(su.ID, []uint64{c1}); err != nil {
		t.Fatalf("重设成员失败: %v", err)
	}
	members, _ = svc.Members(su.ID)
	if len(members) != 1 || members[0].CaseID != c1 {
		t.Errorf("重设后成员 = %+v, want 只剩 c1", members)
	}
}

func Test重复加入同一用例被拒(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "dupm")
	c1 := mustCaseOnly(t, d, p.ID, "c1")

	_, err := New(d).Suite.SetMembers(su.ID, []uint64{c1, c1})
	if err == nil {
		t.Fatal("同一用例重复加入应被拒绝（会破坏通过率的分母）")
	}
	if codeOfErr(t, err) != 40001 {
		t.Errorf("错误码 = %d, want 40001", codeOfErr(t, err))
	}
}

func Test成员不能跨项目(t *testing.T) {
	d := testDeps(t)
	p1 := seedProject(t, d)
	p2 := seedProject(t, d)
	su := mustSuite(t, d, p1.ID, "cross")
	own := mustCaseOnly(t, d, p1.ID, "own")
	foreign := mustCaseOnly(t, d, p2.ID, "foreign")

	_, err := New(d).Suite.SetMembers(su.ID, []uint64{own, foreign})
	if err == nil {
		t.Fatal("跨项目成员应被拒绝")
	}
	if codeOfErr(t, err) != 40000 {
		t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
	}
	// 失败后成员应保持不变，而不是写进去一半
	if m, _ := New(d).Suite.Members(su.ID); len(m) != 0 {
		t.Errorf("失败的写入不应留下部分成员，实际 %d 条", len(m))
	}
}

func Test成员列表不接受空ID(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "zero")
	if _, err := New(d).Suite.SetMembers(su.ID, []uint64{0}); codeOfErr(t, err) != 40000 {
		t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
	}
}

func Test清空成员是合法操作(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "clr")
	c1 := mustCaseOnly(t, d, p.ID, "c1")
	svc := New(d).Suite
	if _, err := svc.SetMembers(su.ID, []uint64{c1}); err != nil {
		t.Fatalf("设置失败: %v", err)
	}
	// 空数组 = 清空，不是"没传"
	if _, err := svc.SetMembers(su.ID, []uint64{}); err != nil {
		t.Fatalf("清空成员失败: %v", err)
	}
	if m, _ := svc.Members(su.ID); len(m) != 0 {
		t.Errorf("清空后仍有 %d 条成员", len(m))
	}
}

// 已禁用 / 已删除的用例必须在成员列表里看得出来 ——
// 否则用户只会看到"预期 10 条实际 8 条"，却不知道少了哪两条。
func Test已禁用与已删除的成员要标出来(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "st")

	ok := mustCaseOnly(t, d, p.ID, "ok1")
	disabled := mustCaseOnly(t, d, p.ID, "dis1")
	deleted := mustCaseOnly(t, d, p.ID, "del1")

	svc := New(d).Suite

	// ⚠️ 时序必须这样：先加入成员（此时用例都还活着），之后用例才被禁用/删除。
	// 反过来（先删再加）会被 SetMembers 拒绝 —— 已删除的用例本来就不该能被加入，
	// 那是另一条规则，由 Test成员不能跨项目 之类覆盖。
	// 这里要验的是"加入之后被弄坏了怎么办"。
	if _, err := svc.SetMembers(su.ID, []uint64{ok, disabled, deleted}); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}

	if err := d.DB.Model(&model.TestCase{}).Where("id = ?", disabled).
		Update("status", model.CaseStatusDisabled).Error; err != nil {
		t.Fatalf("禁用用例失败: %v", err)
	}
	if err := d.DB.Delete(&model.TestCase{}, deleted).Error; err != nil {
		t.Fatalf("软删除用例失败: %v", err)
	}

	members, err := svc.Members(su.ID)
	if err != nil {
		t.Fatalf("读取成员失败: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("成员数 = %d, want 3（已删除的成员也要列出来，否则用户不知道少了谁）", len(members))
	}

	byID := map[uint64]SuiteMemberView{}
	for _, m := range members {
		byID[m.CaseID] = m
	}
	if !byID[ok].Runnable {
		t.Error("正常用例应可运行")
	}
	if byID[disabled].Runnable || byID[disabled].SkipReason == "" {
		t.Errorf("已禁用用例应标为不可运行并给出原因: %+v", byID[disabled])
	}
	if byID[deleted].Runnable || byID[deleted].SkipReason == "" {
		t.Errorf("已删除用例应标为不可运行并给出原因: %+v", byID[deleted])
	}
}

// ---------------------------------------------------------------------------
// 列表与删除
// ---------------------------------------------------------------------------

func Test列表带成员数(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	s1 := mustSuite(t, d, p.ID, "s1")
	s2 := mustSuite(t, d, p.ID, "s2")
	c1 := mustCaseOnly(t, d, p.ID, "c1")
	c2 := mustCaseOnly(t, d, p.ID, "c2")

	svc := New(d).Suite
	if _, err := svc.SetMembers(s1.ID, []uint64{c1, c2}); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}

	list, total, err := svc.List(p.ID, SuiteListQuery{}, Page{})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	counts := map[uint64]int{}
	for _, v := range list {
		counts[v.ID] = v.CaseCount
	}
	if counts[s1.ID] != 2 {
		t.Errorf("s1 成员数 = %d, want 2", counts[s1.ID])
	}
	if counts[s2.ID] != 0 {
		t.Errorf("s2 成员数 = %d, want 0", counts[s2.ID])
	}

	// 关键字过滤
	if _, total, _ := svc.List(p.ID, SuiteListQuery{Keyword: "s1"}, Page{}); total != 1 {
		t.Errorf("按 s1 过滤 total = %d, want 1", total)
	}
}

func Test被计划引用时拒绝删除用例集(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "used")

	plan := &model.TestPlan{ProjectID: p.ID, Name: "计划", TriggerType: model.TriggerManual}
	if err := d.DB.Create(plan).Error; err != nil {
		t.Fatalf("创建计划失败: %v", err)
	}
	if err := d.DB.Create(&model.PlanSuite{PlanID: plan.ID, SuiteID: su.ID, Seq: 1}).Error; err != nil {
		t.Fatalf("关联计划与用例集失败: %v", err)
	}

	err := New(d).Suite.Delete(su.ID)
	if err == nil {
		t.Fatal("被计划引用的用例集不应被删除")
	}
	if codeOfErr(t, err) != 40003 {
		t.Errorf("错误码 = %d, want 40003", codeOfErr(t, err))
	}

	// 解除引用后可以删，且成员一并清掉
	c1 := mustCaseOnly(t, d, p.ID, "c1")
	if _, err := New(d).Suite.SetMembers(su.ID, []uint64{c1}); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}
	if err := d.DB.Where("plan_id = ?", plan.ID).Delete(&model.PlanSuite{}).Error; err != nil {
		t.Fatalf("解除引用失败: %v", err)
	}
	if err := New(d).Suite.Delete(su.ID); err != nil {
		t.Fatalf("解除引用后应能删除: %v", err)
	}
	var left int64
	d.DB.Unscoped().Model(&model.SuiteCase{}).Where("suite_id = ?", su.ID).Count(&left)
	if left != 0 {
		t.Errorf("删除用例集后仍残留 %d 条成员", left)
	}
}

func Test用例集不存在时返回40002(t *testing.T) {
	d := testDeps(t)
	svc := New(d).Suite
	if _, err := svc.Get(9999); codeOfErr(t, err) != 40002 {
		t.Errorf("Get 错误码 = %d, want 40002", codeOfErr(t, err))
	}
	if _, err := svc.Members(9999); codeOfErr(t, err) != 40002 {
		t.Errorf("Members 错误码 = %d, want 40002", codeOfErr(t, err))
	}
	if err := svc.Delete(9999); codeOfErr(t, err) != 40002 {
		t.Errorf("Delete 错误码 = %d, want 40002", codeOfErr(t, err))
	}
}

// 迁移后唯一索引必须真的存在：这是"重复加入被拒"的最后一道闸门，
// 应用层校验可以被绕过，索引不能。
func Test成员唯一索引真实存在(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "idx")
	c1 := mustCaseOnly(t, d, p.ID, "c1")

	if err := d.DB.Create(&model.SuiteCase{SuiteID: su.ID, CaseID: c1, Seq: 1}).Error; err != nil {
		t.Fatalf("写入成员失败: %v", err)
	}
	err := d.DB.Create(&model.SuiteCase{SuiteID: su.ID, CaseID: c1, Seq: 2}).Error
	if err == nil {
		t.Fatal("第二条相同成员应被唯一索引拒绝")
	}
	if !isDuplicated(err) {
		t.Errorf("错误应被识别为唯一约束冲突: %v", err)
	}
}

// 布尔零值必须能落库（沿用全库约定，见 service_test.go）。
// 新增的 TestPlan 运行态字段都是指针/空串，这里确认 Enabled=false 不会被
// 数据库的默认值偷偷改成 true。
func Test计划启用状态零值能落库(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	plan := &model.TestPlan{ProjectID: p.ID, Name: "未启用计划", Enabled: false}
	if err := d.DB.Create(plan).Error; err != nil {
		t.Fatalf("创建计划失败: %v", err)
	}
	var got model.TestPlan
	if err := d.DB.First(&got, plan.ID).Error; err != nil {
		t.Fatalf("读回计划失败: %v", err)
	}
	if got.Enabled {
		t.Error("Enabled=false 被改成了 true —— 布尔列带了默认值，违反本库约定")
	}
	if got.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone = %q, want Asia/Shanghai", got.Timezone)
	}
	_ = gorm.ErrRecordNotFound
}
