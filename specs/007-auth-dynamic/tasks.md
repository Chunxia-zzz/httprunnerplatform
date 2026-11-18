# Tasks: 认证助手 + 动态变量

## Phase 1 — 模型与服务

- [x] T1 `AuthConfig`（Valuer/Scanner）+ environment.auth / test_step.auth
- [x] T2 环境：请求体接收 auth、非管理员掩码、掩码提交保持原值
- [x] T3 步骤：StepReq/模型透传 auth；归一（type 校验）

## Phase 2 — 编译器

- [x] T4 `resolveAuth`（步骤 inherit→环境）+ `applyAuth`（bearer/basic/api_key）
- [x] T5 认证头作为最低优先级层（手写请求头覆盖）
- [x] T6 `expandDynamic`：URL/头/参数/body 递归替换 `{{$timestamp}}`/`{{$timestampMs}}`/`{{$uuid}}`

## Phase 3 — 校验与前端

- [x] T7 后端 varScope 白名单；前端 BUILTIN_VARS 同步
- [x] T8 环境表单「认证」区块（类型 + 条件字段 + 掩码展示）
- [x] T9 步骤编辑器「认证」页签（inherit/none/bearer/basic/api_key + `$token` 提示）
- [x] T10 用例编辑器 EditorStep 增加 auth 字段与转换

## Phase 4 — 测试与文档

- [x] T11 单测：优先级/注入/覆盖/动态变量/掩码
- [x] T12 端到端：环境与步骤 auth 真实执行 + 动态变量两次不同
- [x] T13 契约 + 验收指南 + 清单 + README
- [x] T14 `scripts/check.sh` + CI
