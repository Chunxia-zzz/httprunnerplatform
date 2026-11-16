# Tasks: 接口定义库 + 服务/模块层级

## Phase 1 — 后端数据层

- [x] T1 `model.APIDef` 加 `Module` 字段（size:64，default ''）
- [x] T2 `service/apisvc.go`：`APIReq/APIView`、normalize（code/method/path/module 校验）、Create/Update/List/Get/Delete、`Tree`（GROUP BY module，空归「未分类」）、`UsedBy` 派生计数、`loadAPIs`
- [x] T3 `service/service.go`：`Set.API` + `New` 接线
- [x] T4 `handler_api.go` + `router.go`：5 个端点
- [x] T5 用例步骤保存校验 `ref_api_id`（存在/同项目）

## Phase 2 — 编译与校验

- [x] T6 `compiler.Input.APIs`；`StepAPI` 内联展开（method/url/headers/params/body by body_type）
- [x] T7 4 个装配点传入 APIs（case ×2 / debug / export）
- [x] T8 `validator.Input.APIs`：api 步骤可解析即放行；不可解析报 UnresolvedRef；testcase 仍拒绝

## Phase 3 — 前端

- [x] T9 `web/src/api/apiDefs.ts` + types（APIDef/APIGroup/APIReq）
- [x] T10 `ApiLibraryView.vue`（服务分组树 + 列表 + 编辑表单 + 删除保护提示）+ 路由 + 菜单
- [x] T11 `StepEditor.vue`/`CaseEditorView.vue`：步骤类型「请求 / 引用接口」切换、接口选择、解除引用

## Phase 4 — 测试与验证

- [x] T12 `apisvc_test.go`：CRUD/重名/删除保护/跨项目/树分组
- [x] T13 `compiler_test.go`：api 步骤内联（含 body_type、headers 合并）
- [x] T14 `validator_test.go`：api 步骤放行/悬空引用报错（更新既有「M2 未开放」断言）
- [x] T15 端到端：真实 hrp 执行引用型用例 + 改接口生效
- [x] T16 `bash scripts/check.sh` 全绿；文档同步（清单 F1、方案裁剪口径、验收清单边界表）
