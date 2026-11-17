# Feature Specification: 请求调试台（临时发请求 → 存为接口/用例）

**Feature Branch**: `003-adhoc-console`

**Created**: 2026-11-17

**Status**: Draft

**Input**: 待开发功能清单 F3——不建用例也能发一个请求看响应，满意后一键沉淀为接口定义或用例。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 随手发一个请求并看到响应 (Priority: P1)

测试人员在「调试台」选环境、选方法、填 URL（支持 `$base_url` 与变量）、可选填头/参/体，点「发送」后同步看到：状态码、耗时、最终 URL、响应头、响应体。**不产生任何执行记录**。

**Why this priority**: 这是 Postman 的最高频场景；现在必须「先建用例 → 再执行」才能发一个请求，录入税太重。

**Independent Test**: 对任意可达服务发 GET，面板展示 200 与响应体；执行记录列表不新增条目。

### User Story 2 - 一键存为接口定义 / 用例 (Priority: P1)

响应满意后，两个出口：
- **存为接口**：填服务模块 + 标识 + 名称 → 写入接口库（F1）；已存在同名标识时提示冲突。
- **存为用例**：填用例标识 + 名称 → 创建单步骤用例并跳转到编辑器（用例可继续加断言）。

**Why this priority**: 「调试 → 沉淀」闭环是调试台区别于裸 curl 的价值；否则用户还是要复制粘贴。

**Independent Test**: 发送后点「存为接口」，接口库出现对应条目；点「存为用例」，跳转编辑器且请求内容一致。

### User Story 3 - 发送前的即时校验 (Priority: P2)

URL 为空、方法非法时直接在前端拦截；`$base_url` 在所选环境缺失时给出明确提示（而不是发出去后失败）。

**Why this priority**: 减少「发出去才报错」的往返，但不影响主流程可用。

**Independent Test**: 空 URL 点发送被拦；环境无 base_url 时提示文案准确。

### Edge Cases

- 目标服务不可达 / 超时：返回 `error` 状态与可读错误（复用执行器的超时与错误摘要），面板展示原始日志。
- 响应不是 UTF-8：按原始字节截断展示，不崩溃（parser 已有兜底）。
- 请求体 JSON 非法：前端发前拦截。
- 保存为用例时标识重复：后端 40001，前端提示。
- **安全判断**：平台本就是内网测试工具，用例本来就能请求任意 URL；调试台不引入新的网络面，因此不做目标白名单（记录在 Assumptions，便于审计时对齐口径）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：新增 `POST /api/v1/projects/{id}/debug/request`，请求体 `{env_id, timeout_sec, request:{method,url,headers,params,body,body_type}}`，同步返回与单步调试**同构**的 `DebugStepResult`。
- **FR-2**：ad-hoc 调试**不落库**：不产生 `run_record` / `case_result`，临时工作区跑完即弃（与 `DebugStep` 同语义）。
- **FR-3**：复用单步调试的执行管线——把「构造临时用例 → 编译 → 临时工作区 → 执行 → 解析」抽成内部函数，`DebugStep` 与 ad-hoc 共用，避免两套实现漂移。
- **FR-4**：ad-hoc 请求支持环境变量替换（`$base_url`、环境变量）——由编译器按现有规则渲染，不另造替换逻辑。
- **FR-5**：保存出口复用已有端点：接口 → `POST /projects/{id}/apis`（F1）；用例 → `POST /projects/{id}/cases`（单步骤）。**不新增保存端点**（避免职责重叠）。
- **FR-6**：前端新增「调试台」页：环境选择、请求构造（方法/URL/头/参数/体）、发送、响应面板（状态/耗时/最终 URL/头/体）、两个保存入口；路由与菜单接入。
- **FR-7**：前端类型与 API 封装（`debugApi.request`）入库；发送中禁用按钮防重复提交。
- **FR-8**：文档同步：接口契约新增调试台端点、待开发清单 F3 状态、用户验收指南/README 导航补一句。

### Key Entities

- **DebugStepResult**（复用）：`status` / `duration_ms` / `steps[0].request|response|final_url|elapsed_ms` / `error_msg` / `compile_error`。
- 临时用例（不落库）：`code=adhoc`、`name=临时请求`、单步骤 `step_type=request`。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：调试台发一个 GET 请求，面板 1~3 秒内展示状态码与响应体（本地服务）。
- **SC-2**：「存为接口」后接口库列表立即出现该条目（同模块分组）。
- **SC-3**：「存为用例」后跳转编辑器，请求内容与调试台一致，可直接补断言保存。
- **SC-4**：调试不新增执行记录（`GET /runs` 总数不变）。
- **SC-5**：`bash scripts/check.sh` 全绿；新增校验分支有单测；真实 hrp 端到端验证一次。

## Assumptions

- 复用 `DebugStep` 的临时用例机制：hrp 最小执行单位是「一个用例文件」，ad-hoc 请求同样走「合成单步骤用例」路径（实测 A2），不做引擎层 bypass。
- 不做「历史记录」：ad-hoc 请求不落库，刷新即失（与 DebugStep 一致）；需要留存的走「存为用例」。
- 不做目标白名单（见 Edge Cases 的安全判断）。
