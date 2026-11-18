# Implementation Plan: 失败聚类

**Branch**: `013-failure-clusters` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

`FailureClusterService` 读失败用例/步骤/断言，按四级特征聚类，生成标题与建议；前端「失败聚类」页。

## Technical Context

- 三次查询：失败 `case_result`（窗口/单次）→ 这些 run 的失败 `step_result` → 失败 `assertion_result`；
  在内存里按 case_result_id 关联（避免复杂 join 与跨驱动差异）。
- 状态码从 `step_result.response_snapshot` 的 `status_code` 读；缺失不猜。
- 路径展示复用 `normalizePerfPath`（同一口径，避免两套归一化）。
- 聚类键与标题集中在一个纯函数里（`clusterOf`），便于单测与后续调规则。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 服务 | `internal/service/failure.go` | 聚类与建议 |
| 接口 | `handler_failure.go`、`router.go` | GET /projects/:id/failures/clusters |
| 前端 | `api/failures.ts`、`views/FailuresView.vue`、router、menu | 页面 |
| 文档 | 契约 / 清单 / README / 边界表 | 同步 |

## Verification

单测：断言簇/HTTP 簇/步骤错误簇/归因簇、同因合并、一条用例只进一个簇、截断、空窗口。
端到端：造 3 条 502 + 1 条断言失败 → 两簇 3/1；窗口查询合并两次执行。
