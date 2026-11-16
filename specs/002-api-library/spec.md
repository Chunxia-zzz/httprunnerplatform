# Feature Specification: 接口定义库 + 服务/模块层级

**Feature Branch**: `002-api-library`

**Created**: 2026-11-16

**Status**: Draft

**Input**: 待开发功能清单 F1——建立「项目 → 服务（模块）→ 接口」三层结构，用例通过引用接口定义组装步骤。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 维护接口定义库并按服务分组浏览 (Priority: P1)

测试人员在「接口库」页面按服务（模块）分组浏览接口，新建/编辑/删除接口定义（方法、路径、请求头、查询参数、请求体、说明）。服务清单由接口的 `module` 字段聚合得出，不单独建表。

**Why this priority**: 这是数据层，后续引用、Mock、文档、OpenAPI 导入都建立在这之上。

**Independent Test**: 创建两个模块（如 `user` / `order`）下的若干接口，列表按服务分组显示；编辑后回显正确；同项目内 code 重复被拒绝。

### User Story 2 - 用例步骤引用接口定义，编译为内联请求 (Priority: P1)

用例编辑器里步骤类型可选「引用接口」，选择某个接口后步骤不再自带请求内容。编译时把接口定义**内联展开**为 `request` 步骤，产出的 YAML 自包含（可离线执行、可导出）。

**Why this priority**: 「引用」是接口库存在的全部意义——改接口一处、全部用例生效。

**Independent Test**: 建接口 + 建用例引用它 → 执行成功；修改接口路径后再次执行，新路径生效（用例未改）。

### User Story 3 - 引用关系的一致性与保护 (Priority: P1)

引用必须同项目、必须存在；被引用的接口不可直接删除（或删除前明确提示影响面）；引用失效时编译期给出可读错误，保存时即拦截。

**Why this priority**: 引用是跨实体的强约束，不做保护会出现「跑起来才发现」的坏数据。

**Independent Test**: 删除被引用接口 → 400 系列错误并提示引用数；构造悬空引用（DB 直改）→ 编译返回明确错误。

### User Story 4 - 从已有请求「提取为接口定义」（编辑器便捷入口）(Priority: P2)

在用例编辑器的请求步骤上提供「保存为接口」动作：把当前步骤的请求字段写入接口库（可指定模块），步骤自动切换为引用该接口。

**Why this priority**: 降低存量用例迁移成本；不做也能用（手动建接口再引用），所以 P2。

**Independent Test**: 对存量 request 步骤点「保存为接口」→ 接口库出现对应接口，步骤变为引用型且行为不变。

### Edge Cases

- `module` 为空：归入「未分类」分组展示，不允许作为服务名提交空串（由前端兜底 + 后端 trim 后为空则拒绝）。
- 跨项目引用：`ref_api_id` 指向其它项目的接口 → 保存/编译均拒绝。
- 删除被引用接口：返回 40003（CodeInUse）+ 引用用例数提示。
- 修改接口的 method/path/body 但被用例引用：所有引用用例下次执行自动生效（无需改动用例）。
- 存量 YAML（独立 request 步骤）导入/回显不受影响——引用是平台层概念，编译产物始终是 request 步骤。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`api_def` 增加 `module` 字段（size:64，默认 ''），与 `test_case.module` 的既有惯例一致；不新建服务表。
- **FR-2**：接口库 CRUD 端点：`GET/POST /projects/:id/apis`、`GET/PUT/DELETE /apis/:id`；列表支持按 module 过滤。
- **FR-3**：`GET /projects/:id/apis/tree` 返回按 module 聚合的分组（服务名 + 接口数组 + 数量），空 module 归入「未分类」。
- **FR-4**：接口 code 在项目内唯一（唯一索引或服务层校验二选一，取与用例一致的实现方式）；method 限定为支持的 HTTP 方法；path 必填且不允许包含空白。
- **FR-5**：用例步骤保存时：`step_type=api` 必须带 `ref_api_id`，且该接口存在、同项目、未被禁用；否则 40000。
- **FR-6**：编译器放开 `step_type=api`：按 `ref_api_id` 取出接口定义，内联展开为 `request` 步骤（method/url/headers/params/body 按 body_type 渲染）；引用不存在/跨项目 → 编译失败并给出可读错误。
- **FR-7**：删除接口时若被引用，返回 40003 并给出被引用用例数（`used_by` 计数由查询得出，不落库）。
- **FR-8**：接口视图包含 `used_by`（引用它的用例数）与 `module`，供前端展示与删除保护。
- **FR-9**：前端新增「接口库」页面（服务分组 + 接口列表 + 编辑表单），路由与菜单接入。
- **FR-10**：用例编辑器步骤支持在「请求 / 引用接口」之间切换（引用时选择接口、展示接口摘要、「解除引用」还原为独立请求）。
- **FR-11**（P2）：请求步骤提供「保存为接口」动作：写 api_def + 步骤切换为引用。
- **FR-12**：`docs/项目方案.md` 的裁剪说明与 `docs/待开发功能清单.md` 的 F1 状态同步更新；验收清单中相关「不是 bug」条目修正。

### Key Entities

- **APIDef**（`api_def`）：接口定义。新增 `module`；`used_by` 为派生字段（引用计数），不落库。
- **TestStep**：步骤。`step_type=api` 时以 `ref_api_id` 引用 APIDef，自身不再携带 request 内容（断言/提取/名称仍属步骤）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：接口库页面可按服务分组浏览 ≥ 2 个模块的接口，CRUD 全部可用。
- **SC-2**：引用接口的用例执行成功，编译产物 YAML 中该步骤为内联 `request`（`ref_api_id` 不泄漏进 YAML）。
- **SC-3**：修改接口定义后，未改用例的下一次执行使用新定义（端到端验证）。
- **SC-4**：删除被引用接口返回 40003 并带引用数；悬空引用编译报可读错误。
- **SC-5**：`bash scripts/check.sh` 全绿；新增服务/编译器/校验层单测覆盖上述分支。

## Assumptions

- 服务层级用 `api_def.module` 字符串表达（与 `test_case.module` 一致），**不新建服务表**——30 人团队的服务数在 10 个量级，字符串分组足够，且避免为「服务」引入独立 CRUD 生命周期。
- 引用在编译期**内联展开**而非使用引擎的 `api/` 目录引用：保证导出产物自包含（可离线执行），且不依赖引擎对 api 文件的查找约定。
- 步骤引用接口后，请求部分完全来自接口定义，步骤自身字段（headers 覆盖、局部改 body）MVP 不支持；需要时先「解除引用」。
- 工作区磁盘上的 `api/*.yaml` 本轮不生成（内联方案不需要）；`api_def` 是唯一真源。
