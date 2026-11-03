# Tasks: 容器化与 CI 镜像发布

**Input**: plan.md / spec.md（依据手册步骤 4-8、12）

## Phase 1 — 仓库容器化（US1/US3）

- [x] T1 `.dockerignore`（手册步骤 4 清单，含 `!internal/webui/dist/.gitkeep`）
- [x] T2 `Dockerfile` 三阶段（手册步骤 5 原文，含 build-id、hrp 下载与 `-v` 自检、非 root、HEALTHCHECK）
- [x] T3 `docker-compose.yml`（命名卷 hrp-data、挂载配置 ro、HRP_PLATFORM_CONFIG、healthcheck）
- [x] T4 `deploy/config.example.yaml`（sqlite dsn=/app/data/platform.db、workspace root /app/data、session_secret 占位）
- [x] T5 `.gitignore` 追加 `/deploy/config.yaml`
- [x] T6 修复 `configs/config.example.yaml` `jwt_secret` → `session_secret`

## Phase 2 — CI 镜像发布（US2）

- [x] T7 `check.yml` 顶部追加 `concurrency` 组
- [x] T8 `check.yml` 追加 `image` job（needs 三检查、push main 限定、GHCR 登录、build-push、sha+latest tag、gha 缓存、镜像冒烟）

## Phase 3 — 本地门禁（US4）

- [x] T9 `.githooks/pre-push`（bash scripts/check.sh，可执行位）+ `core.hooksPath` 说明

## Phase 4 — 验证

- [x] T10 本地静态验证：bash -n / yaml 解析 / check.sh 基线仍绿
- [ ] T11 push 后 CI 验证：检查×4 全绿 → image 全绿 → Packages 出现两个 tag（远端，push 后观察）
