package service

import (
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

func TestCanAccess_三种状态(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Member

	// 1) 无成员：对所有人开放（存量兼容）
	if ok, err := svc.CanAccess(p.ID, 42, false); err != nil || !ok {
		t.Fatalf("无成员项目应开放: %v %v", ok, err)
	}
	// 2) 管理员始终可访问
	if ok, _ := svc.CanAccess(p.ID, 1, true); !ok {
		t.Fatal("管理员应可访问")
	}

	// 建两个用户：一个成员，一个非成员
	u1 := seedUser(t, d, "member_a", model.RoleMember, true)
	u2 := seedUser(t, d, "member_b", model.RoleMember, true)
	if _, err := svc.Add(p.ID, AddMemberReq{Username: u1.Username}); err != nil {
		t.Fatal(err)
	}

	// 3) 有成员后收敛：成员可访问，非成员不可
	if ok, _ := svc.CanAccess(p.ID, u1.ID, false); !ok {
		t.Fatal("成员应可访问")
	}
	if ok, _ := svc.CanAccess(p.ID, u2.ID, false); ok {
		t.Fatal("非成员不应可访问")
	}
}

func TestMember_增删与约束(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Member
	// 先建两个账号：seedProject 的 owner_id 固定为 1，第一个用户可能恰好是 1，
	// 因此用第二个账号做「普通成员移除」。
	_ = seedUser(t, d, "member_owner_slot", model.RoleMember, true)
	u := seedUser(t, d, "member_c", model.RoleMember, true)

	m, err := svc.Add(p.ID, AddMemberReq{Username: u.Username, Role: model.MemberRoleMember})
	if err != nil || m.UserID != u.ID {
		t.Fatalf("添加成员 = %+v, %v", m, err)
	}
	// 重复添加
	if _, err := svc.Add(p.ID, AddMemberReq{Username: u.Username}); err == nil {
		t.Fatal("重复添加应拒绝")
	} else {
		wantCode(t, err, response.CodeConflict)
	}
	// 不存在的用户
	if _, err := svc.Add(p.ID, AddMemberReq{Username: "no_such_user"}); err == nil {
		t.Fatal("未知用户应拒绝")
	} else {
		wantCode(t, err, response.CodeNotFound)
	}

	list, err := svc.List(p.ID)
	// 列表包含「隐式所有者 + 显式成员」：所有者从未进过成员表也必须出现在名单里
	if err != nil || len(list) != 2 {
		t.Fatalf("成员列表 = %+v, %v", list, err)
	}
	found := false
	for _, m := range list {
		if m.Username == u.Username && m.Role == model.MemberRoleMember {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表中缺少刚加入的成员: %+v", list)
	}

	// 所有者不能被移除
	if err := svc.Remove(p.ID, p.OwnerID); err == nil {
		t.Fatal("移除所有者应被拒绝")
	}
	// 正常移除
	if err := svc.Remove(p.ID, u.ID); err != nil {
		t.Fatalf("移除成员失败: %v", err)
	}
	// 再移除 → 不存在
	if err := svc.Remove(p.ID, u.ID); err == nil {
		t.Fatal("移除不存在的成员应报错")
	}
	// ⭐ 移除最后一个成员后，项目仍保持「仅成员可见」：
	// 靠成员数判断会在这里悄悄把私有项目变回全员可见（数据泄漏）。
	if ok, err := svc.CanAccess(p.ID, u.ID, false); err != nil || ok {
		t.Fatalf("移除最后一个成员后不应自动开放: %v %v", ok, err)
	}
}

func TestProjectList_按成员可见性过滤(t *testing.T) {
	d := testDeps(t)
	p1 := seedProject(t, d)
	_ = p1
	p2 := seedProject(t, d)
	u := seedUser(t, d, "member_d", model.RoleMember, true)

	// 两个项目都无成员：普通用户都可见
	list, total, err := New(d).Project.List(ProjectListQuery{}, Page{}, u.ID, false)
	if err != nil || total != 2 {
		t.Fatalf("无成员项目应全部可见: %d, %v", total, err)
	}
	_ = list

	// 给 p2 加成员：普通用户只能看到 p1（无成员）+ p2（本人是成员）
	if _, err := New(d).Member.Add(p2.ID, AddMemberReq{Username: u.Username}); err != nil {
		t.Fatal(err)
	}
	_, total, err = New(d).Project.List(ProjectListQuery{}, Page{}, u.ID, false)
	if err != nil || total != 2 {
		t.Fatalf("成员项目 + 无成员项目应都可见: %d, %v", total, err)
	}

	// 另一个非成员用户：p2 不可见（p2 有成员表记录，但不是他）
	other := seedUser(t, d, "member_e", model.RoleMember, true)
	_, total, err = New(d).Project.List(ProjectListQuery{}, Page{}, other.ID, false)
	if err != nil || total != 1 {
		t.Fatalf("非成员看不到 p2: %d, %v", total, err)
	}

	// 管理员看全部
	if _, total, _ := New(d).Project.List(ProjectListQuery{}, Page{}, 1, true); total != 2 {
		t.Fatalf("管理员应看全部: %d", total)
	}
}

func TestAudit_写入与筛选(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Audit

	svc.Log(ChangeEntry{ProjectID: p.ID, UserID: 1, Username: "admin",
		Action: ChangeCreate, Entity: EntityCase, EntityID: 1, EntityKey: "c1", Summary: "新建用例「A」"})
	svc.Log(ChangeEntry{ProjectID: p.ID, UserID: 1, Username: "admin",
		Action: ChangeUpdate, Entity: EntityAPI, EntityID: 2, EntityKey: "a1", Summary: "修改接口「B」"})

	list, total, err := svc.List(p.ID, "", "", Page{})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("变更列表 = %d/%d, %v", len(list), total, err)
	}
	// 倒序：最新在前
	if list[0].Entity != EntityAPI {
		t.Fatalf("应为倒序，首条 = %s", list[0].Entity)
	}
	// 按实体筛选
	_, total, _ = svc.List(p.ID, EntityCase, "", Page{})
	if total != 1 {
		t.Fatalf("按实体筛选 = %d", total)
	}
	// 按动作筛选
	_, total, _ = svc.List(p.ID, "", ChangeUpdate, Page{})
	if total != 1 {
		t.Fatalf("按动作筛选 = %d", total)
	}
	// 项目不存在
	if _, _, err := svc.List(99999, "", "", Page{}); err == nil {
		t.Fatal("未知项目应报错")
	}
}
