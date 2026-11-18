# Implementation Plan: 接口文档 + 只读分享

**Branch**: `006-docs-share` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

新增 `DocService`（文档构建 + HTML 渲染）与 `ShareService`（token 签发/校验/吊销），端点分两组：登录态（预览/分享管理）与公开（`/open/docs/{token}` 只读）。

## Technical Context

- token：32 字节随机（`crypto/rand`）→ hex；存 sha256 hex；沿用 `APIToken` 的哈希理由（无猜测空间，不用 bcrypt）。
- 文档构建：`api_def` 全量 + `test_step(step_type=api)` 反查用例（含 last_status）→ 分组排序（module, code）。
- HTML：`html/template` + 内嵌 CSS；`text/template` 禁止（会逃逸失效）。
- 公开端点：注册在 `/open` 之外的独立组 `r.GET("/open/docs/:token")`？—— `/open` 组已挂 Token 中间件，不能复用；新组 `docs`，只注册 GET/HEAD。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `internal/model/model.go` | `ShareLink` |
| 服务 | `internal/service/docsvc.go` | 文档构建 + HTML |
| 服务 | `internal/service/sharesvc.go` | 签发/列表/吊销/校验 |
| 接口 | `internal/api/handler_docs.go`、`router.go` | 两组端点 |
| 前端 | `web/src/api/docs.ts`、`views/DocsView.vue`、router、menu | 文档页 |
| 文档 | 契约 / 清单 / README / 验收边界 | 同步 |

## Verification

单测：文档分组与覆盖率、关联用例、HTML 转义、token 校验（有效/过期/不存在）、吊销。
端到端：生成链接 → 未登录 GET 200 → 过期 410 → 吊销 404。
