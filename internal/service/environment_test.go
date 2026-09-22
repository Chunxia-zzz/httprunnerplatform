package service

import (
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

func envReq(name string, environs map[string]any) EnvReq {
	return EnvReq{
		Name:          name,
		BaseURL:       "http://127.0.0.1:8899",
		Environs:      jsonx.Map(environs),
		GlobalHeaders: jsonx.Map{"X-From": "hrp"},
		VerifySSL:     false,
	}
}

// TestEnvironment_非管理员读取时敏感值被掩码 掩码必须在服务端完成。
//
// 交给前端过滤等于把敏感值先放进了 HTTP 响应体，
// 任何一次抓包、每一次访问日志都会留下它。
func TestEnvironment_非管理员读取时敏感值被掩码(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Environment

	e, err := svc.Create(p.ID, envReq("local", map[string]any{
		"api_key": "super-secret",
		"token":   "t-123456",
		"plain":   "not-secret",
	}))
	if err != nil {
		t.Fatalf("创建环境失败: %v", err)
	}

	// 非管理员视角
	got, err := svc.Get(e.ID, false)
	if err != nil {
		t.Fatalf("读取环境失败: %v", err)
	}
	if got.Environs["api_key"] != MaskedValue || got.Environs["token"] != MaskedValue {
		t.Errorf("敏感值未被掩码: %+v", got.Environs)
	}
	if got.Environs["plain"] != "not-secret" {
		t.Errorf("非敏感值不该被掩码: %+v", got.Environs)
	}

	// 管理员视角拿到明文 —— 否则管理员根本没法核对配置
	admin, err := svc.Get(e.ID, true)
	if err != nil {
		t.Fatalf("读取环境失败: %v", err)
	}
	if admin.Environs["api_key"] != "super-secret" {
		t.Errorf("管理员应看到明文，实际 %v", admin.Environs["api_key"])
	}

	// 掩码只作用于返回副本，库里必须还是原文
	var raw model.Environment
	if err := d.DB.First(&raw, e.ID).Error; err != nil {
		t.Fatalf("读取原始环境失败: %v", err)
	}
	if raw.Environs["api_key"] != "super-secret" {
		t.Errorf("库里被掩码污染了: %+v", raw.Environs)
	}
}

// TestEnvironment_掩码占位符回写时保留原值 覆盖编辑器最常见的操作序列。
//
// 前端拿到的是 "***"，保存时原样回传。若服务端不做识别，
// 用户"只改了个备注"就会把 token 覆盖成三个星号，而且没有任何提示。
func TestEnvironment_掩码占位符回写时保留原值(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Environment

	e, err := svc.Create(p.ID, envReq("local", map[string]any{
		"api_key": "super-secret",
		"plain":   "keep-me",
	}))
	if err != nil {
		t.Fatalf("创建环境失败: %v", err)
	}

	// 模拟前端回写：敏感值带回占位符，同时改了名字与普通值
	req := envReq("local-renamed", map[string]any{
		"api_key": MaskedValue,
		"plain":   "changed",
	})
	if _, err := svc.Update(e.ID, req); err != nil {
		t.Fatalf("更新环境失败: %v", err)
	}

	var raw model.Environment
	if err := d.DB.First(&raw, e.ID).Error; err != nil {
		t.Fatalf("读取环境失败: %v", err)
	}
	if raw.Environs["api_key"] != "super-secret" {
		t.Errorf("占位符回写把真实值冲掉了: %v", raw.Environs["api_key"])
	}
	if raw.Environs["plain"] != "changed" {
		t.Errorf("普通值应被更新，实际 %v", raw.Environs["plain"])
	}
	if raw.Name != "local-renamed" {
		t.Errorf("名称应被更新，实际 %q", raw.Name)
	}

	// 库里出现过的占位符本身绝不该被存进去
	for k, v := range raw.Environs {
		if v == MaskedValue {
			t.Errorf("键 %q 把掩码占位符存进了库", k)
		}
	}
}

// TestEnvironment_首个环境自动成为默认 没有默认环境时执行会悄悄退回引擎内置地址。
//
// 那种失败很难被用户联想到"是环境没设默认"，因此这里直接兜住。
func TestEnvironment_首个环境自动成为默认(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Environment

	first, err := svc.Create(p.ID, envReq("local", nil))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if !first.IsDefault {
		t.Error("项目的第一个环境应自动成为默认环境")
	}

	def, err := svc.Default(p.ID)
	if err != nil {
		t.Fatalf("解析默认环境失败: %v", err)
	}
	if def == nil || def.ID != first.ID {
		t.Errorf("默认环境解析错误: %+v", def)
	}
}

// TestEnvironment_is_default互斥 同一项目只能有一个默认环境。
func TestEnvironment_is_default互斥(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Environment

	a, err := svc.Create(p.ID, envReq("local", nil))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	b, err := svc.Create(p.ID, envReq("staging", nil))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if b.IsDefault {
		t.Error("第二个环境不该自动成为默认")
	}

	// 把 b 设为默认，a 必须被摘掉
	req := envReq("staging", nil)
	req.IsDefault = true
	if _, err := svc.Update(b.ID, req); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	list, err := svc.List(p.ID, true)
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	defaults := 0
	for _, e := range list {
		if e.IsDefault {
			defaults++
			if e.ID != b.ID {
				t.Errorf("默认环境应是 staging(%d)，实际 %d", b.ID, e.ID)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("默认环境数量 = %d, want 1", defaults)
	}
	_ = a

	// 把唯一的默认环境取消掉 ⇒ 服务端要自动补一个，不能出现"没有默认环境"
	req = envReq("staging", nil)
	req.IsDefault = false
	if _, err := svc.Update(b.ID, req); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	def, err := svc.Default(p.ID)
	if err != nil {
		t.Fatalf("解析默认环境失败: %v", err)
	}
	if def == nil {
		t.Fatal("项目有环境却没有任何默认环境 —— 执行时会静默退回引擎内置地址")
	}
}

// TestEnvironment_重名与引用检查 覆盖两个约束。
func TestEnvironment_重名与引用检查(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Environment

	e, err := svc.Create(p.ID, envReq("local", nil))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	_, err = svc.Create(p.ID, envReq("local", nil))
	wantCode(t, err, response.CodeConflict)

	// 被测试计划引用时不可删：计划是无人值守执行的，悬空 env_id 到那时才发现就太晚了
	plan := model.TestPlan{ProjectID: p.ID, Name: " nightly ", EnvID: e.ID}
	if err := d.DB.Create(&plan).Error; err != nil {
		t.Fatalf("创建测试计划失败: %v", err)
	}
	wantCode(t, svc.Delete(e.ID), response.CodeInUse)
}

// TestEnvironment_没有环境时也能执行 返回 nil 而不是报错。
//
// 让用户在建环境之前就能先把用例发出去调试，是"本地开箱可用"的关键一步。
func TestEnvironment_没有环境时也能执行(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	def, err := New(d).Environment.Default(p.ID)
	if err != nil {
		t.Fatalf("没有环境时不该报错: %v", err)
	}
	if def != nil {
		t.Errorf("没有环境时应返回 nil，实际 %+v", def)
	}
}
