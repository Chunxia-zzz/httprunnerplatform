package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
)

// 本文件覆盖 CI 令牌的两条核心规则：
//
//  1. **只存哈希**（决策 4）：库里绝不能出现明文；
//  2. **按项目签发**（决策 1）：Verify 出来的令牌带着项目归属，
//     由 API 层强制比对请求里的 project_id。
//
// 项目比对本身在 internal/api 的 open 测试里测（那里才有 HTTP 请求）。

func Test令牌签发只在这一次给出明文(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	issued, err := svc.Issue(p.ID, CreateTokenReq{Name: "流水线"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if !strings.HasPrefix(issued.Token, "hrp_ci_") {
		t.Errorf("明文 = %q, 应以 hrp_ci_ 开头", issued.Token)
	}
	if len(issued.Token) != len("hrp_ci_")+64 {
		t.Errorf("明文长度 = %d, want %d（32 字节随机数的 hex）",
			len(issued.Token), len("hrp_ci_")+64)
	}

	// ⭐ 列表里绝不能再出现明文
	list, err := svc.List(p.ID)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("列表 = %d 条, want 1", len(list))
	}
	if strings.Contains(list[0].Prefix, issued.Token) {
		t.Error("列表里出现了明文（prefix 不该包含明文）")
	}
	if list[0].Prefix == "" {
		t.Error("prefix 为空，用户将无法在列表里认出这枚令牌")
	}

	// 库里也不该有明文
	var raw string
	if err := d.DB.Model(&model.APIToken{}).Where("id = ?", issued.ID).
		Limit(1).Pluck("token", &raw).Error; err != nil {
		t.Fatalf("读库失败: %v", err)
	}
	if raw == issued.Token {
		t.Fatal("库里存的是明文 —— 令牌泄漏一次就等于永久泄漏")
	}
	if raw == "" {
		t.Error("库里的哈希为空")
	}
}

func Test令牌校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	issued, err := svc.Issue(p.ID, CreateTokenReq{Name: "流水线"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	tk, err := svc.Verify(issued.Token)
	if err != nil {
		t.Fatalf("正确的明文应当校验通过: %v", err)
	}
	if tk.ProjectID != p.ID {
		t.Errorf("token.ProjectID = %d, want %d", tk.ProjectID, p.ID)
	}
	if tk.LastUsedAt == nil {
		t.Error("校验通过应回写 LastUsedAt（它是判断令牌还活着的唯一依据）")
	}

	// 空 / 错 / 改一个字符都得拒绝
	for _, bad := range []string{"", "   ", "hrp_ci_xxx", issued.Token + "x",
		strings.TrimSuffix(issued.Token, issued.Token[len(issued.Token)-1:])} {
		if _, err := svc.Verify(bad); err == nil {
			t.Errorf("明文 %q 不该校验通过", bad)
		} else if codeOfErr(t, err) != 40100 {
			t.Errorf("明文 %q 的错误码 = %d, want 40100", bad, codeOfErr(t, err))
		}
	}
}

func Test令牌两枚明文互不相同(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	a, err := svc.Issue(p.ID, CreateTokenReq{Name: "甲"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	b, err := svc.Issue(p.ID, CreateTokenReq{Name: "乙"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if a.Token == b.Token {
		t.Fatal("两枚令牌的明文相同")
	}
	if a.Prefix == b.Prefix {
		t.Error("两枚令牌的 prefix 相同，列表里无法区分")
	}
	// 交叉校验：甲的令牌不能当作乙的用
	if _, err := svc.Verify(b.Token); err != nil {
		t.Errorf("乙的明文应能通过: %v", err)
	}
}

func Test令牌过期后不能用(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	// 有效期 -1 天是非法的；用 1 天再把库里的过期时间改到过去
	issued, err := svc.Issue(p.ID, CreateTokenReq{Name: "会过期", TTLDays: 1})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if _, err := svc.Verify(issued.Token); err != nil {
		t.Fatalf("还没过期应当能用: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if err := d.DB.Model(&model.APIToken{}).Where("id = ?", issued.ID).
		Update("expire_at", &past).Error; err != nil {
		t.Fatalf("改过期时间失败: %v", err)
	}
	if _, err := svc.Verify(issued.Token); err == nil {
		t.Error("已过期的令牌不该通过")
	} else if codeOfErr(t, err) != 40100 {
		t.Errorf("错误码 = %d, want 40100", codeOfErr(t, err))
	}

	// 列表里要能直接看出来它过期了
	list, err := svc.List(p.ID)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if !list[0].Expired {
		t.Error("列表里 expired = false, want true")
	}
}

func Test令牌吊销后立刻失效(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	issued, err := svc.Issue(p.ID, CreateTokenReq{Name: "会吊销"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if err := svc.Revoke(p.ID, issued.ID); err != nil {
		t.Fatalf("吊销失败: %v", err)
	}
	// 校验走数据库查询，没有缓存 ⇒ 吊销即刻生效，不存在"还能用一会儿"的窗口
	if _, err := svc.Verify(issued.Token); err == nil {
		t.Error("吊销后的令牌不该通过")
	}
	// 再次吊销 / 吊销不存在的令牌 → 40002
	if err := svc.Revoke(p.ID, issued.ID); codeOfErr(t, err) != 40002 {
		t.Errorf("重复吊销的错误码 = %d, want 40002", codeOfErr(t, err))
	}
	if err := svc.Revoke(p.ID, 9999); codeOfErr(t, err) != 40002 {
		t.Errorf("吊销不存在的错误码 = %d, want 40002", codeOfErr(t, err))
	}
	// 别的项目的令牌不能被这个项目吊销
	p2 := seedProject(t, d)
	other, err := svc.Issue(p2.ID, CreateTokenReq{Name: "别人的"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if err := svc.Revoke(p.ID, other.ID); codeOfErr(t, err) != 40002 {
		t.Errorf("跨项目吊销的错误码 = %d, want 40002", codeOfErr(t, err))
	}
}

func Test令牌名称与权限校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	if _, err := svc.Issue(p.ID, CreateTokenReq{}); codeOfErr(t, err) != 40000 {
		t.Errorf("空名称的错误码 = %d, want 40000", codeOfErr(t, err))
	}
	if _, err := svc.Issue(p.ID, CreateTokenReq{Name: "x", Scope: "run:admin"}); codeOfErr(t, err) != 40000 {
		t.Errorf("未知权限的错误码 = %d, want 40000", codeOfErr(t, err))
	}
	if _, err := svc.Issue(p.ID, CreateTokenReq{Name: "x", TTLDays: -1}); codeOfErr(t, err) != 40000 {
		t.Errorf("负有效期的错误码 = %d, want 40000", codeOfErr(t, err))
	}

	// 默认权限是"触发 + 读结果"
	issued, err := svc.Issue(p.ID, CreateTokenReq{Name: "默认权限"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if issued.Scope != DefaultTokenScope {
		t.Errorf("scope = %q, want %q", issued.Scope, DefaultTokenScope)
	}

	if _, err := svc.Issue(p.ID, CreateTokenReq{Name: "默认权限"}); codeOfErr(t, err) != 40001 {
		t.Errorf("重名的错误码 = %d, want 40001", codeOfErr(t, err))
	}
	// 不同项目下允许同名
	p2 := seedProject(t, d)
	if _, err := svc.Issue(p2.ID, CreateTokenReq{Name: "默认权限"}); err != nil {
		t.Errorf("不同项目下允许同名: %v", err)
	}
}

func Test令牌权限判断(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Token

	full, err := svc.Issue(p.ID, CreateTokenReq{Name: "全能"})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	readOnly, err := svc.Issue(p.ID, CreateTokenReq{Name: "只读", Scope: ScopeRunRead})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	tkFull, err := svc.Verify(full.Token)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	tkRead, err := svc.Verify(readOnly.Token)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}

	if !svc.HasScope(tkFull, ScopeRunTrigger) || !svc.HasScope(tkFull, ScopeRunRead) {
		t.Error("默认权限应同时具备触发与读结果")
	}
	if svc.HasScope(tkRead, ScopeRunTrigger) {
		t.Error("只读令牌不该有触发权限")
	}
	if !svc.HasScope(tkRead, ScopeRunRead) {
		t.Error("只读令牌应有读结果权限")
	}
	// 冗余的空格与空段不该影响判断
	tkRead.Scope = " run:read ,, "
	if !svc.HasScope(tkRead, ScopeRunRead) {
		t.Error("权限解析应容忍空格与空段")
	}
}
