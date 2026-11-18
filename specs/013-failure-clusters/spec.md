# Feature Specification: 失败聚类

**Feature Branch**: `013-failure-clusters`

**Created**: 2026-11-18

**Status**: Done

**Input**: 待开发功能清单 F13——一次 200 条失败往往只有 3 个根因；按错误特征聚类，直接告诉人「80% 是同一个 502」。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 一次执行里把失败归成几簇 (Priority: P1)

跑完一个 50 条用例的用例集，30 条失败。打开失败聚类：显示 2 簇——「HTTP 502 · GET /orders」28 条、「断言失败：body.code eq 200」2 条。每簇可展开看具体用例。

**Independent Test**: 造 3 条同因失败（同一步骤同状态码）+ 1 条不同因失败 → 聚成 2 簇，计数 3/1。

### User Story 2 - 跨执行看最近 N 天的失败分布 (Priority: P2)

不指定 run_id 时，按时间窗口聚合项目的全部失败用例，用于判断「最近是不是同一个问题反复出现」。

**Independent Test**: 两次执行各失败 2 条同因 → 窗口查询得到 1 簇 4 条。

### User Story 3 - 每簇给出可执行的判断 (Priority: P1)

每簇除计数与用例列表外，给出**归因标签**与一句「先查什么」的建议（如 5xx 集中 → 先看服务健康；4xx 集中 → 查鉴权/参数/环境变量）。

**Independent Test**: 5xx 簇的 hint 指向服务端；`case_issue` 簇的 hint 指向变量/引用。

### Edge Cases

- 失败用例没有任何失败断言（如连接失败）：退化为按 HTTP 状态码 / 步骤错误前缀 / 归因聚类，不硬造断言簇。
- 同一用例有多条失败断言：取**第一条**（seq 最小）作为代表，避免一条用例出现在多个簇里。
- 用例被删：`case_id` 为 0 时仍以 `case_code` 展示，不丢样本。
- 同一簇超过 50 条用例：列表截断到 50 并标注总数（响应体不膨胀）。
- 窗口内无失败：返回空簇与 `total_failed_cases=0`。
- 状态码缺失（引擎没产出响应快照）：不猜测，聚类键退到错误信息/归因。
- `run_id` 不属于该项目：40000。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`FailureClusterService`：从 `case_result`（失败用例）+ `step_result`（失败步骤、响应状态码）+ `assertion_result`（失败断言）构建聚类。
- **FR-2**：聚类键优先级：失败断言（check/assert/expect）→ HTTP 状态码（≥400）→ 步骤错误前缀 → 归因。
- **FR-3**：标题与建议由服务端生成（与归因文案同一原则），每簇含 `attribution`。
- **FR-4**：`GET /api/v1/projects/{id}/failures/clusters?run_id=&days=30&limit=20`
  返回 `{scope, generated_at, total_failed_cases, clusters:[{key,title,attribution,hint,count,cases[],sample}]}`。
- **FR-5**：簇按 count 降序；每簇 cases 截断 50 条并给 `truncated` 标记。
- **FR-6**：前端「失败聚类」页：范围（最近一次失败执行 / 最近 N 天）、簇卡片（计数/归因/建议）、展开用例列表并可跳转。
- **FR-7**：文档：契约章节、清单 F13（本批收尾）、README、验收边界表。

### Key Entities

- **FailureCluster**：key、title、attribution、hint、count、cases[]、sample。
- **ClusterScope**：run_id 或 days。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：同因多条失败聚成一簇，计数准确；不同因不混簇。
- **SC-2**：窗口查询能合并多次执行的同因失败。
- **SC-3**：每簇有归因与建议；同因用例只出现在一个簇里。
- **SC-4**：空窗口/无失败时返回空结果而不是报错。
- **SC-5**：`scripts/check.sh` + CI 全绿。
