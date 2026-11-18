# Tasks: 多协议

## Phase 1 — 技术验证（先行）

- [x] T1 实测 WS：动作枚举、每步带 URL、URL 末尾补 `/`、断言 `body`（A26）
- [x] T2 实测 GraphQL：POST 包装可行；`$` 被当变量 → `$$` 转义（A27/A28）
- [x] T3 结论：gRPC 不做（需 protobuf 运行时，单独立项）

## Phase 2 — 后端

- [x] T4 模型字段：ws_url/ws_headers/ws_payload + graphql_query/variables/operation
- [x] T5 编译器：websocket 段渲染；graphql → POST + JSON（查询 `$`→`$$`）
- [x] T6 校验器：动作/URL/payload、GraphQL 地址与查询；新增 BAD_STEP_CONFIG
- [x] T7 StepReq 透传

## Phase 3 — 前端

- [x] T8 类型与 newStep 默认值
- [x] T9 步骤编辑器：WS 面板（动作/地址/发送内容/握手头）+ GraphQL 面板（查询/操作名/variables 校验）
- [x] T10 用例编辑器双向转换

## Phase 4 — 测试与文档

- [x] T11 单测：WS 渲染/校验、GraphQL 渲染与 `$$`
- [x] T12 端到端：WS 三步 pass、GraphQL 断言通过、非法动作拦截
- [x] T13 实测记录 A26/A27/A28 + 契约 3I + 清单 + README + 边界表
- [x] T14 `scripts/check.sh` + CI
