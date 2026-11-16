package service

import (
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// mustAPI 建一个接口定义，失败即终止。
func mustAPI(t *testing.T, d Deps, projectID uint64, req APIReq) *APIView {
	t.Helper()
	v, err := New(d).API.Create(projectID, req)
	if err != nil {
		t.Fatalf("创建接口 %q 失败: %v", req.Code, err)
	}
	return v
}

func TestAPI创建与查询(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).API

	v := mustAPI(t, d, p.ID, APIReq{
		Code: "user_login", Name: "登录", Module: "user", Method: "post",
		Path: "$base_url/login", Headers: jsonx.Map{"X-Trace": "1"},
		Body: jsonx.Any{Val: map[string]any{"u": "a"}}, BodyType: "json",
	})
	// method 归一化为大写；默认 body_type 为 json；来源打 manual。
	if v.Method != "POST" || v.BodyType != "json" || v.Source != model.APISourceManual {
		t.Fatalf("归一化结果 = %+v", v)
	}
	if v.Module != "user" || v.UsedBy != 0 {
		t.Fatalf("module/used_by = %q/%d", v.Module, v.UsedBy)
	}

	got, err := svc.Get(v.ID)
	if err != nil || got.Code != "user_login" {
		t.Fatalf("Get = %v, %v", got, err)
	}

	list, err := svc.List(p.ID, "user")
	if err != nil || len(list) != 1 {
		t.Fatalf("List(module=user) = %d 条, %v", len(list), err)
	}
	// 过滤不匹配的模块应为空
	if l, _ := svc.List(p.ID, "order"); len(l) != 0 {
		t.Fatalf("List(module=order) 应为空，得到 %d", len(l))
	}
}

func TestAPI重名与校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).API

	mustAPI(t, d, p.ID, APIReq{Code: "dup", Path: "/x"})
	if _, err := svc.Create(p.ID, APIReq{Code: "dup", Path: "/y"}); err == nil {
		t.Fatal("同项目 code 重复应拒绝")
	} else {
		wantCode(t, err, response.CodeConflict)
	}

	// 非法输入
	for name, req := range map[string]APIReq{
		"空 code":  {Code: "", Path: "/x"},
		"非法 code": {Code: "a/b", Path: "/x"},
		"空 path":  {Code: "c1", Path: ""},
		"非法方法":    {Code: "c2", Path: "/x", Method: "FETCH"},
		"非法 body": {Code: "c3", Path: "/x", BodyType: "xml"},
	} {
		if _, err := svc.Create(p.ID, req); err == nil {
			t.Errorf("%s 应被拒绝", name)
		} else {
			wantCode(t, err, response.CodeBadParam)
		}
	}

	// 跨项目允许同名
	p2 := seedProject(t, d)
	if _, err := svc.Create(p2.ID, APIReq{Code: "dup", Path: "/x"}); err != nil {
		t.Fatalf("跨项目同名应允许: %v", err)
	}
}

func TestAPI树按服务分组(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).API

	mustAPI(t, d, p.ID, APIReq{Code: "a1", Module: "user", Path: "/a1"})
	mustAPI(t, d, p.ID, APIReq{Code: "a2", Module: "user", Path: "/a2"})
	mustAPI(t, d, p.ID, APIReq{Code: "b1", Module: "order", Path: "/b1"})
	mustAPI(t, d, p.ID, APIReq{Code: "c1", Module: "", Path: "/c1"})

	groups, err := svc.Tree(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 {
		t.Fatalf("应 3 组（order/user/未分类），得到 %d: %+v", len(groups), groups)
	}
	// 排序：order < user < 未分类（拼音/字典序中「未」在后）
	if groups[0].Module != "order" || groups[1].Module != "user" || groups[2].Module != apiModuleUnclassified {
		t.Fatalf("分组顺序 = %q/%q/%q", groups[0].Module, groups[1].Module, groups[2].Module)
	}
	if groups[1].Count != 2 {
		t.Fatalf("user 组数量 = %d", groups[1].Count)
	}
}

func TestAPI删除保护与引用计数(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	apiSvc := New(d).API

	v := mustAPI(t, d, p.ID, APIReq{Code: "referenced", Module: "user", Path: "$base_url/items"})

	// 建一个引用它的用例
	c, err := New(d).Case.Create(p.ID, CaseReq{
		Code: "c_ref", Name: "引用接口的用例",
		Steps: []StepReq{{Seq: 1, StepType: model.StepAPI, RefAPIID: v.ID, Name: "引用步骤"}},
	}, 1)
	if err != nil {
		t.Fatalf("创建引用用例失败: %v", err)
	}

	// 引用计数应变为 1
	got, err := apiSvc.Get(v.ID)
	if err != nil || got.UsedBy != 1 {
		t.Fatalf("UsedBy = %d, %v（期望 1）", got.UsedBy, err)
	}

	// 删除应被拒绝（40003），且提示引用数
	wantCode(t, apiSvc.Delete(v.ID), response.CodeInUse)

	// 解除引用后可删
	if _, err := New(d).Case.Update(c.ID, CaseReq{
		Code: "c_ref", Name: "引用接口的用例",
		Steps: []StepReq{{Seq: 1, StepType: model.StepRequest, Request: jsonx.Any{Val: map[string]any{"method": "GET", "url": "$base_url/x"}}}},
	}); err != nil {
		t.Fatalf("改用例失败: %v", err)
	}
	if err := apiSvc.Delete(v.ID); err != nil {
		t.Fatalf("解除引用后删除应成功: %v", err)
	}
}

func TestAPI引用保存期校验(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	p2 := seedProject(t, d)
	other := mustAPI(t, d, p2.ID, APIReq{Code: "other", Path: "/x"})

	// 引用其它项目的接口 → 40000
	_, err := New(d).Case.Create(p.ID, CaseReq{
		Code: "c_bad", Name: "跨项目引用",
		Steps: []StepReq{{Seq: 1, StepType: model.StepAPI, RefAPIID: other.ID}},
	}, 1)
	wantCode(t, err, response.CodeBadParam)

	// 引用不存在的接口 → 40000
	_, err = New(d).Case.Create(p.ID, CaseReq{
		Code: "c_bad2", Name: "悬空引用",
		Steps: []StepReq{{Seq: 1, StepType: model.StepAPI, RefAPIID: 99999}},
	}, 1)
	wantCode(t, err, response.CodeBadParam)

	// 未选接口 → 40000
	_, err = New(d).Case.Create(p.ID, CaseReq{
		Code: "c_bad3", Name: "未选接口",
		Steps: []StepReq{{Seq: 1, StepType: model.StepAPI}},
	}, 1)
	wantCode(t, err, response.CodeBadParam)
}
