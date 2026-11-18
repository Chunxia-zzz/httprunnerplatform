# Tasks: OpenAPI / Swagger 导入

## Phase 1 — 解析器

- [x] T1 `openapisvc.go`：版本识别（openapi 3.x / swagger 2.0）+ 统一成内部模型
- [x] T2 prefix 推导（servers / host+basePath / `$base_url`）
- [x] T3 module（tags[0] / 路径首段 / 未分类）、code 清洗与去重
- [x] T4 示例生成：example/default/enum/类型占位 + `$ref`（深度 5、防环）
- [x] T5 请求体类型映射（json/form/raw/none）与响应示例 → body

## Phase 2 — 预览与提交

- [x] T6 `Preview(projectID, content)`：冲突标记、告警收集、500 条上限
- [x] T7 `Commit(projectID, apis, policy)`：skip/overwrite 逐条结果
- [x] T8 handler + 两个端点（multipart preview / JSON commit）

## Phase 3 — 前端

- [x] T9 `api/openapi.ts` + 类型
- [x] T10 接口库页「导入 OpenAPI」对话框：上传、预览勾选、冲突标记、策略、结果

## Phase 4 — 测试与文档

- [x] T11 单测：两份内联文档（OAS3 + Swagger2）+ 循环 $ref + code 去重 + 冲突
- [x] T12 端到端：导入 → 分组 → Mock 命中示例
- [x] T13 契约 + 功能清单 + README
- [x] T14 `scripts/check.sh` + CI
