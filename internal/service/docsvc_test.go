package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func TestDocBuild_分组覆盖与关联用例(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)

	userAPI := mustAPI(t, d, p.ID, APIReq{
		Code: "get_user", Name: "查用户", Module: "user", Method: "GET",
		Path: "$base_url/users/{id}", BodyType: "none",
		ResponseBody: jsonx.Any{Val: map[string]any{"id": 1}}, ResponseType: "json",
	})
	mustAPI(t, d, p.ID, APIReq{Code: "orphan", Name: "无模块", Method: "GET", Path: "/x", BodyType: "none"})

	// 一个用例引用第一个接口
	_, err := New(d).Case.Create(p.ID, CaseReq{
		Code: "c_doc", Name: "文档用例",
		Steps: []StepReq{{Seq: 1, StepType: model.StepAPI, RefAPIID: userAPI.ID}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := New(d).Doc.Build(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.TotalAPIs != 2 || doc.CoveredAPIs != 1 {
		t.Fatalf("覆盖 = %d/%d", doc.CoveredAPIs, doc.TotalAPIs)
	}
	if len(doc.Groups) != 2 {
		t.Fatalf("分组数 = %d", len(doc.Groups))
	}
	if doc.Groups[0].Module != "user" || doc.Groups[1].Module != docModuleUnclassified {
		t.Fatalf("分组顺序 = %q/%q（未分类应在最后）", doc.Groups[0].Module, doc.Groups[1].Module)
	}
	api := doc.Groups[0].APIs[0]
	if len(api.Cases) != 1 || api.Cases[0].Code != "c_doc" {
		t.Fatalf("关联用例 = %+v", api.Cases)
	}
	if !strings.Contains(api.ResponseText, "\"id\": 1") {
		t.Fatalf("响应示例未进文档: %q", api.ResponseText)
	}
}

func TestDocHTML_转义用户内容(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	mustAPI(t, d, p.ID, APIReq{
		Code: "xss", Name: "<script>alert(1)</script>", Module: "m",
		Method: "GET", Path: "/x", BodyType: "none",
		Description: "<img src=x onerror=alert(2)>",
	})

	page, err := New(d).Doc.HTML(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if strings.Contains(html, "<script>alert(1)</script>") || strings.Contains(html, "<img src=x") {
		t.Fatal("用户内容未被转义")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("应出现转义后的 script 文本")
	}
	if !strings.Contains(html, "只读视图") {
		t.Fatal("页面缺少只读标识")
	}
}

func TestShare_生命周期(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Share

	issued, err := svc.Issue(p.ID, CreateShareReq{Name: "给前端", ExpiresInDays: 7}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.Token, shareTokenPrefix) || len(issued.Token) < 40 {
		t.Fatalf("token 形态异常: %q", issued.Token)
	}
	if issued.Path != "/open/docs/"+issued.Token {
		t.Fatalf("path = %q", issued.Path)
	}

	// 有效
	link, proj, err := svc.Verify(issued.Token)
	if err != nil || link == nil || proj.ID != p.ID {
		t.Fatalf("Verify = %v, %v", link, err)
	}

	// 列表：状态 active
	list, err := svc.List(p.ID)
	if err != nil || len(list) != 1 || list[0].Status != "active" {
		t.Fatalf("列表 = %+v, %v", list, err)
	}

	// 过期：直接把 expires_at 改到过去
	past := time.Now().Add(-time.Hour)
	if err := d.DB.Model(&model.ShareLink{}).Where("id = ?", link.ID).
		Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Verify(issued.Token); !errors.Is(err, ErrShareExpired) {
		t.Fatalf("过期应返回 ErrShareExpired，得到 %v", err)
	}

	// 吊销后：不存在
	if err := svc.Revoke(p.ID, issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Verify(issued.Token); err == nil || errors.Is(err, ErrShareExpired) {
		t.Fatalf("吊销后应为不存在，得到 %v", err)
	}
}

func TestShare_永久链接与非法参数(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).Share

	issued, err := svc.Issue(p.ID, CreateShareReq{ExpiresInDays: 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ExpiresAt != nil {
		t.Fatal("0 天应为永久（expires_at 为空）")
	}
	if _, err := svc.Issue(p.ID, CreateShareReq{ExpiresInDays: -1}, 1); err == nil {
		t.Fatal("负数天数应被拒绝")
	}
	if _, _, err := svc.Verify("shr_nonexistent"); err == nil {
		t.Fatal("未知 token 应报错")
	}
}
