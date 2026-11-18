package service

import (
	"strings"
	"testing"
)

const oas3Doc = `
openapi: 3.0.3
servers:
  - url: https://api.example.com/v1
paths:
  /users/{id}:
    get:
      operationId: getUserById
      summary: 查用户
      tags: [user]
      parameters:
        - name: verbose
          in: query
          schema: {type: boolean}
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/User'
  /orders:
    post:
      summary: 建订单
      tags: [order]
      requestBody:
        content:
          application/json:
            example: {"sku": "A1", "qty": 2}
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: integer}
                  ok: {type: boolean}
components:
  schemas:
    User:
      type: object
      properties:
        id: {type: integer}
        name: {type: string}
        tags:
          type: array
          items: {type: string}
    Node:
      type: object
      properties:
        child:
          $ref: '#/components/schemas/Node'
`

const swagger2Doc = `
swagger: "2.0"
host: api.old.com
basePath: /api
schemes: [https]
paths:
  /items/{itemId}:
    get:
      operationId: get_item
      summary: 取物料
      responses:
        '200':
          examples:
            application/json: {"id": 1, "name": "螺丝"}
  /no-tag/thing:
    put:
      description: 无 tag 的接口
      parameters:
        - name: body
          in: body
          schema:
            type: object
            properties:
              a: {type: string}
      responses:
        '200':
          schema:
            type: object
            properties:
              ok: {type: boolean}
`

func TestParseOpenAPI_v3(t *testing.T) {
	doc, err := parseOpenAPIDoc([]byte(oas3Doc))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Detected != "openapi3" || len(doc.Servers) != 1 || doc.Servers[0] != "https://api.example.com/v1" {
		t.Fatalf("版本/服务器 = %q %v", doc.Detected, doc.Servers)
	}
	if len(doc.APIs) != 2 {
		t.Fatalf("接口数 = %d", len(doc.APIs))
	}

	byCode := map[string]ImportedAPI{}
	for _, a := range doc.APIs {
		byCode[a.Code] = a
	}
	users, ok := byCode["getuserbyid"]
	if !ok {
		t.Fatalf("缺少 getuserbyid: %+v", doc.APIs)
	}
	if users.Code != "getuserbyid" || users.Module != "user" ||
		users.Method != "GET" || users.Path != "https://api.example.com/v1/users/{id}" {
		t.Fatalf("GET /users 解析 = %+v", users)
	}
	if users.Params["verbose"] != false {
		t.Fatalf("查询参数示例 = %v", users.Params["verbose"])
	}
	// 响应示例解析 $ref：User 结构
	rb, ok := users.ResponseBody.(map[string]any)
	if !ok || rb["id"] != 0 || rb["name"] != "string" {
		t.Fatalf("响应示例 = %#v", users.ResponseBody)
	}
	if users.ResponseType != "json" || users.BodyType != "none" {
		t.Fatalf("类型 = resp:%q body:%q", users.ResponseType, users.BodyType)
	}

	orders, ok := byCode["post_orders"]
	if !ok {
		t.Fatalf("缺少 post_orders: %+v", doc.APIs)
	}
	if orders.Code != "post_orders" || orders.Module != "order" || orders.BodyType != "json" {
		t.Fatalf("POST /orders 解析 = %+v", orders)
	}
	req, ok := orders.Body.(map[string]any)
	if !ok || req["sku"] != "A1" {
		t.Fatalf("请求体示例 = %#v", orders.Body)
	}
	resp, ok := orders.ResponseBody.(map[string]any)
	if !ok || resp["ok"] != false {
		t.Fatalf("201 响应示例 = %#v", orders.ResponseBody)
	}
}

func TestParseOpenAPI_swagger2(t *testing.T) {
	doc, err := parseOpenAPIDoc([]byte(swagger2Doc))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Detected != "swagger2" {
		t.Fatalf("版本 = %q", doc.Detected)
	}
	if len(doc.APIs) != 2 {
		t.Fatalf("接口数 = %d", len(doc.APIs))
	}
	item := doc.APIs[0]
	if item.Path != "https://api.old.com/api/items/{itemId}" {
		t.Fatalf("path = %q", item.Path)
	}
	// 无 tags → 路径首段
	if item.Module != "items" {
		t.Fatalf("module = %q", item.Module)
	}
	if resp, ok := item.ResponseBody.(map[string]any); !ok || resp["name"] != "螺丝" {
		t.Fatalf("examples 响应 = %#v", item.ResponseBody)
	}

	noTag := doc.APIs[1]
	if noTag.Module != "no-tag" || noTag.BodyType != "json" {
		t.Fatalf("no-tag 接口 = %+v", noTag)
	}
	if b, ok := noTag.Body.(map[string]any); !ok || b["a"] != "string" {
		t.Fatalf("body 参数 = %#v", noTag.Body)
	}
}

func TestParseOpenAPI_循环ref不挂起(t *testing.T) {
	doc := `
openapi: 3.0.0
paths:
  /tree:
    get:
      responses:
        '200':
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Node'}
components:
  schemas:
    Node:
      type: object
      properties:
        name: {type: string}
        child: {$ref: '#/components/schemas/Node'}
`
	res, err := parseOpenAPIDoc([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if res.APIs[0].Path != "$base_url/tree" {
		t.Fatalf("无 servers 时应回退 $base_url，得到 %q", res.APIs[0].Path)
	}
	m := res.APIs[0].ResponseBody.(map[string]any)
	if m["name"] != "string" || m["child"] == nil {
		t.Fatalf("循环引用应截断为空对象而不报错: %#v", m)
	}
}

func TestParseOpenAPI_版本识别失败(t *testing.T) {
	for _, doc := range []string{"", "foo: bar", "{\"openapi\": \"2.0\"}"} {
		if _, err := parseOpenAPIDoc([]byte(doc)); err == nil {
			t.Errorf("文档 %q 应被拒绝", doc)
		}
	}
}

func TestParseOpenAPI_code去重与清洗(t *testing.T) {
	doc := `
openapi: 3.0.0
paths:
  /a:
    get:
      operationId: same.id
      responses: {'200': {description: ok}}
  /b:
    get:
      operationId: same.id
      responses: {'200': {description: ok}}
  /c:
    post:
      responses: {'200': {description: ok}}
`
	res, err := parseOpenAPIDoc([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{res.APIs[0].Code, res.APIs[1].Code, res.APIs[2].Code}
	if codes[0] != "same_id" || codes[1] != "same_id_2" {
		t.Fatalf("重复 operationId 去重失败: %v", codes)
	}
	if !strings.HasPrefix(codes[2], "post_") {
		t.Fatalf("无 operationId 时应用 method_path: %v", codes)
	}
}

func TestOpenAPI提交策略(t *testing.T) {
	d := testDeps(t)
	p := seedProject(t, d)
	svc := New(d).OpenAPI

	prev, err := svc.Preview(p.ID, []byte(oas3Doc))
	if err != nil {
		t.Fatal(err)
	}
	if prev.APIs[0].Conflicts {
		t.Fatal("首次导入不应有冲突")
	}

	r1, err := svc.Commit(p.ID, OpenAPICommitReq{APIs: prev.APIs, Policy: "skip"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Created != 2 || len(r1.Failed) != 0 {
		t.Fatalf("首次提交 = %+v", r1)
	}

	// 第二次预览：全部冲突
	prev2, err := svc.Preview(p.ID, []byte(oas3Doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range prev2.APIs {
		if !a.Conflicts {
			t.Fatalf("%s 应标记冲突", a.Code)
		}
	}

	// skip：全部跳过；overwrite：全部覆盖
	if r2, _ := svc.Commit(p.ID, OpenAPICommitReq{APIs: prev2.APIs, Policy: "skip"}); r2.Skipped != 2 || r2.Created != 0 {
		t.Fatalf("skip 结果 = %+v", r2)
	}
	if r3, _ := svc.Commit(p.ID, OpenAPICommitReq{APIs: prev2.APIs, Policy: "overwrite"}); r3.Overwritten != 2 {
		t.Fatalf("overwrite 结果 = %+v", r3)
	}

	// 落库内容与语义检查：请求体与响应体分开存储
	list, err := New(d).API.List(p.ID, "order")
	if err != nil || len(list) != 1 {
		t.Fatalf("接口库中 order 分组 = %d, %v", len(list), err)
	}
	if list[0].ResponseType != "json" || list[0].BodyType != "json" || list[0].Source != "openapi" {
		t.Fatalf("落库类型 = %+v", list[0])
	}
}
