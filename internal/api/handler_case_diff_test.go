package api

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func TestCaseDiffSummary(t *testing.T) {
	old := &service.CaseDetail{
		TestCase: &model.TestCase{Name: "登录", Module: "user", Code: "c_login"},
		Steps: []model.TestStep{
			{Seq: 1, Validate: jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq"}}},
			{Seq: 2},
		},
	}
	req := service.CaseReq{Name: "登录流程", Module: "auth", Steps: []service.StepReq{
		{}, {}, {},
	}}
	now := &service.CaseDetail{
		TestCase: &model.TestCase{Name: "登录流程", Module: "auth", Code: "c_login"},
		Steps: []model.TestStep{
			{Seq: 1, Validate: jsonx.Slice[model.AssertItem]{{Check: "status_code", Assert: "eq"}}},
			{Seq: 2}, {Seq: 3},
		},
	}

	got := caseDiffSummary(old, req, now)
	for _, want := range []string{"名称「登录」→「登录流程」", "模块「user」→「auth」", "步骤 2 → 3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("摘要缺少 %q: %s", want, got)
		}
	}
	// 断言数未变，不应该出现在摘要里
	if strings.Contains(got, "断言") {
		t.Fatalf("断言数未变却出现差异: %s", got)
	}

	// 无结构差异
	same := caseDiffSummary(now, req, now)
	if !strings.Contains(same, "未检测到结构性差异") {
		t.Fatalf("无差异时应明确说明: %s", same)
	}
}
