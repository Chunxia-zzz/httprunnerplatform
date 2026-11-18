# Tasks: 性能回归告警

## Phase 1 — 服务

- [x] T1 `normalizePerfPath`（去 query/数字/UUID → `:id`）
- [x] T2 样本聚合：窗口内 step_result → 每次执行每接口平均耗时
- [x] T3 基线与判定（中位数、样本不足、阈值 + 绝对下限）

## Phase 2 — 接口

- [x] T4 `GET /projects/{id}/perf`（run_id/days/阈值/筛选 + applied 回显）

## Phase 3 — 前端

- [x] T5 `api/perf.ts` + 类型
- [x] T6 性能回归页（控件/筛选/表格/趋势迷你条/标红）
- [x] T7 执行详情「本次 N 个接口回归」入口 + 路由菜单

## Phase 4 — 测试与文档

- [x] T8 单测：归一化/中位数/判定矩阵/聚合与过滤
- [x] T9 端到端：3 快 + 1 慢 → regressed；阈值内不报
- [x] T10 契约 + 清单（注明通知未接入）+ README + 边界表
- [x] T11 `scripts/check.sh` + CI
