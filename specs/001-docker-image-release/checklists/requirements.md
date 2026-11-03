# 需求质量 Checklist: 容器化与 CI 镜像发布

**Purpose**: 校验 spec.md 的需求质量（完整性 / 无歧义 / 可验证）
**Created**: 2026-11-03
**Feature**: [spec.md](../spec.md)

## 需求完整性

- [x] 每个用户故事可独立测试（US1 构建 / US2 发布门禁 / US3 compose / US4 pre-push）
- [x] 明确的排除项（CD/deploy job 本轮不做，写进 FR-9 与 Assumptions）
- [x] 边界情况覆盖（镜像名小写、hrp 下载失败兜底、上下文膨胀、前端未内嵌、端口互斥）

## 需求无歧义

- [x] 镜像 tag 契约明确（full-sha + latest，回滚锚点）
- [x] 「检查全绿才产出镜像」有可观测判定（image skipped）
- [x] jwt_secret 字段名缺陷有代码级证据（config.go:112 yaml:"session_secret"）

## 可验证性

- [x] SC-1~SC-5 均可通过 Actions 页面 / Packages / 构建日志客观判定
- [x] 验证路径对本机无 Docker 的现实做了显式假设（CI 承担镜像冒烟）
