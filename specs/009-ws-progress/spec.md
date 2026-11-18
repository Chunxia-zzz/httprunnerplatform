# Feature Specification: 执行进度推送（WebSocket）

**Feature Branch**: `009-ws-progress`

**Created**: 2026-11-18

**Status**: Done

**Input**: 待开发功能清单 F9——把执行详情的 1s 轮询改为推送，降低延迟与请求量；断线可降级。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 执行中实时看到进展 (Priority: P1)

点「执行」后打开详情页：状态与逐条用例结果随执行推进即时出现，页面显示「实时推送」，不再每秒发一次请求。

**Independent Test**: 跑一条用例，WS 收到 `status running` 与 `finished success` 两类事件；浏览器网络面板无 1s 轮询请求。

### User Story 2 - 断线自动降级并恢复 (Priority: P1)

网络抖动或代理不支持 Upgrade 时，页面自动切回 1s 轮询（显示「轮询中」），并每 5s 重连；重连成功后恢复实时推送。

**Independent Test**: 断开 WS → 页面仍持续更新且角标变为「轮询中」；恢复后角标回到「实时推送」。

### User Story 3 - 多标签页与权限 (Priority: P1)

同一次执行被多个标签页同时订阅，各自独立收到事件；非项目成员订阅返回 403。

**Independent Test**: 两个 WS 连接都收到事件；非成员连接得到 HTTP 403。

### Edge Cases

- 已结束的执行：连接即给 finished 快照并关闭（前端不必猜）。
- 执行在排队期间被取消：终态事件为 canceled，连接关闭。
- 慢客户端：服务端丢弃事件而不阻塞执行协程；前端靠"收到任意事件就刷新"最终一致。
- 编译失败 / 落库失败：同样推终态（error），避免页面永远转圈。
- 服务重启：连接断开 → 前端降级轮询；轮询接口本身会纠正"running 但进程已死"的状态。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：进程内 `EventHub`：按 run_id 订阅、广播、退订；订阅者缓冲区满时丢弃事件（不阻塞执行）。
- **FR-2**：`GET /api/v1/ws/runs/{id}`：会话 cookie 鉴权；校验执行所属项目的成员资格，非成员 **HTTP 403**。
- **FR-3**：连上先发当前状态快照；若已终态则发完即关闭。
- **FR-4**：事件类型：`status`（running）、`case`（用例集逐条，含跳过/编译失败）、`finished`（终态，发完关闭）。
- **FR-5**：发布点覆盖单用例与用例集的进入 running、逐条结果、终态；平台侧失败（编译/落库）也推送。
- **FR-6**：服务端每 25s ping；客户端断开能感知并清理订阅。
- **FR-7**：前端详情页：优先 WS，事件合并刷新（150ms 去抖）；断线降级 1s 轮询并每 5s 重连；角标展示当前模式。
- **FR-8**：开发代理开启 `ws: true`（否则 Vite 会拦掉握手）。
- **FR-9**：文档同步：契约、方案裁剪口径、验收清单边界表、待开发清单、README。

### Key Entities

- **RunEvent**：`run_id / type / status / total / case{code,name,status,seq,duration_ms} / at / finished`。

## Success Criteria *(mandatory)*

- **SC-1**：执行中无轮询即可实时更新（网络面板只有一条 WS 连接）。
- **SC-2**：断线后自动降级且数据仍更新；恢复后回到实时。
- **SC-3**：两个标签页同时订阅互不干扰。
- **SC-4**：非成员订阅 403；已结束执行连接即关闭。
- **SC-5**：`scripts/check.sh` + CI 全绿。

## Assumptions

- 单实例部署，因此用进程内广播；多实例需引入消息中间件（当前 30 人团队不必要）。
- 事件只带「变了什么」，不带全量结果：前端收到后拉一次详情，避免事件体膨胀。
- 不做步骤级（hrp 每一步 stdout 实时解析）推送：引擎已一次性产出结果，收益低于复杂度。
