package compiler

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func TestRender_DB步骤编译为回环POST(t *testing.T) {
	tc := &model.TestCase{Code: "c_db", Name: "db 用例", ProjectID: 1}
	steps := []model.TestStep{
		{
			Seq: 1, StepType: model.StepDB, Name: "造数", Enabled: true,
			DBSourceName: "paydb",
			DBSQL:        "INSERT INTO orders(id, amount) VALUES ($order_id, 100)",
			DBParams:     anyOf([]any{"arg1", "arg2"}),
		},
		{Seq: 2, StepType: model.StepRequest, Name: "调接口", Enabled: true,
			Request: anyOf(map[string]any{"method": "GET", "url": "$base_url/orders"})},
	}
	out, err := Render(&Input{
		Project:     newProject(),
		Env:         newEnv(),
		Cases:       []CaseSpec{{Case: tc, Steps: steps}},
		LoopbackURL: "http://127.0.0.1:18124",
	})
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	yml := out.Cases[0].YAML
	for _, want := range []string{
		"method: POST",
		"url: http://127.0.0.1:18124/dbstep",
		"X-Dbstep-Token: $dbstep_token",
		"conn: paydb",
		"sql: INSERT INTO orders(id, amount) VALUES ($order_id, 100)",
		"params:",
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("db 步骤产物缺少 %q:\n%s", want, yml)
		}
	}
	// 第二步正常编译
	if !strings.Contains(yml, "$base_url/orders") {
		t.Fatalf("HTTP 步骤丢失:\n%s", yml)
	}

	// ⭐ 自动注入的 status_code 断言必须同时进 decl（重建明细的数据源）
	decls := out.Cases[0].Steps
	if len(decls) == 0 || len(decls[0].Assertions) == 0 {
		t.Fatalf("decl.Assertions 为空，fail 步骤的断言明细将无法重建: %+v", decls)
	}
	if decls[0].Assertions[0].Check != "status_code" {
		t.Fatalf("注入的断言不是 status_code: %+v", decls[0].Assertions[0])
	}
}

func TestRender_DB步骤缺回环地址报错(t *testing.T) {
	tc := &model.TestCase{Code: "c_db", Name: "db", ProjectID: 1}
	steps := []model.TestStep{{Seq: 1, StepType: model.StepDB, DBSourceName: "a", DBSQL: "SELECT 1", Enabled: true}}
	_, err := Render(&Input{
		Project: newProject(),
		Env:     newEnv(),
		Cases:   []CaseSpec{{Case: tc, Steps: steps}},
	})
	if err == nil || !strings.Contains(err.Error(), "回环地址") {
		t.Fatalf("缺 LoopbackURL 应报错，得到 %v", err)
	}
}

func TestDBParamsOf(t *testing.T) {
	if got := dbParamsOf(jsonx.Any{}); got != nil {
		t.Fatalf("nil 应返回 nil，得到 %v", got)
	}
	got := dbParamsOf(anyOf([]any{"a", float64(1)}))
	if len(got) != 2 || got[0] != "a" || got[1] != float64(1) {
		t.Fatalf("数组参数解析错误: %v", got)
	}
}
