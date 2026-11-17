# Tasks: Mock Server

## Phase 1 — 后端

- [x] T1 `Project.MockEnabled`（布尔列不带 default 标签）
- [x] T2 `mocksvc.go`：normalizePath / matchAPI（三种占位符）/ 开关 / 命中日志 ring
- [x] T3 公开路由 `ANY /mock/{projectCode}/*path`（免鉴权、独立前缀）
- [x] T4 `GET/PUT /projects/:id/mock`、`GET /projects/:id/mock/hits`

## Phase 2 — 前端

- [x] T5 `api/mock.ts` + 类型
- [x] T6 `MockView.vue`：开关、基址与 curl 示例、接口清单、命中日志
- [x] T7 路由 + 菜单

## Phase 3 — 测试与文档

- [x] T8 单测：匹配表驱动（精确/三种占位/query/方法不匹配/未开启）、ring 上限
- [x] T9 端到端 curl：200 / 404 / 命中日志
- [x] T10 契约 + 待开发清单 + README/边界表
- [x] T11 `scripts/check.sh` + CI
