# Tasks: 请求调试台

## Phase 1 — 后端

- [x] T1 重构 `debug.go`：抽 `runSyntheticCase(ctx, project, env, tc, steps, timeoutSec, targetSeq)`，`DebugStep` 改用它（行为不变，既有测试必须仍绿）
- [x] T2 `DebugRequest`：校验（project/url/body_type）、合成单步骤用例、调用 T1
- [x] T3 `handler_debug.go` 新增 `Request` + 路由 `POST /projects/:id/debug/request`

## Phase 2 — 前端

- [x] T4 `types.ts`：`DebugRequestReq`；`api/debug.ts`：`request()`
- [x] T5 `DebugConsoleView.vue`：环境选择 + 请求构造 + 响应面板 + 存为接口/用例弹窗
- [x] T6 路由 + 菜单接入

## Phase 3 — 测试与文档

- [x] T7 单测：ad-hoc 参数校验分支
- [x] T8 端到端（真实 hrp）：发送 200 → 存接口 → 存用例
- [x] T9 接口契约 3B 节 + 待开发清单 F3 状态
- [x] T10 `scripts/check.sh` 全绿 + CI
