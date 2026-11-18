# Implementation Plan: 测试资产健康度

**Branch**: `012-asset-health` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

新增 `HealthService`（只读聚合六类问题）+ `GET /projects/{id}/health` + 前端健康度页。

## Technical Context

- 一次读全量用例/步骤/接口/执行记录（项目维度，30 人规模可控），在内存里分类。
- 步骤指纹：`step_type|ref_api_id 或 method url|断言 check 列表`，SHA-1 前 12 位。
- 执行时间 join `run_record.created_at`；`last_run_id=0` 归 `never_run`。
- 评分扣分：高 3 / 中 1.5 / 低 0.5，下限 0；`deductions` 表达完整。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 服务 | `internal/service/health.go` | 六类检测 + 评分 |
| 接口 | `handler_health.go`、`router.go` | GET /projects/:id/health |
| 前端 | `api/health.ts`、`views/HealthView.vue`、router、menu | 页面 |
| 文档 | 契约 / 清单 / README / 边界表 | 同步 |

## Verification

单测：六类检测各一例、互斥（never_run vs stale）、指纹去重、评分下限、空项目。
端到端：造未被引用接口 + 从未执行用例 + 无断言用例 → 清单命中；补引用后消失。
