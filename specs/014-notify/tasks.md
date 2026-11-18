# Tasks: 通知

## Phase 1 — 数据与服务

- [x] T1 `NotifyLog` 模型 + 迁移清单
- [x] T2 配置 CRUD（掩码、webhook 校验、管理员写入）
- [x] T3 4 渠道 payload 与签名（纯函数）
- [x] T4 投递：超时/重试/日志

## Phase 2 — 集成

- [x] T5 执行终态钩子（失败必推、成功按开关；异步）
- [x] T6 端点：CRUD、test、logs

## Phase 3 — 前端

- [x] T7 `api/notify.ts` + 类型
- [x] T8 通知设置页（列表/编辑/测试/投递记录）
- [x] T9 路由 + 菜单

## Phase 4 — 测试与文档

- [x] T10 单测：payload/签名/掩码/过滤/重试
- [x] T11 端到端：mock 接收端跑通 4 渠道 + 签名比对 + 500 重试 + 失败必推
- [x] T12 契约 + 清单 F2 ✅ + README + 边界表
- [x] T13 `scripts/check.sh` + CI
