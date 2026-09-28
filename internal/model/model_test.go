package model

import (
	"strconv"
	"strings"
	"testing"
)

// TestAttributionReasonWith_断言条数三态 锁住 BUG-004 的修复。
//
// 背景：用例「一条断言都没写」时引擎照样正常退出（exit=0），归因是 pass，
// 但文案原来无条件写「全部断言通过」—— 用户会读成"验证过了"，
// 而平台的静态校验对同一条用例给的是 NO_VALIDATE 警告
//（「没有断言的步骤只能验证『请求发得出去』」）。两处口径必须一致。
//
// 这里锁三件事：
//
//	1. 未知条数（旧调用路径）退回通用文案，不能因为拿不到条数就给出空字符串；
//	2. 0 条断言**绝不能**出现「全部断言通过」字样；
//	3. 有条数时给出具体数字，比笼统的"全部断言通过"更可核对。
func TestAttributionReasonWith_断言条数三态(t *testing.T) {
	// —— 1. 未知：旧签名必须继续可用（parser 包的归因全覆盖测试就是走这条）——
	if got := AttributionReason(AttrPass); got == "" {
		t.Error("AttributionReason 在断言条数未知时也不该返回空文案")
	}
	if got := AttributionReasonWith(AttrPass, AssertTotalUnknown); !strings.Contains(got, "全部断言通过") {
		t.Errorf("条数未知时应退回通用文案，实际 = %q", got)
	}

	// —— 2. 0 条：这是本 bug 的核心，必须换口径 ——
	zero := AttributionReasonWith(AttrPass, 0)
	if zero == "" {
		t.Fatal("0 条断言的文案不能为空")
	}
	if strings.Contains(zero, "全部断言通过") {
		t.Errorf("0 条断言绝不能写「全部断言通过」（与 NO_VALIDATE 警告矛盾），实际 = %q", zero)
	}
	for _, want := range []string{"没有声明任何断言", "请求发得出去", "结果对不对"} {
		if !strings.Contains(zero, want) {
			t.Errorf("0 条断言的文案应含 %q（与静态校验同一口径），实际 = %q", want, zero)
		}
	}

	// —— 3. 有条数：给出可核对的数字 ——
	for _, n := range []int{1, 2, 7} {
		got := AttributionReasonWith(AttrPass, n)
		want := "全部 " + strconv.Itoa(n) + " 条断言通过"
		if !strings.Contains(got, want) {
			t.Errorf("条数 %d 的文案应含 %q，实际 = %q", n, want, got)
		}
	}

	// —— 4. 非 pass 归因与断言条数无关：条数不该改变失败原因的表述 ——
	for _, attr := range []string{
		AttrSystemUnderTest, AttrEnvironment, AttrCaseIssue, AttrOps, AttrTimeout, AttrCanceled, AttrUnknown,
	} {
		base := AttributionReasonWith(attr, AssertTotalUnknown)
		for _, n := range []int{0, 3} {
			if got := AttributionReasonWith(attr, n); got != base {
				t.Errorf("归因 %q 的文案不该随断言条数（%d）变化：\n got = %q\nwant = %q", attr, n, got, base)
			}
		}
	}
}

// TestAttributionReason_每个归因都有文案 保证新增归因枚举时不会漏掉文案。
func TestAttributionReason_每个归因都有文案(t *testing.T) {
	all := []string{
		AttrPass, AttrSystemUnderTest, AttrEnvironment,
		AttrCaseIssue, AttrOps, AttrTimeout, AttrCanceled, AttrUnknown,
	}
	for _, a := range all {
		if AttributionReason(a) == "" {
			t.Errorf("归因 %q 缺少判断依据说明", a)
		}
		if AttributionLabel(a) == "" {
			t.Errorf("归因 %q 缺少中文短标签", a)
		}
	}
}

// TestAssertTotalUnknown_哨兵是负数 防止有人把它改成 0。
//
// 0 是**有意义**的取值（该用例确实没写断言），哨兵一旦变成 0，
// 「没查过」和「确认 0 条」就会被混为一谈 —— 那正是 BUG-004 的成因。
func TestAssertTotalUnknown_哨兵是负数(t *testing.T) {
	if AssertTotalUnknown >= 0 {
		t.Fatalf("AssertTotalUnknown 必须是负数哨兵，当前 = %d", AssertTotalUnknown)
	}
	if AttributionReasonWith(AttrPass, AssertTotalUnknown) == AttributionReasonWith(AttrPass, 0) {
		t.Error("「未知条数」与「确认 0 条」的文案必须不同")
	}
}
