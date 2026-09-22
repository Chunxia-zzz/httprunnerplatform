package service

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// 本文件覆盖用例集执行里两处"最不能出错"的判断：
//
//  1. decideSuite —— 一批用例跑完之后到底显示成绿还是红。
//     它是纯函数，可以脱离 hrp 子进程被测，这也是它被抽出来的唯一理由。
//  2. suiteRunMembers —— 谁能跑、谁该被跳过。
//     分错类会直接污染对账结果。
//
// 真正"跑起来"的那条路径依赖本机 hrp 二进制，不在这里测。

// ---------------------------------------------------------------------------
// decideSuite
// ---------------------------------------------------------------------------

// so 造一条用例结果。started 表示引擎是否真的启动了这条用例。
func so(status, code string, started bool) suiteOutcome {
	return suiteOutcome{Status: status, CaseCode: code, Started: started}
}

func soAttr(status, code, attr string, started bool) suiteOutcome {
	o := so(status, code, started)
	o.Attribution = attr
	return o
}

func Test用例集裁决_全部通过才是成功(t *testing.T) {
	cases := []struct {
		name     string
		outcomes []suiteOutcome
		expected int
		want     string
	}{
		{
			name: "空也要落 success：expected=0 且一条都没跑，不算不一致",
			// 实际不会出现（空用例集在启动阶段就被拒了），
			// 但纯函数不该对空输入崩掉或给出奇怪的终态。
			expected: 0,
			want:     model.RunSuccess,
		},
		{
			name:     "全通过→success",
			outcomes: []suiteOutcome{so(model.StatusPass, "a", true), so(model.StatusPass, "b", true)},
			expected: 2,
			want:     model.RunSuccess,
		},
		{
			name:     "有一条 fail→failed",
			outcomes: []suiteOutcome{so(model.StatusPass, "a", true), so(model.StatusFail, "b", true)},
			expected: 2,
			want:     model.RunFailed,
		},
		{
			name:     "error 优先于 fail",
			outcomes: []suiteOutcome{so(model.StatusFail, "a", true), so(model.StatusError, "b", true)},
			expected: 2,
			want:     model.RunError,
		},
		{
			name:     "canceled 优先于 error",
			outcomes: []suiteOutcome{soAttr(model.StatusError, "a", model.AttrCanceled, true), so(model.StatusError, "b", true)},
			expected: 2,
			want:     model.RunCanceled,
		},
		{
			name:     "编译失败没启动过→error，但不该被算成已执行",
			outcomes: []suiteOutcome{so(model.StatusPass, "a", true), so(model.StatusError, "b", false)},
			expected: 1,
			want:     model.RunError,
		},
		{
			name:     "已删除成员→error，且不影响对账",
			outcomes: []suiteOutcome{so(model.StatusPass, "a", true), so(model.StatusPass, "b", true), so(model.StatusError, "(已删除)", false)},
			expected: 2,
			want:     model.RunError,
		},
		{
			name:     "已禁用成员→skipped，其余全过则整批 success",
			outcomes: []suiteOutcome{so(model.StatusPass, "a", true), so(model.StatusPass, "b", true), so(model.StatusSkipped, "c", false)},
			expected: 2,
			want:     model.RunSuccess,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideSuite(c.outcomes, c.expected)
			if got.Status != c.want {
				t.Errorf("status = %q, want %q（err=%q）", got.Status, c.want, got.ErrMsg)
			}
		})
	}
}

// ⭐ 这是最重要的一条：实测 F5 证明畸形用例被引擎静默丢弃时，
// 退出码是 0、summary 里 success 还是 true。只看退出码会给出"全绿"的假象。
func Test用例集裁决_对账不一致压过一切(t *testing.T) {
	// 三条用例全部 pass，但引擎只启动了两条（第三条被静默丢掉了）。
	outcomes := []suiteOutcome{
		so(model.StatusPass, "a", true),
		so(model.StatusPass, "b", true),
	}
	v := decideSuite(outcomes, 3)

	if v.Status != model.RunError {
		t.Fatalf("status = %q, want error：全 pass 但少跑了一条，绝不能显示成绿色", v.Status)
	}
	if !strings.Contains(v.ErrMsg, "对账不一致") {
		t.Errorf("err_msg = %q, 应说明对账不一致", v.ErrMsg)
	}
	if !strings.Contains(v.ErrMsg, "预期执行 3 条，实际执行 2 条") {
		t.Errorf("err_msg = %q, 应带上预期/实际条数", v.ErrMsg)
	}
}

func Test用例集裁决_对账正常时不提对账(t *testing.T) {
	v := decideSuite([]suiteOutcome{
		so(model.StatusFail, "a", true),
		so(model.StatusPass, "b", true),
	}, 2)

	if v.Status != model.RunFailed {
		t.Fatalf("status = %q, want failed", v.Status)
	}
	if strings.Contains(v.ErrMsg, "对账不一致") {
		t.Errorf("err_msg = %q, 对账正常时不该提对账", v.ErrMsg)
	}
	if !strings.Contains(v.ErrMsg, "a") {
		t.Errorf("err_msg = %q, 应列出未通过的用例标识", v.ErrMsg)
	}
}

// abort 与取消都是"我们主动不跑后面的用例"，不是"用例被引擎弄丢了"。
// 这两种情形下 expected 由调用方收缩到已尝试条数，裁决不该再报对账不一致 ——
// 否则每一次 abort 都会多一句"对账不一致"，把真正的信号（F5 静默丢弃）淹掉。
func Test用例集裁决_主动中止不报对账不一致(t *testing.T) {
	t.Run("abort：第一条就失败，expected 收缩到 1", func(t *testing.T) {
		v := decideSuite([]suiteOutcome{so(model.StatusFail, "a", true)}, 1)
		if v.Status != model.RunFailed {
			t.Errorf("status = %q, want failed", v.Status)
		}
		if strings.Contains(v.ErrMsg, "对账不一致") {
			t.Errorf("err_msg = %q, abort 之后不该报对账不一致", v.ErrMsg)
		}
	})

	t.Run("取消：不再叠加对账结论", func(t *testing.T) {
		v := decideSuite([]suiteOutcome{
			soAttr(model.StatusPass, "a", model.AttrPass, true),
			soAttr(model.StatusError, "b", model.AttrCanceled, false),
		}, 3)
		if v.Status != model.RunCanceled {
			t.Fatalf("status = %q, want canceled", v.Status)
		}
		if strings.Contains(v.ErrMsg, "对账不一致") {
			t.Errorf("err_msg = %q, 取消之后不该再报对账不一致", v.ErrMsg)
		}
	})
}

func Test用例集裁决_失败用例全部列出(t *testing.T) {
	v := decideSuite([]suiteOutcome{
		so(model.StatusPass, "ok1", true),
		so(model.StatusFail, "bad1", true),
		so(model.StatusError, "bad2", true),
	}, 3)

	for _, want := range []string{"bad1", "bad2"} {
		if !strings.Contains(v.ErrMsg, want) {
			t.Errorf("err_msg = %q, 应包含 %q", v.ErrMsg, want)
		}
	}
	if strings.Contains(v.ErrMsg, "ok1") {
		t.Errorf("err_msg = %q, 不该把通过的用例列进失败清单", v.ErrMsg)
	}
}

// ---------------------------------------------------------------------------
// suiteRunMembers
// ---------------------------------------------------------------------------

func Test执行成员快照_已删除与已禁用分开归类(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "mem")

	idAlive := mustCaseOnly(t, d, p.ID, "alive")
	idDisabled := mustCaseOnly(t, d, p.ID, "disabled")
	idDeleted := mustCaseOnly(t, d, p.ID, "deleted")

	// ⭐ 顺序很重要：先全部加入成员，之后才去禁用/删除。
	// 反过来（先删再加）会被 SetMembers 的"用例必须存在"规则拒绝 —— 那是另一条规则。
	mustSetMembers(t, d, su.ID, []uint64{idAlive, idDisabled, idDeleted})

	if err := d.DB.Model(&model.TestCase{}).Where("id = ?", idDisabled).
		Update("status", model.CaseStatusDisabled).Error; err != nil {
		t.Fatalf("禁用例失败: %v", err)
	}
	if err := d.DB.Delete(&model.TestCase{}, idDeleted).Error; err != nil {
		t.Fatalf("删除用例失败: %v", err)
	}

	svc := NewRunService(d)
	members, broken, err := svc.suiteRunMembers(su.ID)
	if err != nil {
		t.Fatalf("suiteRunMembers 失败: %v", err)
	}
	if len(members) != 1 || members[0].ID != idAlive {
		t.Fatalf("可跑成员 = %d 条, want 只剩 alive 一条", len(members))
	}
	if len(broken) != 2 {
		t.Fatalf("跑不了的成员 = %d 条, want 2", len(broken))
	}

	var skippedCount int
	for _, b := range broken {
		if b.Skipped {
			skippedCount++
			if b.CaseCode != "disabled" {
				t.Errorf("被禁用的成员标识 = %q, want disabled", b.CaseCode)
			}
		} else {
			if b.CaseCode != "(已删除)" {
				t.Errorf("已删除成员的标识 = %q, want (已删除)", b.CaseCode)
			}
		}
	}
	if skippedCount != 1 {
		t.Errorf("skipped 成员 = %d, want 1（只有禁用算 skipped，删除算 error）", skippedCount)
	}
}

func Test执行成员快照_顺序即执行顺序(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "order")

	idA := mustCaseOnly(t, d, p.ID, "z_last")
	idB := mustCaseOnly(t, d, p.ID, "a_first")
	// 故意按"z 在前"的顺序加入：成员顺序由 SetMembers 的数组顺序决定，
	// 不是由用例 ID 或标识决定。
	mustSetMembers(t, d, su.ID, []uint64{idA, idB})

	members, _, err := NewRunService(d).suiteRunMembers(su.ID)
	if err != nil {
		t.Fatalf("suiteRunMembers 失败: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("成员 = %d 条, want 2", len(members))
	}
	if members[0].Code != "z_last" || members[1].Code != "a_first" {
		t.Errorf("顺序 = [%s %s], want [z_last a_first]", members[0].Code, members[1].Code)
	}
}

// ---------------------------------------------------------------------------
// StartSuite 的入参校验
// ---------------------------------------------------------------------------

// ⚠️ 这里只测"启动之前"的校验：这些分支都在 goroutine 起之前 return，
// 不会真的去调 hrp 二进制。
func Test启动用例集执行_入参校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	p2 := seedProject(t, d)
	su := mustSuite(t, d, p.ID, "vp")

	svc := NewRunService(d)

	t.Run("项目与用例集不能为空", func(t *testing.T) {
		if _, err := svc.StartSuite(StartRunRequest{ProjectID: p.ID}, 1); codeOfErr(t, err) != 40000 {
			t.Errorf("缺 target_id 的错误码 = %d, want 40000", codeOfErr(t, err))
		}
		if _, err := svc.StartSuite(StartRunRequest{TargetID: su.ID}, 1); codeOfErr(t, err) != 40000 {
			t.Errorf("缺 project_id 的错误码 = %d, want 40000", codeOfErr(t, err))
		}
	})

	t.Run("用例集不存在", func(t *testing.T) {
		_, err := svc.StartSuite(StartRunRequest{ProjectID: p.ID, TargetID: 9999}, 1)
		if codeOfErr(t, err) != 40002 {
			t.Errorf("错误码 = %d, want 40002", codeOfErr(t, err))
		}
	})

	t.Run("用例集不属于该项目", func(t *testing.T) {
		_, err := svc.StartSuite(StartRunRequest{ProjectID: p2.ID, TargetID: su.ID}, 1)
		if codeOfErr(t, err) != 40000 {
			t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
		}
	})

	t.Run("没有成员不能执行", func(t *testing.T) {
		_, err := svc.StartSuite(StartRunRequest{ProjectID: p.ID, TargetID: su.ID}, 1)
		if codeOfErr(t, err) != 40000 {
			t.Errorf("错误码 = %d, want 40000", codeOfErr(t, err))
		}
	})
}

// mustSetMembers 设置成员，失败即终止。
func mustSetMembers(t *testing.T, d Deps, suiteID uint64, caseIDs []uint64) {
	t.Helper()
	if _, err := New(d).Suite.SetMembers(suiteID, caseIDs); err != nil {
		t.Fatalf("设置成员失败: %v", err)
	}
}
