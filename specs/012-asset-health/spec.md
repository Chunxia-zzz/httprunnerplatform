# Feature Specification: 测试资产健康度

**Feature Branch**: `012-asset-health`

**Created**: 2026-11-18

**Status**: Draft

**Input**: 待开发功能清单 F12——帮测试主管发现「僵尸资产」：从未被引用的接口定义、长期没跑的用例、重复用例。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 一眼看到该清理什么 (Priority: P1)

「资产健康度」页给出评分与六类问题清单：从未执行的用例、超过 N 天未执行的用例、没有任何断言的用例、空壳用例（无可用步骤）、从未被引用的接口、重复用例（步骤与断言结构完全一致）。每条可点击跳到用例/接口详情。

**Independent Test**: 造 1 条从未执行的用例 + 1 条未被引用的接口 → 两个清单各出现 1 条，评分下降。

### User Story 2 - 阈值可配，结论可解释 (Priority: P1)

`days`（默认 30）控制「多久算僵尸」；每类问题给出**为什么算问题**的一句话说明（不是干巴巴的数字），并标明严重度（高/中/低）。

**Independent Test**: `days=1` 时昨晚跑过的用例被列为「长期未执行」；`days=365` 时不再列出。

### User Story 3 - 评分口径透明 (Priority: P2)

评分从 100 起扣分：高严重度每项扣 3 分、中每项 1.5 分、低每项 0.5 分，扣完为止（下限 0）。页面上直接展示扣分明细，避免"分数怎么来的"说不清。

**Independent Test**: 只有 1 条空壳用例（高严重度）→ 评分 97。

### Edge Cases

- 项目没有任何用例/接口：评分 100 且各清单为空（空项目不算不健康），文案说明「还没有资产」。
- 「从未执行」与「长期未执行」互斥：`last_run_id=0` 归前者，不占后者名额。
- 执行时间取 `run_record.created_at`（不是用例的 `updated_at`——改个名字不代表跑过）。
- 空壳用例：0 个启用步骤，或所有步骤都被禁用。
- 无断言用例：所有启用步骤的 `validate` 全为空（引用接口的步骤算同样规则）。
- 重复用例：按「启用步骤序列的方法/URL/引用接口 + 断言检查表达式」生成指纹，指纹相同且用例数 ≥2 才报；步骤数为 0 的不参与（空壳已单独归类）。
- 接口引用计数只看启用步骤（禁用步骤不引用）。
- 评分下限 0，不出现负分。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`HealthService` 只读聚合：用例（含 last_run_id）、步骤（enabled/seq/step_type/ref_api_id/request/validate）、接口、执行记录。
- **FR-2**：六类检测：`never_run` / `stale` / `no_assert` / `empty_case` / `unused_api` / `duplicate_case`；每类含 `severity`、`hint`（为什么算问题 + 怎么处理）。
- **FR-3**：`GET /api/v1/projects/{id}/health?days=` 返回 `{score, deductions[], summary{}, categories[], generated_at, applied{days}}`。
- **FR-4**：评分：100 - Σ(高 3 / 中 1.5 / 低 0.5)，下限 0；`deductions` 逐类给出扣分与原因。
- **FR-5**：每条 item 带可跳转的 `entity_type`（case/api）与 `entity_id`、名称/标识、以及一句具体说明（如「最后执行于 2026-09-01」）。
- **FR-6**：重复用例按指纹分组返回，组内列出全部用例（code/name/id）。
- **FR-7**：前端「资产健康度」页：评分卡片 + 扣分明细 + 天数输入 + 分类清单（可折叠/跳转）。
- **FR-8**：文档：契约章节、清单 F12 状态、README、验收边界表。

### Key Entities

- **HealthReport**：score、deductions、categories、applied。
- **HealthCategory**：key、label、severity、hint、count、items[]。
- **HealthItem**：entity_type、entity_id、key、name、detail。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：未被引用的接口会出现在 `unused_api`；创建引用它的用例后消失。
- **SC-2**：`days` 阈值生效（1 天 vs 365 天结论不同）。
- **SC-3**：空项目评分 100、各清单为空。
- **SC-4**：重复用例按指纹分组，组内 ≥2 条；改掉其中一条步骤后不再重复。
- **SC-5**：`scripts/check.sh` + CI 全绿。

## Assumptions

- 不做定时任务：健康度按需计算（数据量在 30 人团队可控），避免引入调度与缓存失效问题。
- 不自动删除任何资产：只给清单与建议，删除由人决定（自动删用例的风险远大于收益）。
- 「相似」判定用结构指纹（完全相同），不做语义相似度：后者误报率高，且难以解释。
