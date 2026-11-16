# Implementation Plan: 接口定义库 + 服务/模块层级

**Branch**: `002-api-library` | **Date**: 2026-11-16 | **Spec**: [spec.md](spec.md)

## Summary

在既有 `api_def` 表上增加 `module` 字段，补齐 CRUD + 按服务分组的树视图；编译器通过 `Input.APIs map` 把引用接口**内联展开**为 request 步骤；校验器放开 `step_type=api`（保留 `testcase` 的拒绝）；前端新增接口库页面并在用例编辑器支持「请求 / 引用接口」切换。

## Technical Context

- **编译器是纯函数包**：沿用 `Input.Datasets` 的先例，新增 `Input.APIs map[uint64]*model.APIDef`，由 service 层在 4 个装配点（case.go ×2、debug.go、exportsvc.go）一次性查好传入。
- **内联展开**而非引擎 `api/` 目录引用：导出产物自包含、可离线执行；`ref_api_id` 不泄漏进 YAML。
- **服务层级用字符串**：`api_def.module`（与 `test_case.module` 一致）；分组由 `GROUP BY` 得出，不建服务表。
- **引用计数 `used_by` 是派生值**（`GROUP BY ref_api_id` 查询），不落库——与项目「派生统计不落库」一致。
- **删除保护**：被引用时返回 `CodeInUse`（40003），与用例/环境的既有语义一致。
- **校验器**：`Input.APIs` 传入可解析集合；能解析 → 不再报「M2 未开放」；解析不到 → `CodeUnresolvedRef`。

## 改动清单（文件级）

| 层 | 文件 | 动作 |
|---|---|---|
| 模型 | `internal/model/model.go` | `APIDef` 加 `Module` |
| 服务 | `internal/service/apisvc.go` | 新增：CRUD/树/引用计数/loadAPIs |
| 服务 | `internal/service/service.go` | `Set.API` + `New` 接线 |
| 服务 | `internal/service/case.go` | 步骤保存校验 `ref_api_id`；2 处装配 APIs |
| 服务 | `internal/service/debug.go`、`exportsvc.go` | 装配 APIs |
| 编译 | `internal/compiler/compiler.go` | `Input.APIs`；`StepAPI` 分支内联展开 |
| 校验 | `internal/validator/validator.go` | `Input.APIs`；放开 api 步骤、保留 testcase 拒绝 |
| 接口 | `internal/api/handler_api.go`、`router.go` | 5 个端点注册 |
| 前端 | `web/src/api/apiDefs.ts`、`types.ts`、`router`、菜单、`ApiLibraryView.vue`、`StepEditor.vue`、`CaseEditorView.vue` | 接口库页 + 步骤类型切换 |
| 文档 | `docs/待开发功能清单.md`、`docs/项目方案.md`、`docs/完整路径验收清单.md` | F1 状态与裁剪口径 |

## Verification

单测：apisvc CRUD/重名/删除保护/跨项目；compiler 内联展开；validator 放行与拒绝。
端到端：建接口 → 用例引用 → 执行（真实 hrp）→ 改接口再执行生效。
门禁：`bash scripts/check.sh` 全绿 + CI。
