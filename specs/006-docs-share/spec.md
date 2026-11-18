# Feature Specification: 接口文档自动生成 + 只读分享链接

**Feature Branch**: `006-docs-share`

**Created**: 2026-11-18

**Status**: Draft

**Input**: 待开发功能清单 F6——从接口库与用例反推文档，生成只读分享链接给非平台用户看；可导出静态 HTML。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 在平台内预览接口文档 (Priority: P1)

打开「接口文档」页，看到按服务分组的接口：方法、路径、说明、请求头/参数/请求体、响应示例，以及**每个接口被哪些用例覆盖**（用例名 + 最近执行状态）；顶部给出覆盖率（被引用的接口数 / 总接口数）。

**Why this priority**: 文档的第一用途是团队自查「哪些接口测了、哪些没测」。

**Independent Test**: 接口库有 2 个服务、3 个接口，其中 1 个被用例引用；文档页按服务分组显示 3 条，覆盖率 1/3。

### User Story 2 - 生成只读分享链接给外部 (Priority: P1)

点「生成分享链接」，可选有效期（7/30/90 天/永久）与备注名；生成后得到 `{平台地址}/open/docs/{token}`。未登录的浏览器打开可看到同一份文档（HTML），**没有任何写操作入口**。

**Why this priority**: 「发给非平台用户」是 F6 的验收口径；领导/上下游不会为了看文档开账号。

**Independent Test**: 无痕窗口打开分享链接能看文档；链接里的接口没有编辑按钮；调用非 GET 请求返回 405。

### User Story 3 - 链接管理与失效 (Priority: P2)

分享页列出已生成的链接（备注、创建时间、过期时间、状态），可复制/打开/删除（吊销）；过期或吊销后访问返回明确提示页（不是空白或 500）。

**Why this priority**: 链接一旦泄漏需要能立即作废。

**Independent Test**: 生成 1 天有效期链接 + 人为过期 → 访问提示「已过期」；删除后提示「已失效」。

## Edge Cases

- 项目没有任何接口：文档页显示空态说明，分享链接生成按钮禁用。
- 接口被多个用例引用：全部列出（按用例 code 排序）。
- 分享 token 泄露风险：token 只存 sha256，明文仅在创建时返回一次；列表只显示前缀。
- 过期判定使用服务器时间；`expires_at` 为空 = 永久有效。
- 已吊销（删除）与不存在：对未登录者给同样的「链接无效或已失效」提示，避免探测。
- HTML 里的用户内容（接口名、说明、body）必须转义——用 `html/template`，禁止字符串拼接。
- 文档内容实时生成（每次请求现算），不做快照：接口改了文档立即跟上。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`share_link` 表：`project_id` / `name` / `token_hash`（sha256 hex，唯一）/ `expires_at`（可空）/ `created_by`。
- **FR-2**：`GET /api/v1/projects/{id}/docs` 返回文档 JSON（分组 + 接口 + 关联用例 + 覆盖率）。
- **FR-3**：`GET /api/v1/projects/{id}/docs/html` 返回同一份文档的静态 HTML（登录态可预览/下载）。
- **FR-4**：`POST /api/v1/projects/{id}/shares` `{name, expires_in_days}`（0 = 永久）→ 返回明文 token 与完整 URL（仅此一次）。
- **FR-5**：`GET /api/v1/projects/{id}/shares` 列表（token 只显示前 8 位 + `…`）；`DELETE /api/v1/shares/{id}` 吊销。
- **FR-6**：公开端点 `GET /open/docs/{token}`：有效 → HTML；过期 → 410 页面；不存在/已吊销 → 404 页面；`?format=json` 返回 JSON。
- **FR-7**：公开端点**只注册 GET/HEAD**，同一路径的其它方法由路由返回 405（不可写）。
- **FR-8**：HTML 使用 `html/template` 渲染，自动转义全部用户内容；单文件、无外部 JS/CSS 依赖、打印友好。
- **FR-9**：前端「接口文档」页：文档预览（分组锚点导航）、覆盖率、分享链接管理（生成/复制/打开/删除）。
- **FR-10**：文档同步：接口契约新增章节、待开发清单 F6 状态、README。

### Key Entities

- **ShareLink**（落库）：项目、备注、token 哈希、过期时间、创建人。
- **APIDoc / DocGroup / DocAPI / DocCase**（实时计算，不落库）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：文档页分组、请求/响应示例、关联用例与覆盖率均与接口库一致。
- **SC-2**：分享链接在未登录浏览器可读，页面无写操作入口；非 GET 方法被拒。
- **SC-3**：过期链接返回 410 提示页，吊销链接返回 404 提示页，页面文案可读。
- **SC-4**：接口名里含 `<script>` 时，HTML 输出被转义（单测覆盖）。
- **SC-5**：`scripts/check.sh` + CI 全绿。

## Assumptions

- 分享链接是**项目级只读视图**，不做单接口级分享（30 人团队用不到，且粒度越细越难管理）。
- 文档实时生成，不做版本快照（快照与「改一处全部生效」的原则冲突，也增加存储）。
- 不在文档中展示环境的 `base_url` 具体值（避免泄漏内网地址），只展示接口路径。
