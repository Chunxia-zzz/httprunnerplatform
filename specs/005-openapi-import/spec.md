# Feature Specification: OpenAPI / Swagger 导入

**Feature Branch**: `005-openapi-import`

**Created**: 2026-11-18

**Status**: Draft

**Input**: 待开发功能清单 F5——上传 OpenAPI 3.x / Swagger 2.0 文档，生成服务分组与接口定义，导入后 Mock 立即可用。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 上传文档预览将创建的接口 (Priority: P1)

测试人员点「导入 OpenAPI」，上传 `.json` / `.yaml` 文件，看到：识别出的版本（openapi 3.x / swagger 2.0）、从 `servers`/`host+basePath` 得到的地址前缀、按服务（tag）分组的接口清单（方法、路径、名称、是否与现有标识冲突）、以及若干告警（不支持的字段）。

**Why this priority**: 导入前必须能看清「将要发生什么」，否则一次点下去就污染接口库。

**Independent Test**: 上传含 2 个 tag、3 个路径的 OpenAPI 3 文档，预览列出 3 条接口并按 tag 分组；再上传同标识的文档，冲突被标出。

### User Story 2 - 提交后接口库可用，且立即能被 Mock 使用 (Priority: P1)

确认后提交：接口写入 `api_def`（module=tag，path=前缀+路径，示例请求体来自 schema/example，**响应示例写入 body**）。打开 Mock 页即可按这些接口返回示例数据。

**Why this priority**: 「导入即生成服务分组 + 接口定义」是 F5 的验收口径；接上 F4 才有联调价值。

**Independent Test**: 导入后接口库按 tag 分组可见；Mock 端点命中返回文档里的响应示例。

### User Story 3 - 冲突策略与部分失败可见 (Priority: P2)

同一标识已存在时可选「跳过 / 覆盖」；提交结果逐条报告（created / skipped / overwritten / failed 及原因），不做整体回滚。

**Why this priority**: 真实文档会反复导入，没有冲突策略就没法迭代。

**Independent Test**: 同一文档导入两次，第二次 skip 策略下全部 skipped；overwrite 策略下全部 overwritten 且内容更新。

### Edge Cases

- Swagger 2.0 与 OpenAPI 3.x 结构差异：`host + basePath + schemes` vs `servers[].url`；`parameters(in: body)` vs `requestBody`；`definitions` vs `components.schemas`。
- `$ref` 循环引用 / 深层嵌套：解析深度上限（如 5 层），超过则给 `{}` 并告警，不抛异常。
- 路径参数 `{id}`：原样保留（与平台模板匹配、Mock 通配一致）。
- 无可识别版本：返回 40000 并说明「既不是 OpenAPI 3.x 也不是 Swagger 2.0」。
- 无 tags：module 取路径首段；根路径 `/` 归为未分类。
- `operationId` 重复或含非法字符：清洗为合法标识（字母/数字/下划线/连字符），重复加 `_2` 后缀。
- 文档很大（数百接口）：预览上限 500 条，超出给告警并截断（避免响应过大）。
- 二进制/YAML 缩进错误：解析失败给明确错误（含行号，来自 yaml.v3 的错误信息）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：`POST /api/v1/projects/{id}/import/openapi/preview`（multipart `file`）→ `{detected, servers, apis[], warnings[]}`；每条含 `code/name/module/method/path/body/body_type/description/conflict`。
- **FR-2**：解析器支持 OpenAPI 3.x 与 Swagger 2.0（JSON 与 YAML 均可），自动识别版本。
- **FR-3**：路径前缀：OpenAPI 取 `servers[0].url`；Swagger 取 `schemes[0]://host+basePath`；缺失时用 `$base_url`。
- **FR-4**：`module` = `tags[0]`，否则路径首段，否则空（未分类）。
- **FR-5**：示例体优先级：`example`/`examples` → schema 中 `example`/`default`/`enum[0]` → 按类型生成的占位值；`$ref` 解析深度上限 5、循环引用给 `{}`。
- **FR-6**：⭐ 语义拆分：`api_def.body` 是**请求体**（编译器内联用），新增 `response_body` 存**响应示例**（Mock 用）。导入时请求体写 `body`（按 content-type 映射 json/form/raw/none），响应示例写 `response_body`（按响应 content-type 映射 json/raw）——这样「引用接口的用例」拿到请求体，「Mock」拿到响应体，两者不再互相污染。
- **FR-7**：`code` 清洗为合法标识并去重（`_2`）；冲突相对**已存在的接口定义**标记。
- **FR-8**：`POST /projects/{id}/import/openapi/commit`（JSON：`{apis:[...], policy:"skip"|"overwrite"}`）→ 逐条结果 `{created, skipped, overwritten, failed:[{code, reason}]}`。
- **FR-9**：前端在「接口库」页提供「导入 OpenAPI」：上传、预览表（可勾选/取消）、冲突标记、策略选择、提交后刷新。
- **FR-10**：文档同步：接口契约新增章节、待开发清单 F5 状态、README 能力项。

### Key Entities

- **ImportedAPI**（预览项）：`code/name/module/method/path/headers/body/body_type/response_body/response_type/description/conflict`。
- **api_def**（落库）：F1 结构 + `response_body`/`response_type`（F5 新增），额外打 `source="openapi"`。

### 兼容说明（对既有行为的影响）

- F4 的 Mock 改为：**优先** `response_body`，为空时回退 `body`（F1 手工建的接口没有响应示例，行为与之前完全一致）。
- F1 的编译器只用 `body`（请求体），不受影响；接口库编辑表单增加「响应示例」字段（可留空）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：一份真实 OpenAPI 3 文档导入后，接口库按 tag 分组齐全，路径参数保留为 `{id}`。
- **SC-2**：导入的接口在 Mock 端点命中并返回文档中的响应示例。
- **SC-3**：同一文档二次导入：skip 策略 0 新增；overwrite 策略全部覆盖且 `updated_at` 变化。
- **SC-4**：`$ref` 循环引用不导致超时/崩溃（单测覆盖）。
- **SC-5**：`scripts/check.sh` 全绿 + CI 全绿。

## Assumptions

- 只导入**接口定义**，不自动生成用例（用例组装需要断言意图，机器猜不出；用户可用调试台/F1 引用快速组装）。
- 不支持 OpenAPI 的 callbacks / links / security schemes（导入时忽略并在告警里列出）。
- 预览与提交是两段式：提交的 JSON 就是预览结果的子集，用户勾选即裁剪。
