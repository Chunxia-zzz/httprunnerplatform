# Implementation Plan: OpenAPI / Swagger 导入

**Branch**: `005-openapi-import` | **Date**: 2026-11-18 | **Spec**: [spec.md](spec.md)

## Summary

新增 `OpenAPIService`：解析 OpenAPI 3.x / Swagger 2.0 → `ImportedAPI` 列表（预览），提交时按策略写入 `api_def`；前端在接口库页加入导入对话框。

## Technical Context

- 解析：`gopkg.in/yaml.v3` 统一吃 JSON/YAML（`yaml.Unmarshal` 对 JSON 同样有效），转 `map[string]any`。
- `$ref`：本地 `#/components/schemas/X` 与 `#/definitions/X` 解析，深度上限 5 + 访问栈防环。
- 示例生成：`example`/`examples` → `default`/`enum[0]` → 类型占位（string→"string"，number→0，boolean→false，array→[0]，object→递归字段）。
- 路径：prefix（servers/host）拼 path；无 prefix 用 `$base_url`。
- 落库：复用 `APIService.Create/Update`？——提交需要 overwrite 语义且逐条报告，直接在 OpenAPIService 内用 DB 操作更直接；但需复用 `normalizeAPIReq` 校验（同包可访问）。
- 前端：`ApiLibraryView` 加按钮 + 导入对话框（上传 → 预览表格 → 策略 → 提交）。

## Files

| 层 | 文件 | 动作 |
|---|---|---|
| 服务 | `internal/service/openapisvc.go` | 解析/预览/提交 |
| 服务 | `internal/service/service.go` | `Set.OpenAPI` |
| 接口 | `internal/api/handler_openapi.go`、`router.go` | 两个端点 |
| 前端 | `web/src/api/openapi.ts`、`ApiLibraryView.vue` | 导入对话框 |
| 文档 | `docs/接口契约.md`、`docs/待开发功能清单.md`、README | 同步 |

## Verification

单测（表驱动 + 两份内联文档）：版本识别、prefix、tag→module、`{id}` 保留、$ref 解析与循环、示例生成、code 清洗去重、冲突标记、skip/overwrite。
端到端：导入真实风格文档 → 接口库分组 → Mock 命中返回示例。
