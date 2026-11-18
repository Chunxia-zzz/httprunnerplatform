# Feature Specification: 项目级成员与变更历史

**Feature Branch**: `008-members-audit`

**Created**: 2026-11-18

**Status**: Draft

**Input**: 待开发功能清单 F8——项目成员管理（非成员 403）+ 关键实体的变更历史（谁、何时、改了什么）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 项目成员管理 (Priority: P1)

项目页里可以查看成员、按用户名添加成员、移除成员；管理员可为任意项目增删成员，普通成员只能查看名单。

**Why this priority**: 30 人团队里「谁能看这个项目」必须能回答；没有成员概念时所有项目对所有人可见。

**Independent Test**: 添加成员 B 后，B 的项目列表出现该项目；移除后消失。

### User Story 2 - 非成员访问被拒 (Priority: P1)

非管理员访问自己不属于的项目（页面或 API）得到 **403**；项目列表只显示「自己是成员的项目」以及「尚未设置成员的项目」。

**Why this priority**: 这是 F8 的核心验收口径。

**Independent Test**: 用 C 账号调 `GET /projects/{id}/cases` 返回 403；C 的项目列表里没有该项目。

### User Story 3 - 变更历史可查、用例改动有摘要 (Priority: P1)

「变更历史」页按项目展示：时间、操作人、动作（新建/修改/删除）、对象类型与名称、摘要。用例修改时摘要包含**结构化差异**（名称/模块/步骤数/断言数的变化），而不是一句「已更新」。

**Why this priority**: 「谁把这条用例改坏了」是协作里最常见的追问；只有「已更新」等于没记。

**Independent Test**: 修改用例（加一步、改名字）→ 历史出现一条「修改用例」，摘要含「步骤 2 → 3」「名称 A → B」。

### Edge Cases

- **存量项目**：`project_members` 为空且 `project.access_restricted=false` 的项目视为「未设置成员」，对所有登录用户可见（避免升级后所有人被锁在门外）；
  一旦添加第一个成员，`access_restricted` 置 true，**永不自动回退**——移除最后一个成员后项目仍是「仅成员可见」。
  用「成员数是否为 0」判断会在移除最后一人时把私有项目悄悄变回全员可见，这是数据泄漏。
- 管理员（`role=admin`）永远可见全部项目，且不受成员表约束。
- 项目所有者（`owner_id`）添加成员时自动成为 owner 角色；不允许把最后一个 owner 移除（否则项目无人可管，管理员除外）。
- 重复添加同一成员：返回 40001（已存在）。
- 移除成员后，该成员的历史记录仍保留（审计不可删）。
- 变更历史只记**关键实体**：用例、接口、环境、用例集、计划、参数集；执行记录不进历史（执行本身有记录）。
- 变更历史只读，不提供删除/编辑（审计数据不可篡改）。
- diff 摘要只覆盖用例（结构化字段），其他实体记「动作 + 名称」，不伪造 diff。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`project.access_restricted` 收敛标记（含幂等回填）+ `project_member` 表（project_id, user_id, role=owner/member，唯一索引 project_id+user_id）+ `change_log` 表（project_id, user_id, username, action, entity, entity_id, entity_key, summary, created_at）。
- **FR-2**：成员端点：`GET/POST /projects/{id}/members`、`DELETE /projects/{id}/members/{userId}`；写操作仅管理员。
- **FR-3**：访问控制：新增 `ProjectGuard` 中间件，覆盖 `/projects/:id/*` 与嵌套资源路径（cases/environments/apis/suites/plans/datasets/runs 的 `:id`），解析出 project 后校验：
  管理员放行；项目无成员放行；否则要求是成员，不然 **403**。
- **FR-4**：项目列表对非管理员按「成员项目 + 无成员项目」过滤。
- **FR-5**：变更记录写入点：用例/接口/环境/用例集/计划/参数集的创建、修改、删除（handler 层调用 `AuditService.Log`，带当前用户）。
- **FR-6**：变更历史端点：`GET /projects/{id}/changes?entity=&action=&page=`，倒序分页。
- **FR-7**：用例修改的摘要为结构化 diff（名称、模块、步骤数、断言数），由修改前快照与提交内容比较得出；其他实体摘要为「动作 + 对象名」。
- **FR-8**：前端：项目列表行内「成员」入口（抽屉：名单/添加/移除）；独立「变更历史」页（筛选 + 分页）。
- **FR-9**：文档同步：契约（成员/历史/403 语义）、方案与验收清单、待开发清单 F8、README。

### Key Entities

- **ProjectMember**：项目、用户、角色（owner/member）。
- **ChangeLog**：项目、操作人（id + 用户名冗余）、动作、实体类型/id/标识、摘要、时间。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：非成员访问项目任一入口得 403；加入成员后可访问。
- **SC-2**：项目列表正确过滤（成员项目 + 无成员项目）。
- **SC-3**：修改用例后，历史中出现「步骤 2 → 3」这类 diff 摘要，操作人正确。
- **SC-4**：无成员项目保持全员可见（升级不锁门）。
- **SC-5**：`scripts/check.sh` + CI 全绿。

## Assumptions

- 不做项目级自定义角色（owner/member 两档足够；更细的权限等真有需求再说）。
- 变更历史不做字段级完整快照（存储与噪音都太大），只做结构化摘要。
- 审计数据永久保留、不提供清理入口（30 人规模下数据量可控）。
