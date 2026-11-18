# Implementation Plan: 通知

**Branch**: `014-notify` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

`NotifyService`（配置 CRUD + 4 渠道 payload + 签名 + 重试 + 投递日志）；执行终态异步派发；前端通知设置页；用本地 mock 接收端完成联调（无真实群机器人）。

## Technical Context

- 渠道协议按官方群机器人文档实现，签名公式单独抽成纯函数以便单测与 mock 端独立复算。
- 异步派发：`go` 协程 + 5s 超时；执行收尾路径只做一次"投递登记"，不阻塞。
- 日志：即使失败也写 `notify_log`（含 HTTP 状态与响应片段），设置页可查。
- 掩码：读接口对非管理员把 `secret` 替换为 `***`；更新时 `***` = 保持原值（复用 env 既有语义）。
- 无新增第三方依赖（标准库 hmac/sha256/base64）。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `model.go` | `NotifyLog` |
| 服务 | `service/notify.go` | CRUD/签名/payload/投递/日志 |
| 接口 | `handler_notify.go`、`router.go` | 6 个端点 |
| 执行 | `run.go`、`service.go` | 终态派发钩子 |
| 前端 | `api/notify.ts`、`views/NotifyView.vue`、router、menu | 设置页 |
| 文档 | 契约 / 清单 / README / 边界表 | 同步 |

## Verification

单测：4 渠道 payload、钉钉/飞书签名（固定 timestamp/secret 的期望值）、掩码、过滤规则、重试计数。
端到端：本地 mock 接收端（Python）→ 配 4 个渠道 → 测试发送 → 校验 payload/签名 → 500 重试 → 失败执行必推。
