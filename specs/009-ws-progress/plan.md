# Implementation Plan: 执行进度推送

**Branch**: `009-ws-progress` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

`EventHub` 进程内广播 + `GET /api/v1/ws/runs/{id}`（gorilla/websocket）+ 执行链路发布点 + 前端 WS/轮询降级。

## Technical Context

- 依赖：`github.com/gorilla/websocket v1.5.3`（本机模块缓存可用）。
- 鉴权：WS 握手带会话 cookie（同源）；项目权限在 handler 显式校验（ProjectGuard 只认 REST 路径）。
- 403 用真实 HTTP 状态（不是 200+业务码）：WS 客户端只看状态码。
- 发布点：run.go（running/persist/finish）、run_suite.go（running/逐条/finishSuite）。
- 前端：`utils/runSocket.ts` 只负责连接与转发；降级/重连策略放在页面。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 服务 | `internal/service/events.go` | EventHub + 事件类型 |
| 服务 | `run.go`、`run_suite.go`、`service.go` | 发布点与单例注入 |
| 接口 | `handler_ws.go`、`router.go` | WS 端点 |
| 前端 | `utils/runSocket.ts`、`RunDetailView.vue`、`vite.config.ts` | 订阅与降级 |
| 文档 | 契约 3H、方案、清单、README、验收边界 | 同步 |

## Verification

单测：Hub 广播/隔离/慢订阅者/关闭；WS 集成（快照、事件、终态关闭、已结束即关）。
端到端：真实 WS 客户端 + 真实执行（多标签页、403、降级路径）。
