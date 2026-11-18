# Implementation Plan: 认证助手 + 动态变量

**Branch**: `007-auth-dynamic` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

`AuthConfig`（JSON 列）挂到 environment 与 test_step；编译器解析生效认证并注入头/参数；编译期展开 `{{$timestamp}}`/`{{$timestampMs}}`/`{{$uuid}}`；两端变量校验白名单同步。

## Technical Context

- AuthConfig 实现 `driver.Valuer` / `sql.Scanner`（空串 ↔ 零值），列 `default:''` 以支持老库增量迁移。
- 注入位置：`renderRequestInto` 里形成 `merged` 之前：`merged = merge(envHeaders, caseHeaders)`；认证头作为**最底层**：
  `merged = merge(authHeaders, merged)`，随后 `merge(merged, req.Headers)` 让手写覆盖。
- 动态变量：`expandDynamic(s)` 正则在 `{{$name}}`；body 走递归遍历（map/slice/string）。
- 掩码：沿用 `MaskedValue` 与 `sanitizeEnvMap` 的语义（auth 三个敏感字段单独处理）。
- 校验白名单：后端 `newVarScope` 定义四个名字；前端 `BUILTIN_VARS` 同步。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `internal/model/auth.go`（新）、model.go | AuthConfig + 两处字段 |
| 服务 | `environment.go`、`case.go` | auth 归一/掩码/StepReq 透传 |
| 编译 | `internal/compiler/compiler.go` | resolveAuth / applyAuth / expandDynamic |
| 校验 | `internal/validator/validator.go` | 动态变量白名单 |
| 前端 | `api/types.ts`、`EnvironmentListView.vue`、`StepEditor.vue`、`types/editor.ts`、`CaseEditorView.vue`、`utils/variables.ts` | 表单与提示 |
| 文档 | 契约 / 验收指南 / 清单 / README | 同步 |

## Verification

单测：auth 解析优先级、bearer/basic/api_key 注入、手写覆盖、动态变量格式与唯一性、掩码保持。
端到端：真实引擎执行两种 auth + 动态变量（用 /runs 报文快照或 debug 结果断言请求头）。
