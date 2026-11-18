# Feature Specification: 认证助手 + 动态变量

**Feature Branch**: `007-auth-dynamic`

**Created**: 2026-11-18

**Status**: Draft

**Input**: 待开发功能清单 F7——认证不必手写 header：环境级/步骤级选择认证类型，编译时自动注入；支持 `{{$timestamp}}`、`{{$uuid}}` 等动态变量。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 环境级认证：配一次，全部步骤自动带 (Priority: P1)

在环境里选认证类型（Bearer / Basic / API Key），填 token 或账号；该环境下所有用例请求自动带上对应请求头（或查询参数）。非管理员看到的是掩码值，改动时掩码不会覆盖真实值。

**Why this priority**: 认证是「每个请求都要带、内容完全相同」的东西，放在环境层最省事，也最不容易漏。

**Independent Test**: 环境配 Bearer `abc` → 任意用例执行，报文快照里的请求头含 `Authorization: Bearer abc`；非管理员查看环境看到掩码。

### User Story 2 - 步骤级覆盖：登录后的 token 自动传递 (Priority: P1)

某一步需要不同的认证（或需要 `$token` 这种运行时变量）。步骤上选「认证」：继承环境 / 无 / Bearer / Basic / API Key；Bearer 的值可以写 `$token`——引用上一步「提取」出来的变量，编译期自动生成 `Authorization: Bearer $token`。

**Why this priority**: 「登录拿 token → 后续接口用 token」是接口测试最普遍的链路；现在只能手写 header 字符串。

**Independent Test**: 用例第 1 步提取 token，第 2 步认证填 `$token` → 第 2 步报文头含 `Bearer $token`，且执行成功（变量被引擎替换）。

### User Story 3 - 动态变量：每次执行自动生成 (Priority: P2)

URL / 请求头 / 参数 / 请求体里可写 `{{$timestamp}}`（秒级时间戳）、`{{$timestampMs}}`（毫秒）、`{{$uuid}}`（随机 UUID）；编译期替换成具体值，相邻两次执行的值必然不同。

**Why this priority**: 幂等键、请求 ID、防重放时间戳都要它；没有就得手改或用 debugtalk。

**Independent Test**: 同一请求发两次，`{{$uuid}}` 解析结果格式为 UUID 且两次不同；`{{$timestamp}}` 为 10 位数字。

### Edge Cases

- 环境与步骤同时配置认证：**步骤优先**；步骤选「继承」时用环境配置。
- 认证头与手写请求头冲突：**手写优先**（用户显式写的赢，认证是最低优先层）。
- Basic 的账号/密码按字面量编码（base64 在编译期完成）；需要变量请改用自定义请求头。
- Bearer/API Key 的值可以是 `$var`（运行时变量），编译期原样写入，由引擎替换。
- 掩码：非管理员看到的 token/password/key_value 为 `***`；提交时回传掩码表示「不修改」。
- 动态变量出现在 JSON body 的字符串值里也要替换（递归处理）。
- 动态变量名与用户变量重名（如用户定义了 `$timestamp`）：动态替换只匹配 `{{$...}}` 形式，不受影响；校验器把四个动态名视为已定义。
- 认证类型为 none / 未配置：不注入任何头。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`AuthConfig` 结构（type / token / username / password / key_name / key_value / key_in），以 JSON 文本列存于 `environment.auth` 与 `test_step.auth`（零值可用，老库可增量迁移：`default:''`）。
- **FR-2**：环境请求/响应携带 `auth`；非管理员的 token/password/key_value 掩码为 `***`，更新时掩码值表示「保持原值」。
- **FR-3**：步骤请求/响应携带 `auth`；type 支持 `inherit`（默认）/`none`/`bearer`/`basic`/`api_key`。
- **FR-4**：编译器按「步骤 auth → 环境 auth」解析生效配置，注入为请求头/查询参数；**手写请求头优先级更高**。
- **FR-5**：注入规则：bearer → `Authorization: Bearer <值>`；basic → `Authorization: Basic base64(user:pass)`；api_key → `key_in=header` 时 `<key_name>: <key_value>`，`key_in=query` 时写入 params。
- **FR-6**：动态变量 `{{$timestamp}}` / `{{$timestampMs}}` / `{{$uuid}}` 在编译期替换（URL、请求头值、参数值、body 字符串，递归）。
- **FR-7**：校验器（后端）与编辑器提示（前端）把 timestamp/timestampMs/uuid 视为已定义变量，不报「未定义」。
- **FR-8**：前端环境表单增加「认证」区块；步骤编辑器增加「认证」页签（类型 + 条件字段 + `$token` 提示）。
- **FR-9**：文档同步：接口契约（环境/步骤的 auth 字段与优先级）、用户验收指南、待开发清单 F7、README。

### Key Entities

- **AuthConfig**：`type`（inherit/none/bearer/basic/api_key）+ 各类型对应字段。
- **动态变量**：编译期占位符，不落库、不进 YAML。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：环境配 Bearer 后，引用该环境的用例请求头出现 `Authorization: Bearer …`（报文快照可见）。
- **SC-2**：步骤认证 `$token` 的场景执行成功，且请求头为 `Bearer <真实 token>`。
- **SC-3**：`{{$uuid}}` 两次执行值不同且符合 UUID 形态；`{{$timestamp}}` 为 10 位数字。
- **SC-4**：非管理员看到掩码；回传掩码更新后原值不变。
- **SC-5**：`scripts/check.sh` + CI 全绿。

## Assumptions

- Basic 认证只支持字面量账号密码（不做变量插值）：需要变量时用 `api_key` 或手写 header。
- 动态变量在**编译期**展开（每次执行重新编译，因此每次执行值不同）；不引入运行时状态。
- 不做 OAuth2 授权码流程（需要浏览器交互与回调地址，超出当前范围；可作为未来方向）。
