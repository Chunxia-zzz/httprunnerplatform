# Implementation Plan: 多协议

**Branch**: `010-multi-protocol` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

WS：模型字段 + `yamlWebSocket` 渲染 + 校验规则 + 编辑器面板；GraphQL：编译为 POST（查询 `$`→`$$`）；gRPC 不做。

## Technical Context

- 引擎事实（实测）：A26 WS 动作式/每步带 URL/末尾补斜杠；A28 `$$` 转义；A27 GraphQL 即 POST。
- WS 步骤不套用 HTTP 的 header 合并与 auth（握手头独立字段），避免把 HTTP 语义误用到 WS。
- GraphQL 复用 `renderRequestInto`，因此认证、动态变量、头合并、尾斜杠提示全部继续生效。
- 编辑器：`stepType: request|api|websocket|graphql`；字段级默认值在 `newStep()` 中给足，避免 v-model 写入 undefined。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `model.go` | ws_url / ws_headers / graphql_* |
| 编译 | `yaml.go`、`compiler.go` | yamlWebSocket + 两个 step 分支 + escapeDollar |
| 校验 | `validator.go` | WS / GraphQL 规则 + BAD_STEP_CONFIG |
| 服务 | `case.go` | StepReq 透传 |
| 前端 | `types/editor.ts`、`StepEditor.vue`、`CaseEditorView.vue`、`api/types.ts` | 面板与转换 |
| 文档 | 实测记录 / 契约 / 清单 / README / 边界表 | 同步 |

## Verification

单测：WS 渲染与三类校验、GraphQL 渲染与 `$$` 转义。
端到端：真实引擎跑 WS 三步（断言 body）与 GraphQL POST（回显校验 query/variables/operationName），非法动作被拦截。
