# Tasks: 执行进度推送

## Phase 1 — 事件与发布

- [x] T1 `EventHub`（订阅/广播/退订/慢订阅者丢弃/Close）+ 事件类型
- [x] T2 Deps.Events 单例（`service.New` 兜底创建，Set 暴露给 handler）
- [x] T3 发布点：单用例（running/persist/finish）、用例集（running/逐条/skipped/终态）

## Phase 2 — WS 端点

- [x] T4 `GET /api/v1/ws/runs/:id`：会话鉴权 + 项目成员校验（非成员 HTTP 403）
- [x] T5 初始快照 / 已终态即关 / 25s ping / 断开清理订阅

## Phase 3 — 前端

- [x] T6 `utils/runSocket.ts`（同源 cookie、事件解析、关闭函数）
- [x] T7 详情页：WS 优先 + 150ms 去抖刷新 + 断线降级 1s 轮询 + 5s 重连 + 模式角标
- [x] T8 Vite 代理 `ws: true`

## Phase 4 — 测试与文档

- [x] T9 Hub 单测（4 组）
- [x] T10 WS 集成测试（快照/事件/终态关闭/已结束即关）
- [x] T11 端到端：真实 WS 客户端 6 步（含多标签页、403、降级）
- [x] T12 文档：契约 3H + 方案口径 + 清单 + README + 边界表
- [x] T13 `scripts/check.sh` + CI
