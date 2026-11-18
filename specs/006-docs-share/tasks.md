# Tasks: 接口文档 + 只读分享

## Phase 1 — 数据与服务

- [x] T1 `ShareLink` 模型（token_hash 唯一、expires_at 可空）
- [x] T2 `ShareService`：Issue / List（掩码）/ Revoke / Verify（过期/吊销语义）
- [x] T3 `DocService`：构建文档（分组、请求/响应示例、关联用例、覆盖率）
- [x] T4 HTML 渲染（html/template、内联样式、无外部依赖）

## Phase 2 — 端点

- [x] T5 `GET /projects/:id/docs`、`GET /projects/:id/docs/html`
- [x] T6 `POST/GET /projects/:id/shares`、`DELETE /shares/:id`
- [x] T7 公开 `GET/HEAD /open/docs/:token`（json/html，410/404 页面）

## Phase 3 — 前端

- [x] T8 `api/docs.ts` + 类型
- [x] T9 `DocsView.vue`：预览（iframe）+ 覆盖率 + 分享管理
- [x] T10 路由 + 菜单

## Phase 4 — 测试与文档

- [x] T11 单测：文档构建/覆盖率/转义/token 生命周期
- [x] T12 端到端：分享 200 → 过期 410 → 吊销 404
- [x] T13 契约 + 清单 + README + 边界表
- [x] T14 `scripts/check.sh` + CI
