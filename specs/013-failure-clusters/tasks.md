# Tasks: 失败聚类

## Phase 1 — 服务

- [x] T1 加载失败用例/步骤/断言（单次执行与时间窗口两种范围）
- [x] T2 四级聚类键与标题（断言 → HTTP → 步骤错误 → 归因）
- [x] T3 建议文案、样本、截断与排序

## Phase 2 — 接口

- [x] T4 `GET /projects/{id}/failures/clusters`（run_id/days/limit，越权与不存在校验）

## Phase 3 — 前端

- [x] T5 `api/failures.ts` + 类型
- [x] T6 失败聚类页（范围切换、簇卡片、展开用例、跳转）
- [x] T7 路由 + 菜单

## Phase 4 — 测试与文档（含本批收尾）

- [x] T8 单测：四类簇 + 同因合并 + 唯一归属 + 截断 + 空窗口
- [x] T9 端到端：3×502 + 1×断言失败 → 3/1；窗口合并
- [x] T10 契约 + 清单 F13 ✅ + 收尾总结 + README + 边界表
- [x] T11 `scripts/check.sh` + CI
