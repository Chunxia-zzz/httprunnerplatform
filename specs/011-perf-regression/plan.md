# Implementation Plan: 性能回归告警

**Branch**: `011-perf-regression` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

新增 `PerfService`（只读聚合）+ `GET /projects/{id}/perf` + 前端「性能回归」页与执行详情入口。通知依赖 F2，本期仅站内告警。

## Technical Context

- 数据：`step_result`（run_id、final_url、request_snapshot、elapsed_ms）join `run_record`（project、status、created_at）。
- 方法提取：解析 `request_snapshot` JSON 的 `method` 字段；缺失记空。
- 一次性拉窗口内样本进内存聚合（与 StatsService 同思路）：时间窗口 + 项目过滤后数据量可控，且 mysql/sqlite 行为一致。
- 中位数：排序后取中位（偶数取中间两个均值，四舍五入）。
- 阈值参数做范围夹取（days 1..365、baseline_runs 1..100、threshold 1..1000、min_delta 0..60000、min_samples 1..50）。

## Files

| 层 | 文件 | Actions |
|---|---|---|
| 服务 | `internal/service/perf.go` | 聚合/归一化/判定 |
| 接口 | `handler_perf.go`、`router.go` | GET /projects/:id/perf |
| 前端 | `api/perf.ts`、`views/PerfView.vue`、`RunDetailView.vue`、router、menu | 页面与入口 |
| 文档 | 契约 / 清单 / README / 边界表 | 同步 |

## Verification

单测：路径归一化（数字/UUID/query/尾斜杠）、中位数、判定矩阵（回归/稳定/变快/样本不足/无基线）、服务聚合与 run_id 过滤。
端到端：桩服务支持延迟参数 → 3 次快 + 1 次慢 → 接口标红；阈值内不报。
