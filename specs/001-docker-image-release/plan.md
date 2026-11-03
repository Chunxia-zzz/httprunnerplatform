# Implementation Plan: 容器化与 CI 镜像发布

**Branch**: `001-docker-image-release` | **Date**: 2026-11-03 | **Spec**: [spec.md](spec.md)

**Input**: `specs/001-docker-image-release/spec.md`（依据：Downloads《httprunnerplatform-CI-CD 接入手册》步骤 4-8、12；步骤 10-11 CD 明确不做）

## Summary

为仓库补齐容器化（.dockerignore + 三阶段 Dockerfile + compose + 部署配置样例）与 CI 镜像发布（check.yml 追加 concurrency 与 image job，检查全绿才构建推送 GHCR 并做镜像冒烟）；顺带修复 config.example.yaml 的 `jwt_secret` 字段名缺陷；新增 pre-push 本地门禁。

## Technical Context

- 构建：三阶段 Dockerfile——node:22-bookworm-slim（npm ci + build，内含 vue-tsc）→ golang:1.26-bookworm（CGO_ENABLED=0，GOPROXY=goproxy.cn，内嵌前端同 build.sh，下载 hrp v4.3.6 linux-amd64）→ debian:bookworm-slim（非 root app(10001)，HEALTHCHECK /api/v1/healthz）
- 发布：GitHub Actions `image` job（needs 三检查 job，仅 push main，GHCR，tag=full-sha+latest，gha 缓存，镜像冒烟）
- 配置注入：环境变量 `HRP_PLATFORM_*`（config 包已支持）+ 挂载 `deploy/config.yaml`（session_secret 从这来）
- **验证路径**：本机无 Docker（已确认），本地构建验证跳过，由 CI image job 的镜像冒烟承担；Dockerfile 首跑失败按报错修
- 兼容约束：镜像名转小写；前端内嵌必须显式 COPY 并校验 index.html；数据一律命名卷

## Project Structure

新增（全部在仓库根 / deploy/ / .githooks/）：`.dockerignore`、`Dockerfile`、`docker-compose.yml`、`deploy/config.example.yaml`、`.githooks/pre-push`；修改：`.gitignore`、`configs/config.example.yaml`、`.github/workflows/check.yml`。不触碰任何 Go/Vue 产品代码。

## Verification

本地：`bash -n`（sh 脚本）、`node --check`（如涉 mjs）、yaml 解析、bash scripts/check.sh 基线仍绿。
远端：push 后 Actions 依赖图 检查×4 → image；镜像冒烟 healthz OK；故意失败时 image skipped（可后验）。
