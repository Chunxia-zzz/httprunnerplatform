# Implementation Plan: Mock Server

**Branch**: `004-mock-server` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

## Summary

项目级开关 + 公开 mock 端点（`/mock/{projectCode}/*path`）+ 基于 `api_def` 的路径模板匹配 + 内存命中日志 + 前端 Mock 页。

## Technical Context

- 匹配算法（纯函数，单测重点）：`normalizePath(p)` 去 scheme/host/`$base_url`/query；段级比较，`{x}`/`:x`/`$x` 为单段通配。
- 命中日志：`MockService` 内 `sync.Mutex` + 每项目 ring（200 条），不落库。
- 公开路由：`r.Any("/mock/*path")` 注册在 `/api/v1` 之外，避免误套会话中间件与 JSON 错误兜底。
- 复用：`api_def` 的 method/path/body/body_type；项目按 code 查（不暴露内部 id）。
- 开关存储：`project.mock_enabled`（布尔列不带 default 标签——项目既有约定）。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `internal/model/model.go` | `Project.MockEnabled` |
| 服务 | `internal/service/mocksvc.go` | 匹配/开关/命中日志 |
| 接口 | `internal/api/handler_mock.go`、`router.go` | 公开 mock + 项目管理端点 |
| 前端 | `web/src/api/mock.ts`、`views/MockView.vue`、`router`、`MainLayout` | 新页面 |
| 文档 | `docs/接口契约.md`、`docs/待开发功能清单.md`、README | 同步 |

## Verification

单测：路径归一化与三类模板匹配（表驱动）、未开启/未命中语义、ring 上限。
端到端：真实 curl 打 mock 端点（200/404/命中日志）。
门禁：`scripts/check.sh` + CI。
