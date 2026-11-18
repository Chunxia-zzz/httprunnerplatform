# Tasks: 测试资产健康度

## Phase 1 — 服务

- [x] T1 六类检测的数据加载（用例/步骤/接口/执行记录）
- [x] T2 分类逻辑：never_run / stale / no_assert / empty_case / unused_api / duplicate_case
- [x] T3 评分与扣分明细（高 3 / 中 1.5 / 低 0.5，下限 0）

## Phase 2 — 接口

- [x] T4 `GET /projects/{id}/health?days=`（回显 applied）

## Phase 3 — 前端

- [x] T5 `api/health.ts` + 类型
- [x] T6 健康度页：评分卡、扣分明细、天数、分类清单与跳转
- [x] T7 路由 + 菜单

## Phase 4 — 测试与文档

- [x] T8 单测：六类各一例 + 互斥 + 指纹 + 评分下限 + 空项目
- [x] T9 端到端：接口未被引用→列出；加引用后消失；days 阈值生效
- [x] T10 契约 + 清单 + README + 边界表
- [x] T11 `scripts/check.sh` + CI
