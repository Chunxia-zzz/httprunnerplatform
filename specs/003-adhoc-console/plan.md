# Implementation Plan: 请求调试台

**Branch**: `003-adhoc-console` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

## Summary

把 `DebugStep` 的「编译→临时工作区→执行→解析」抽为 `runSyntheticCase` 内部函数；新增 `DebugRequest` 服务方法与 `POST /projects/{id}/debug/request` 端点；前端新增调试台页，保存出口复用 F1 接口库与用例创建端点。

## Technical Context

- 合成用例：`model.TestCase{Code:"adhoc", Name:"临时请求", ProjectID}` + 单步骤 `StepRequest`；不写库。
- 环境：`resolveEnv(db, projectID, envID)` 复用（0 → 默认环境）。
- 渲染：`compiler.Render` 与正式执行同一条路径（保证 `$base_url`、变量、尾斜杠行为一致）。
- 结果：复用 `DebugStepResult`，`target_seq=1`、`steps[0]` 即响应载体。
- 前端：`web/src/api/debug.ts`（新增 request 封装）+ `views/DebugConsoleView.vue`；保存调用 `apiDefApi.createApi` / `caseApi.createCase`。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 服务 | `internal/service/debug.go` | 抽 `runSyntheticCase`；新增 `DebugRequest` |
| 接口 | `internal/api/handler_debug.go`、`router.go` | 新端点 |
| 前端 | `web/src/api/debug.ts`、`types.ts`、`views/DebugConsoleView.vue`、`router`、`MainLayout` | 新页面 |
| 文档 | `docs/接口契约.md`、`docs/待开发功能清单.md` | 同步 |

## Verification

单测：参数校验、项目/环境不存在、非法 body_type（不依赖 hrp）。
端到端（本地）：真实 hrp 发请求 → 200 面板；存接口 → 列表可见；存用例 → 编辑器可执行。
门禁：`scripts/check.sh` + CI。
