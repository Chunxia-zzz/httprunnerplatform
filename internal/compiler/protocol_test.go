package compiler

import (
	"strings"
	"testing"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/model"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/jsonx"
)

func TestRender_WebSocket步骤(t *testing.T) {
	tc, steps := newCase()
	steps[0].StepType = model.StepWebSocket
	steps[0].WsOpType = "wr"
	steps[0].WsURL = "ws://127.0.0.1:8900/ws"
	steps[0].WsHeaders = jsonx.Map{"X-Trace": "t-1"}
	steps[0].WsPayload = jsonx.Any{Val: "hello"}

	y := renderOne(t, tc, steps).Cases[0].YAML
	for _, want := range []string{"websocket:", "type: wr", "url: ws://127.0.0.1:8900/ws", "text: hello", "X-Trace: t-1"} {
		if !strings.Contains(y, want) {
			t.Fatalf("WS 步骤缺少 %q:\n%s", want, y)
		}
	}
	// 断言属于步骤，仍应渲染
	if !strings.Contains(y, "validate:") {
		t.Fatalf("WS 步骤的断言未渲染:\n%s", y)
	}
}

func TestRender_WebSocket校验(t *testing.T) {
	tc, steps := newCase()
	steps[0].StepType = model.StepWebSocket
	steps[0].WsOpType = "bogus"
	steps[0].WsURL = "ws://x/ws"
	if _, err := Render(&Input{Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}}}); err == nil {
		t.Fatal("非法动作应报错")
	}

	steps[0].WsOpType = "wr"
	steps[0].WsURL = ""
	if _, err := Render(&Input{Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}}}); err == nil {
		t.Fatal("缺 url 应报错（引擎按 URL 查找连接）")
	}

	steps[0].WsURL = "ws://x/ws"
	steps[0].WsPayload = jsonx.Any{}
	if _, err := Render(&Input{Project: newProject(), Env: newEnv(),
		Cases: []CaseSpec{{Case: tc, Steps: steps}}}); err == nil {
		t.Fatal("wr 缺发送内容应报错")
	}
}

func TestRender_GraphQL步骤(t *testing.T) {
	tc, steps := newCase()
	steps[0].StepType = model.StepGraphql
	steps[0].Request = anyOf(map[string]any{"url": "$base_url/graphql"})
	steps[0].GraphqlQuery = "query Q($id: ID!) { user(id: $id) { name } }"
	steps[0].GraphqlOperation = "Q"
	steps[0].GraphqlVariables = jsonx.Any{Val: map[string]any{"id": 1}}

	y := renderOne(t, tc, steps).Cases[0].YAML
	// 查询语句里的 $ 必须转义成 $$（A28），否则引擎会把它当平台变量
	for _, want := range []string{
		"method: POST", "url: $base_url/graphql",
		`query: 'query Q($$id: ID!)`, "operationName: Q", "id: 1",
	} {
		if !strings.Contains(y, want) {
			t.Fatalf("GraphQL 编译结果缺少 %q:\n%s", want, y)
		}
	}
}
