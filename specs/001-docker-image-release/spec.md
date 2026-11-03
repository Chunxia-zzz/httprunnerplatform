# Feature Specification: 容器化与 CI 镜像发布（CI 接入，CD 暂缓）

**Feature Branch**: `001-docker-image-release`

**Created**: 2026-11-03

**Status**: Draft

**Input**: 按《httprunnerplatform-CI-CD 接入手册》（Downloads，2026-11-03）接入 CI：容器化 + 检查通过后构建发布镜像；CD（自动部署 job）本轮明确不做。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 仓库容器化：一条命令构建出能跑的镜像 (Priority: P1)

开发者在仓库根目录执行 `docker build`，得到一个自包含镜像：内嵌前端（登录页而非「前端未构建」提示页）、内置 hrp v4.3.6 引擎、SQLite 数据卷、healthz 健康检查。`docker run` 起容器后 `/api/v1/healthz` 返回 `code:0`。

**Why this priority**: 镜像是 CI 发布与未来 CD 的载体，没有它后面一切免谈。

**Independent Test**: 构建日志三阶段齐全（vite built / hrp -v 输出 v4.3.6 / 镜像命名成功）；起容器后 healthz 通过；镜像体积在 100~200MB 量级。

### User Story 2 - CI 门禁镜像发布：检查全绿才产出镜像 (Priority: P1)

push 到 main 且现有三组检查 job（backend×2 / frontend / security）**全部成功**后，新增的 `image` job 才执行：构建镜像 → 推送 `ghcr.io/chunxia-zzz/httprunnerplatform:<full-sha>` 与 `:latest` → 对推上去的镜像做冒烟（起容器 → healthz）。任一检查失败 → image 显示 skipped，**不产出镜像**。

**Why this priority**: 这是「接入 CI」的核心承诺——坏代码进不了镜像；sha tag 支持未来精确回滚。

**Independent Test**: 正常 push 后 Actions 依赖图为检查 job → image 全绿，Packages 出现两个 tag；故意制造 gofmt 失败后 push，backend 红、image skipped。

### User Story 3 - 本地一键起服务与部署配置样例 (Priority: P2)

提供 `docker-compose.yml`（命名卷持久化数据、挂载配置、healthcheck）与 `deploy/config.example.yaml`（不含真实 secret），使任何装了 Docker 的机器 `docker compose up -d` 即可得到与 CI 产物一致的服务。

**Why this priority**: 镜像造出来要能用；同时修正配置样例中 `jwt_secret` → `session_secret` 的字段名错误（当前样例照抄会导致 session 密钥配置静默失效——已在代码中核实 `internal/config/config.go` 读取的是 `session_secret`）。

**Independent Test**: compose ps 显示 running (healthy)；healthz 返回 ok；浏览器看到登录页。

### User Story 4 - 推送前本地门禁（单分支直推的补偿） (Priority: P3)

新增 `.githooks/pre-push`（执行 `scripts/check.sh`）并通过 `core.hooksPath` 启用：本地先拦一道，减少「push 后 CI 才红」的次数。紧急时可用 `--no-verify` 跳过，CI 仍会拦截镜像构建。

**Why this priority**: 锦上添花的体验改进，不影响主线。

**Independent Test**: 制造 gofmt 违规后 push 被拒并提示；还原后推送成功。

### Edge Cases

- GHCR 镜像名必须全小写（`Chunxia-zzz` → `${GITHUB_REPOSITORY,,}` 转换）。
- hrp 引擎下载失败（网络）：手册提供离线兜底（手动下载放入 `deploy/` 后 COPY）。
- 构建上下文膨胀：`.dockerignore` 必须排除 `web/node_modules`、`data`、`internal/webui/dist/*`（保留 .gitkeep）、`configs/config.yaml`。
- 前端未内嵌：Dockerfile 显式 `COPY --from=web` 并校验 `dist/index.html` 存在。
- Windows 产物 exe 与容器版同时运行会端口冲突 / 双调度器：文档提示互斥。
- 会话存内存：容器重启全员登出，属预期行为。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：新增 `.dockerignore`，排除清单按手册步骤 4（含 `!internal/webui/dist/.gitkeep` 反向放行）。
- **FR-2**：新增三阶段 `Dockerfile`（前端 node:22 → 后端 golang:1.26 `CGO_ENABLED=0` + 下载 hrp v4.3.6 → 运行 debian:bookworm-slim，非 root 用户 app(10001)，HEALTHCHECK 用 `/api/v1/healthz`）。
- **FR-3**：后端构建阶段等价复刻 `scripts/build.sh` 的前端内嵌步骤（`web/dist → internal/webui/dist` + `build-id`），保证「容器里看到的界面」与单文件交付一致。
- **FR-4**：新增 `docker-compose.yml`：命名卷 `hrp-data`（禁止绑定 Windows 目录）、挂载 `deploy/config.yaml:ro`、`HRP_PLATFORM_CONFIG` 指向挂载配置、healthcheck。
- **FR-5**：新增 `deploy/config.example.yaml`（不含真实 secret）；`deploy/config.yaml` 加入 `.gitignore`。
- **FR-6**：修正 `configs/config.example.yaml` 的 `jwt_secret` → `session_secret`（行为缺陷修复，代码字段名为准）。
- **FR-7**：`.github/workflows/check.yml` 追加 `concurrency` 组与 `image` job：`needs: [backend, frontend, security]`、仅 `push main` 执行、`packages: write`、tag = `<full-sha>` + `latest`、gha 层缓存、镜像冒烟步骤。
- **FR-8**：新增 `.githooks/pre-push`（执行 `scripts/check.sh`）并 `git config core.hooksPath .githooks` 入库。
- **FR-9**：明确排除项（本轮不做）：`deploy` job、服务器准备、Secrets 配置、自动回滚（手册步骤 10-11，留待有部署目标后）。

### Key Entities *(include if feature involves data)*

- 镜像 tag：`ghcr.io/chunxia-zzz/httprunnerplatform:<full-commit-sha>`（回滚锚点）与 `:latest`。
- 数据卷：`hrp-data`（SQLite platform.db + workspaces + runtime/reports，唯一持久化点）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：push main 后 Actions 依赖图为 backend(ubuntu) + backend(windows) + frontend + security → image，全绿；Packages 出现 `latest` 与 `<sha>` 两个 tag。
- **SC-2**：镜像冒烟（image job 内起容器调 healthz）输出 `healthz OK`。
- **SC-3**：任一检查 job 失败时 image 状态为 skipped（门禁真实生效）。
- **SC-4**：构建上下文在几 MB 量级（.dockerignore 生效）。
- **SC-5**：容器内 `httprunnerplatform version` 输出对应短 sha；`hrp -v` 输出 v4.3.6。

## Assumptions

- 本机未装 Docker（已确认 `docker: command not found`）：**本地构建/验证跳过**，验证主路径 = push 后由 CI 的 image job 在 GitHub runner 上构建并执行镜像冒烟；Dockerfile 若在 CI 首跑失败，按报错修复重推。
- 仓库已有 `.gitattributes`（`eol=lf`），换行符无需额外处理。
- 部署目标（服务器）未定，deploy job 本轮不做；GHCR 首推后默认私有，拉取需 PAT 或设为 Public——由用户在 GitHub 上自行操作。
