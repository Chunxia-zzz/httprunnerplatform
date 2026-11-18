# Implementation Plan: 项目级成员与变更历史

**Branch**: `008-members-audit` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

`project_member` + `change_log` 两张表；`MemberService`（增删查 + CanAccess）；`AuditService`（Log/List）；
`ProjectGuard` 中间件按路径解析资源所属项目并校验访问；handler 侧写入审计；前端成员抽屉与变更历史页。

## Technical Context

- 访问规则集中在 `MemberService.CanAccess(projectID, userID, isAdmin)`：
  管理员 → 是；项目成员数 0 → 是（存量兼容）；否则查成员表。
- 中间件解析表：`/projects/:id` 直接取值；`/cases|environments|apis|suites|plans|datasets|runs/:id` → 查对应表
  取 project_id（单条 SELECT，失败则放行由 handler 报 404，避免中间件改变 404 语义）。
- 审计写入在 handler（有当前用户）；用例 diff 由 handler 在调用 Update 前取旧值，比较名称/模块/步骤数/断言数。
- 变更列表分页复用 `Page`/`PageData`。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `internal/model/model.go` | ProjectMember / ChangeLog |
| 服务 | `internal/service/member.go`、`audit.go` | 成员与审计 |
| 中间件 | `internal/api/middleware/project_guard.go` | 403 拦截 |
| 接口 | `handler_member.go`、`handler_change.go`、case/api/env/suite/plan/param handler | 端点与写入点 |
| 前端 | `api/members.ts`、`api/changes.ts`、`ProjectListView` 抽屉、`ChangesView.vue`、router/menu | UI |
| 文档 | 契约 / 验收清单 / 清单 / README | 同步 |

## Verification

单测：CanAccess 三态（管理员/无成员/成员）、增删成员与唯一约束、审计列表与用例 diff 摘要、项目列表过滤。
端到端：成员加入前后访问 403/200；修改用例后历史出现 diff。
