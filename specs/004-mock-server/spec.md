# Feature Specification: Mock Server（按接口定义自动生成响应）

**Feature Branch**: `004-mock-server`

**Created**: 2026-11-17

**Status**: Draft

**Input**: 待开发功能清单 F4——开启后，按接口库里的接口定义自动提供 mock 端点，供前端/上下游在被测服务未就绪时联调。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 开启项目的 Mock 并拿到可直接用的地址 (Priority: P1)

测试负责人在 Mock 页打开项目开关，页面给出「Mock 基址」`{平台地址}/mock/{项目code}` 与 curl 示例；接口库里的每个接口都自动有了 mock 端点，无需逐条配置。

**Why this priority**: 自动生成是 F4 的核心承诺——「按接口定义自动生成」，不做手工规则表。

**Independent Test**: 开启后 `curl {基址}/users` 返回 200 与接口定义里的示例 body。

### User Story 2 - 用路径模板匹配真实请求 (Priority: P1)

接口定义里的路径常带变量（`$base_url/users/{id}`、`$base_url/orders/:id`、`$base_url/users/$uid`）。请求 `/mock/{项目code}/users/123` 应命中 `users/{id}` 这条定义，并按定义返回示例体。

**Why this priority**: 真实联调时调用方传的是具体 id；只能精确匹配等于不可用。

**Independent Test**: 三条模板（`{}`/`:`/`$`）各自用具体值请求都命中并返回对应 body；不同的末段值不影响命中。

### User Story 3 - 命中日志与未命中提示 (Priority: P2)

Mock 页展示最近的命中记录（时间/方法/路径/命中的接口/状态码，内存保留最近 N 条，重启即失）；未命中时返回结构化 404，说明已配置多少条接口、请求路径是什么，便于排查。

**Why this priority**: 联调时"为什么没返回我要的数据"是最高频问题；没有日志就只能猜。

**Independent Test**: 连续请求 3 次后日志出现 3 条（倒序）；请求不存在的路径得到 404 与可读说明。

### Edge Cases

- 项目未开启 Mock：返回 404/403 明确说明「该项目未开启 Mock」，而不是空响应。
- 接口 `body_type=none` 或 body 为空：返回 200 空体 + `X-Mock-Source` 头，不伪造内容。
- `body_type=form`：按 JSON 返回（与平台表单语义一致），不做 URL 编码。
- 同一 method + 同一路径有多条定义：取 id 最小的一条（稳定可预期），日志中标注。
- 方法不匹配（定义了 GET，请求 POST）：视为未命中，404 说明「有该路径但方法不匹配」。
- 路径带 query：匹配时忽略 query。
- 路径大小写：按区分大小写匹配（与 HTTP 契约一致）。
- **安全判断**：mock 端点免登录——调用方是被测系统/前端，不是平台用户；返回内容只来自用户自己填写的接口定义，不触及库内其它数据。**不做**写操作（只接受 GET/POST/PUT/PATCH/DELETE 回放，但只读响应，不落库）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`project` 增加 `mock_enabled` 布尔列（默认 false；按项目约定不带 `default:` 标签，避免 GORM 静默写 true）。
- **FR-2**：`PUT /projects/{id}/mock` 开关；`GET /projects/{id}/mock` 返回 `{enabled, base_path, apis:[...], sample_curl}`。
- **FR-3**：公开 mock 端点 `ANY /mock/{projectCode}/*path`（免鉴权、独立前缀，不进 `/api/v1`）：
  按「方法 + 归一化路径」匹配接口定义，返回定义中的 body 与 200；未命中给结构化 404。
- **FR-4**：路径归一化：去掉定义里的协议/域名/`$base_url` 前缀与 query，只保留路径部分；请求侧去 query。
- **FR-5**：模板匹配：把 `{x}`、`:x`、`$x` 三种占位视为「单段通配」，其余段精确匹配（大小写敏感）。
- **FR-6**：命中日志：内存环形缓冲（按项目，最多 200 条），字段 = 时间/方法/路径/命中的接口 code/状态码；`GET /projects/{id}/mock/hits?limit=` 返回倒序。
- **FR-7**：前端「Mock 服务」页：开关、基址与 curl 示例（可复制）、可被 mock 的接口清单、命中日志（手动刷新）。
- **FR-8**：文档同步：接口契约新增 Mock 章节、待开发清单 F4 状态、README/验收边界表。

### Key Entities

- **Project.mock_enabled**：项目级开关。
- **MockHit**（不落库）：`at` / `method` / `path` / `api_code` / `status`。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：开启后 `curl {平台}/mock/{code}/users/123` 返回 200 且 body 等于接口定义的 body。
- **SC-2**：三种模板占位符各自的命中用例通过（单测覆盖）。
- **SC-3**：未开启时返回 404 且说明原因；未命中时 404 含已配置接口数量。
- **SC-4**：命中日志按倒序返回且上限生效（201 条请求后最多 200 条）。
- **SC-5**：`scripts/check.sh` 全绿；CI 全绿。

## Assumptions

- 不做手工 Mock 规则表（F4 目标是「按接口定义自动生成」）；需要特殊响应时先改接口定义或用调试台。
- 命中日志为**内存态**，重启即失；持久化留给 F11（性能回归）/F13（失败聚类）之后统一考虑。
- Mock 端点不支持鉴权、不支持写操作语义；仅按定义回放响应。
