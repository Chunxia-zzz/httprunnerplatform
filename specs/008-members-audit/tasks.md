# Tasks: 项目级成员与变更历史

## Phase 1 — 模型与服务

- [x] T1 `ProjectMember` / `ChangeLog` 模型 + 迁移清单
- [x] T2 `MemberService`：List/Add/Remove/CanAccess（无成员=开放）
- [x] T3 `AuditService`：Log / List（筛选 + 分页）
- [x] T4 项目列表按可见性过滤（非管理员）

## Phase 2 — 访问控制与端点

- [x] T5 `ProjectGuard` 中间件（/projects/:id 与嵌套资源）
- [x] T6 成员端点（GET/POST/DELETE，写操作仅管理员）
- [x] T7 变更历史端点 + 用例 diff 摘要

## Phase 3 — 审计写入

- [x] T8 用例/接口/环境/用例集/计划/参数集的增删改写入审计

## Phase 4 — 前端

- [x] T9 `api/members.ts` / `api/changes.ts`
- [x] T10 项目列表「成员」抽屉（名单/添加/移除）
- [x] T11 变更历史页（筛选 + 分页）+ 路由菜单

## Phase 5 — 测试与文档

- [x] T12 单测：访问三态、成员增删、审计与 diff、列表过滤
- [x] T13 端到端：403/200、成员增删、diff 摘要
- [x] T14 契约 + 清单 + 验收清单 + README
- [x] T15 `scripts/check.sh` + CI
