# Feature Specification: 多协议（WebSocket / GraphQL；gRPC 明确不支持）

**Feature Branch**: `010-multi-protocol`

**Created**: 2026-11-18

**Status**: Done

**Input**: 待开发功能清单 F10——覆盖非 HTTP 接口。先做技术验证再定范围：WebSocket 复用引擎能力、GraphQL 用 HTTP 包装、gRPC 不做。

## 技术验证结论（先于实现）

| 协议 | 引擎能力 | 结论 |
|:---|:---|:---|
| WebSocket | hrp v4 原生 `websocket` 步骤（**动作式**：open/ping/wr/w/r/close） | ✅ 支持 |
| GraphQL | 无专门步骤；GraphQL over HTTP 就是 `POST + JSON {query,variables,operationName}` | ✅ 用普通请求编译，无需新协议 |
| gRPC | 无原生支持，需 protobuf 运行时 + 动态描述符（服务端反射） | ❌ 不支持，单独立项 |

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 编排 WebSocket 链路 (Priority: P1)

用例里添加 WebSocket 步骤：先 `open` 建连接，再 `wr` 发送并收消息（断言 `body` 等于回显内容），最后 `close`。执行详情里三步都显示 pass，失败时能看到引擎原始错误。

**Independent Test**: 对着 WS echo 服务跑 open→wr→close，`wr` 断言 `body == "echo:hello"` 通过。

### User Story 2 - GraphQL 查询 (Priority: P1)

步骤类型选 GraphQL：填地址、查询语句、variables、operationName。编译产物是一个普通 POST（body 为 `{query, variables, operationName}`），断言/提取/认证与 HTTP 完全一致。

**Independent Test**: 对回显服务执行 GraphQL 步骤，断言 `body.request_body.operationName == "Q"`、`body.method == "POST"` 通过；查询里的 `$id` 原样发出（未被当平台变量）。

### User Story 3 - 明确的能力边界 (Priority: P1)

校验器与文档必须对三种协议给出明确结论：WS 动作非法/缺 URL/缺发送内容报错；GraphQL 缺地址/缺查询报错；gRPC 明确标注不支持而非给个假入口。

**Independent Test**: 非法 WS 动作 → `BAD_STEP_CONFIG` 错误；文档里 gRPC 一行结论为「不支持，需单独立项」。

### Edge Cases

- **每个 WS 动作都必须带 URL**：引擎按 URL 查找连接，缺 URL 报 `no connection`（实测 A26）。
- 引擎会把 WS 地址末尾补成 `/`（`/ws` → `/ws/`），服务端需容忍；平台不改写地址（改写会掩盖真实请求）。
- WS 的断言/提取用 `body`（收到的消息），不是 HTTP 的 `body.json.*`。
- GraphQL 查询语句里的 `$` 与平台变量语法同形：编译期转义为 `$$`（引擎的转义写法，实测 A28），发出时还原为 `$`；需要平台变量请写在 variables 中。
- variables 非法 JSON：编辑器即时提示，保存时后端拒绝。
- 二进制帧（binary）暂不支持：平台只做文本帧，避免 base64 编解码带来的歧义。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`test_step` 增加 `ws_url` / `ws_headers`（已有 `ws_op_type` / `ws_payload`）与 `graphql_query` / `graphql_variables` / `graphql_operation`。
- **FR-2**：编译器把 `websocket` 步骤渲染成 `websocket: {type,url,headers,text}`；把 `graphql` 步骤渲染成 `request: {method: POST, url, json: {query,variables,operationName}}`。
- **FR-3**：GraphQL 查询的 `$` 转义为 `$$`（A28）；variables 里的变量引用保持原样（引擎会替换）。
- **FR-4**：校验器：WS 动作枚举、URL 必填、`w`/`wr` 的 payload 必填；GraphQL 地址与查询必填；错误码 `BAD_STEP_CONFIG` / `MISSING_URL`。
- **FR-5**：前端步骤编辑器：类型选择增加 WebSocket / GraphQL；WS 面板（动作、地址、发送内容、握手头）；GraphQL 面板（查询、operationName、variables JSON 校验与格式化）。
- **FR-6**：gRPC 不做（无 UI、无占位），文档与清单明确标注原因。
- **FR-7**：文档：引擎实测 A26/A27/A28、契约章节、清单 F10、README、验收边界表。

### Key Entities

- **WS 步骤**：`ws_op_type`（动作）、`ws_url`、`ws_headers`、`ws_payload`（text 帧）。
- **GraphQL 步骤**：复用 `request.url/headers`，`graphql_query/variables/operation`。

## Success Criteria *(mandatory)*

- **SC-1**：WS open→wr→close 三步全 pass，断言 `body` 生效。
- **SC-2**：GraphQL 步骤执行成功，且 `$id` 原样到达服务端。
- **SC-3**：非法 WS 动作被校验器拦截（`BAD_STEP_CONFIG`）。
- **SC-4**：gRPC 在文档中有明确「不支持」结论。
- **SC-5**：`scripts/check.sh` + CI 全绿。
