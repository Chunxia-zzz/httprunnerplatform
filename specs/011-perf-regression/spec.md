# Feature Specification: 性能回归告警

**Feature Branch**: `011-perf-regression`

**Created**: 2026-11-18

**Status**: Draft

**Input**: 待开发功能清单 F11——按接口聚合耗时，与最近 N 次中位数基线对比，超过阈值标红；通知出口依赖 F2（暂缓），本期做**站内告警**。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 一眼看出哪个接口变慢了 (Priority: P1)

打开「性能回归」页：按接口列出最近一次耗时、基线中位数、变化百分比与结论（回归 / 稳定 / 变快 / 样本不足）。超过阈值的行标红，可按结论筛选。

**Independent Test**: 同一接口先跑 3 次 100ms、再跑 1 次 300ms → 该行标红，`delta_pct ≈ 200%`，结论 `regressed`。

### User Story 2 - 每次执行后看本次回归 (Priority: P1)

执行详情页给出「本次执行有 N 个接口回归」入口，点进去只显示该次执行涉及的接口对比结果，便于判断「这次改动是不是变慢了」。

**Independent Test**: 指定 `run_id` 查询，返回结果只包含该次执行里出现过的接口。

### User Story 3 - 阈值与误报可控 (Priority: P2)

阈值可调：相对百分比（默认 30%）、绝对毫秒下限（默认 50ms，避免 2ms→3ms 判为回归）、基线样本数（默认 3，样本不足不下结论）。参数通过查询参数传入，页面记住上次选择。

**Independent Test**: 100ms → 120ms（+20%，未达 30%）不报；100ms → 103ms（绝对差 3ms，未达 50ms）不报；样本 2 次时结论为「样本不足」。

### Edge Cases

- **接口聚合口径**：`METHOD + 归一化路径`（去掉 query；纯数字段与 UUID 归一为 `:id`）。方法取自报文快照；快照缺失时方法记为空（显示 `—`），不猜测。
- 同一路径不同方法**分开统计**（GET/POST 耗时不具可比性）。
- 基线取「该接口在本项目最近 N 次执行（不含本次）的平均耗时」的中位数；同一执行内重复调用该接口取平均。
- 失败执行的步骤也计入（失败往往最慢）；但 `elapsed_ms = 0` 的步骤（如 ws close、平台侧失败）不计入，避免把 0 拉进基线。
- 接口在某次执行里没出现：该次不贡献样本（不是 0）。
- 全项目无数据：空态说明，不显示假结论。
- 阈值参数非法（负数/过大）：夹到安全范围并在响应里回显生效值。
- **通知**：F2 暂缓，本期不发送任何外部通知；站内标红 + 执行详情入口即为「告警」。文档明确标注，不宣称已对接通知。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`PerfService`：从 `step_result`（join `run_record`）按项目+时间窗口取样本，按接口聚合每次执行的平均耗时。
- **FR-2**：`normalizePerfPath`：去协议/host/query/trailing slash；纯数字段、UUID 段归一为 `:id`。
- **FR-3**：基线 = 最近 `baseline_runs` 次（不含最新一次）平均耗时的中位数；样本数 `< min_samples` 时结论为 `insufficient`。
- **FR-4**：判定：
  - `regressed`：`delta_pct >= threshold_pct` **且** `delta_ms >= min_delta_ms`；
  - `improved`：`delta_pct <= -threshold_pct` 且绝对差达标；
  - 其余 `stable`；无基线 `no_baseline`。
- **FR-5**：`GET /api/v1/projects/{id}/perf` 参数：`run_id`（可选，只看该次执行的接口）、`days`（默认 30）、`baseline_runs`（默认 10）、`threshold_pct`（默认 30）、`min_delta_ms`（默认 50）、`min_samples`（默认 3）、`verdict`（筛选）。返回 `{items[], summary, applied{...}}`。
- **FR-6**：每条 item 含：`method/path/endpoint`、`latest_run_id/latest_at/latest_ms`、`baseline_ms`、`samples`、`delta_ms/delta_pct`、`verdict`、`trend[]`（最近最多 10 个点的 run_id/时间/耗时）。
- **FR-7**：前端「性能回归」页：阈值/窗口控件、结论筛选、表格（当前/基线/变化/样本/趋势迷你条）、回归行标红；执行详情页头部给出「本次 N 个接口回归」链接（N=0 时不显示红点）。
- **FR-8**：文档：契约新增章节、清单 F11 状态（注明通知未接入）、README、验收边界表。

### Key Entities

- **PerfItem**（实时计算，不落库）：接口标识、最新耗时、基线、样本数、变化、结论、趋势点。
- **PerfSample**：单次执行内某接口的平均耗时（run_id + at + ms）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：构造「3 次快 + 1 次慢」后，慢行标红且 `verdict=regressed`。
- **SC-2**：波动在阈值内不报（+20% 且绝对差达标仍不报）。
- **SC-3**：`run_id` 过滤只返回该次执行的接口；样本不足时给 `insufficient` 而不是假结论。
- **SC-4**：空项目返回空列表且 `summary` 全 0。
- **SC-5**：`scripts/check.sh` + CI 全绿。
