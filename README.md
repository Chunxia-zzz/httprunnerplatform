# httprunnerplatform

一个基于 [HttpRunner](https://github.com/httprunner/httprunner) 引擎的**接口自动化测试平台**。

把 HttpRunner 从「手写 YAML + 敲命令行 + 报告散落在磁盘上」，变成「页面上写用例、点一下就调试、定时或流水线自动跑、失败能直接看出是谁的问题」的团队级平台。

## 定位

| 项 | 内容 |
|:---|:---|
| 目标用户 | 小型测试团队（约 10 人） |
| 产品形态 | 私有化部署（非 SaaS） |
| 执行引擎 | HttpRunner **v4.3.6**（`hrp` 二进制，子进程调用，不 fork） |
| 后端 | Go（Gin + GORM + MySQL 8） |
| 前端 | Vue 3 + Vite + TypeScript + Element Plus |
| 交付形态 | 单个可执行文件（内嵌前端）+ 一个数据库 |

## 核心能力

- **写用例**：页面表单化编辑，无需手写 YAML；支持接口引用、断言可视化配置、变量依赖提示
- **调试用例**：单步骤即时执行，不跑完整流程就能验证请求与响应，点击不超过 3 次
- **跑测试计划**：用例集 + 环境 + 触发方式（手动 / 定时 / CI）+ 通知，一体化配置
- **嵌入流水线**：Token 鉴权触发 API，支持同步阻塞等待结果
- **失败归因**：自动区分「被测系统的问题」与「用例/环境的问题」，直接回答该由谁处理
- **防假绿**：执行前后用例数对账，杜绝用例被静默跳过却显示全绿
- **多人协作**：账号由管理员创建，`admin` / `member` 两档；权限变更**立即吊销**对方会话

## 引擎基线（已实测）

引擎的若干行为与官方文档不符，且存在两个会直接影响架构的缺陷。**开工前必须了解这六条：**

| 事实 | 影响 |
|:---|:---|
| **断言失败会让引擎 panic**（退出码 2，且 summary.json / HTML 报告**完全不产出**） | 归因不能只看退出码；平台必须自行重建失败明细 |
| **`hrp run <目录>` 全有或全无**（一条断言失败就 panic，后续用例不跑、整批结果丢失） | **必须一个用例一个独立子进程** |
| **日志与报文分走两条流**（日志 → stderr JSON，请求/响应明细 → stdout 纯文本） | 执行器必须分别采集、分别解析 |
| **加载器静默吞掉畸形用例文件**（不报错、退出码 0） | 必须做事前校验 + 事后用例数对账 |
| **URL 不带查询串时会被补上结尾斜杠**（`$base_url/get` 实际发出 `GET /get/`；`params: {}` 也绕不开） | 平台无法代改，只能给出 `TRAILING_SLASH_ADDED` 校验提示；调试视图必须展示**最终生效 URL** |
| **遥测（GA4 + Sentry）默认开启**，无 CLI 参数，只能靠子进程环境变量关 | 必须注入 `DISABLE_GA=true` / `DISABLE_SENTRY=true`。不关则每次执行耗时在 1.3–5.1 秒间跳动，且上报失败会被当成用例错误 |

完整实测证据与复现方式见 [docs/引擎实测记录.md](docs/引擎实测记录.md)。

## 文档

| 文档 | 内容 |
|:---|:---|
| [docs/项目方案.md](docs/项目方案.md) | **项目目的、技术选型、架构设计、数据模型、核心机制、开发计划**（先读这份） |
| [docs/引擎实测记录.md](docs/引擎实测记录.md) | **HttpRunner v4.3.6 的四轮实测结果与原始证据**（解析器/执行器的契约来源） |
| [docs/接口契约.md](docs/接口契约.md) | **REST API 契约**：端点、请求/响应结构、业务码字典、静态校验 issue 码字典 |
| [docs/数据库设计.md](docs/数据库设计.md) | 表结构与 DDL、双驱动说明、**布尔列不带默认值**的约定 |

## 开发计划

| 里程碑 | 范围 |
|:---|:---|
| M1 · 闭环 | 项目 / 环境 / 用例编辑 / 编译器 / 执行器 / 结果解析 / 执行记录 |
| M2 · 计划与流水线 | 接口定义库 / 用例集 / 测试计划（定时）/ CI Token / 通知 |
| M3 · 调试与数据驱动 | 单步调试 / 参数化 / 断言配置器 / 源码视图可编辑 |
| M4 · 体验与扩展 | 统计看板 / 导入 / 基线对比 |

## 当前进度

- ✅ **13.1 仓库准备**：Go module、目录骨架、`.gitignore`、配置样例、`pkg/hrpclient`（二进制探测/版本）、CLI 骨架、开发环境脚本、本地 echo 测试服务、探针工程
- ✅ **13.2 引擎实测**：四轮实测完成，结论已回写进方案与契约文档（修正了原方案中关于退出码归因、日志流、结果产出的多处错误假设）
- ✅ **M1 后端**：编译器 / 执行器 / 结果解析 / 静态校验 / 服务编排 / REST API 全部完成，`go vet` 与全量单测通过
- ✅ **端到端冒烟**：22 项断言全绿，覆盖建项目 → 建环境 → 建用例 → 校验 → YAML 预览 → 执行 → 步骤/断言明细 → 报告 → 日志 → 错误码（脚本暂存于仓库外的 `../httprunnerplatform-e2e/e2e.sh`）
- ✅ **M1 前端**：`web/` 下的 Vue 3 页面全部完成（登录 / 项目 / 环境 / 用例列表 / 用例编辑器 / 执行记录 / 执行详情），`vue-tsc` 与 `vite build` 均无错误
- ✅ **M1 验收**：四条验收标准已由真实浏览器跑通（63 项断言全绿；脚本暂存于仓库外，尚未入库）
- ✅ **单文件交付**：`bash scripts/build.sh` 产出内嵌前端的单个可执行文件（42 MB，内嵌 42 个前端文件）；关掉 Vite 只跑这一个 exe，前端功能不依赖 Node
- ✅ **M1.5 多账号与权限**（团队上线的前置条件）：
  - 账号 CRUD（`/api/v1/users`，整组 `RequireAdmin`）+ 本人改密码（需验旧密码）
  - 三条守卫：不能对自己下手 / 不能干掉最后一个**可用**管理员 / 重置密码不能作用于自己
  - ⭐ **权限变更立即吊销会话**：会话表缓存着 `Principal`（含 role），不吊销的话被降级的管理员在 TTL 内仍是管理员
  - 前端：账号管理页（仅管理员可见）、顶栏「修改密码」、成员侧边栏不出现管理入口
  - 测试：`internal/auth` 7 项 + `internal/service` 30 余项 + `internal/api` 9 项；浏览器端到端 [`scripts/ui/auth-smoke.mjs`](scripts/ui/auth-smoke.mjs) **44 项全绿**（脚本已入库）
- ✅ **③ 编排层（用例集 / 测试计划 / CI Token）**：后端三片 + 前端三页已全部落地
  - 设计已定稿：[docs/用例集与测试计划设计.md](docs/用例集与测试计划设计.md)
  - ✅ 已完成：用例集的模型、服务、API 与单测（成员唯一、跨项目拒绝、成员「能不能跑」可见化）
  - ✅ 已完成：用例集**执行**（1 条 RunRecord + N 条 CaseResult）
    - 串行执行，成员顺序即执行顺序；`on_failure` 支持 `continue`（默认）/ `abort`
    - 用例数对账：`expected` / `actual` / `count_mismatch`，不一致强制升为 error（防 F5 假绿）
    - ⭐ `abort` 与取消时 `expected` 收缩到实际尝试条数 —— 主动中止不该被记成对账不一致
    - 已禁用成员记 `skipped`；已删除成员记 `error`（该分支页面走不到，见下）
    - ⭐ 实测发现：**被引用的用例不允许删除**（40003），所以「已删除成员」是防御性分支，覆盖放在单测里
    - 单测 20 项 + 端到端冒烟 [`scripts/smoke-suite.mjs`](scripts/smoke-suite.mjs) **54 项全绿**
  - ✅ 已完成：**测试计划与 cron 调度**
    - 计划 = 一组用例集 + 什么时候跑；`trigger_type` 支持 `manual` / `cron`
    - ⭐ cron 在**保存时**校验（5 段 + 描述符），不让它变成"永远不跑的计划"
    - ⭐ 每个计划自带 IANA 时区（默认 `Asia/Shanghai`），`next_fire_at` 按该时区算 ——
      不绑时区的话服务器在 UTC 上跑，用户填的"早上 9 点"会静默错位 8 小时
    - 进程内调度器（`internal/scheduler`，30 秒对账一次，自愈不依赖调用方）：
      重入保护（两道：进程内 + DB 的 `last_run_id`）、不补跑但记 `last_missed_at`
    - ⭐ 一次计划触发 = 每个用例集各一条执行记录（`plan_id` 关联），
      失败隔离优先于"看起来只有一条"
    - 单测 27 项；端到端冒烟 [`scripts/smoke-plan.mjs`](scripts/smoke-plan.mjs) **34 项全绿**
      （配 `* * * * *` 真等一个调度点，验证定时触发真的跑了用例）
  - ✅ 已完成：**CI Token 与 `/open` 触发**
    - 只存 `sha256` 哈希（不是 bcrypt：32 字节随机数没有猜测空间，
      用 bcrypt 只会让每次触发白付 100ms 的 KDF 开销），明文创建时给一次
    - 按项目签发，且**请求里的 `project_id` 必须等于令牌所属项目**（触发与读结果都校验）
    - `?wait=1` 同步等待选配，默认异步 —— CI 的长连接容易被网关掐断
    - ⭐ `exit_code_for_ci` 给 pipeline 一个数字（0/1/2），
      不给的话每条流水线都要写 jq 解析枚举，而且各人判断不一（有人把 error 当成功）
    - 单测 20 项；端到端冒烟 [`scripts/smoke-ci.mjs`](scripts/smoke-ci.mjs) **37 项全绿**
  - ✅ 已完成：**编排层前端页面 + 浏览器端到端验收**
    - 用例集页：成员顺序即执行顺序（↑↓ 调序 + 移出）、跑不了的成员在抽屉里标出原因
    - 测试计划页：cron + 中文说明 + 时区 + `next_fire_at`，跳过的调度点在列表上留痕（不补跑但看得见）
    - CI 令牌页：明文只显示一次 + 复制 + 关窗二次确认；页底给出可直接贴进流水线的 curl 示例
    - 浏览器端到端 [`scripts/ui/orchestration-smoke.mjs`](scripts/ui/orchestration-smoke.mjs) **34 项全绿**：
      ⭐ 成员勾选顺序即执行顺序（按 a/c/b 勾选后列表序完全一致，而非 ID 序）、
      ↑ 调序生效、`case_count` 刷新、执行跳详情；
      cron 中文说明 / 时区 / `next_fire_at`（工作日计划真的算到了下一个工作日 09:00）同列展示；
      明文只在签发弹窗出现、未复制关窗弹二次确认、已复制关窗直接关、列表只有 prefix、吊销即移除
    - （headless 里 `navigator.clipboard` 默认被拒，脚本注入 stub 验证「复制成功后不再确认」的产品逻辑；
      剪贴板权限本身是环境，不是被测对象）
- ✅ **M3 调试与数据驱动（③ 三片全部落地）**
  - 设计已定稿：[docs/M3调试体验设计.md](docs/M3调试体验设计.md)
  - ✅ **③-a 单步调试**：`internal/service/debug.go`（临时最小用例，跑完即弃）+ `POST /cases/{id}/debug` + 前端 `DebugPanel.vue`
    - 引擎最小执行单位是用例文件（实测 A2），平台构造「前置步骤 + 目标步」的最小用例，真实执行前置步骤拿真实变量值
    - 验收：smoke-debug 14 项 + CDP [`scripts/ui/debug-smoke.mjs`](scripts/ui/debug-smoke.mjs) 11 项全绿（含变量依赖验证）
  - ✅ **③-b 参数化**：`internal/service/param.go` + `compiler/parameters.go` + `POST /projects/{id}/datasets` 等 6 个端点 + 前端 `ParamListView.vue`
    - 平台层格式 `config.datasets = [{name, limit}]`，编译时转引擎语法；CSV 存项目工作区，limit 编译期裁剪
    - ⭐ 关键实测（A22）：`- parameterize: file.csv` 写法 v4.3.6 **静默丢弃**；正确写法是 `col1-col2: "${P(data/xxx.csv)}"`；关联参数成对迭代（非笛卡尔积）
    - 验收：compiler 15 项单测 + smoke-param 28 项 + CDP [`scripts/ui/param-smoke.mjs`](scripts/ui/param-smoke.mjs) 16 项全绿（CSV 3 行 → 3 次迭代）
  - ✅ **③-c 断言配置器 + 源码可编辑 + 变量依赖**
    - 断言配置器 M1 已实现（下拉 + 类型推断 + 编译预览），③-c 仅验收
    - 源码可编辑：`internal/decompiler/`（YAML → CaseReq 反解析）+ `PUT /cases/{id}/yaml`；反解析失败带行号、前端红标高亮
    - 变量依赖：前端 `utils/variables.ts` 三件套（`$name` / `${}` 边界 / `$$` 转义）+ 跨步骤作用域 + 未定义标红；后端 validator 的 `UNDEFINED_VARIABLE` 兜底
    - 验收：decompiler 8 项单测 + smoke-yaml-save 11 项 + CDP [`scripts/ui/m3c-smoke.mjs`](scripts/ui/m3c-smoke.mjs) 9 项全绿
- ✅ **M4 体验与扩展（四块全部落地）**
  - ✅ **M4-a 统计看板**：`internal/service/stats.go` + `GET /stats/{trend,flaky,slowest}` + 前端 `StatsDashboardView.vue`（ECharts 按需引入，路由懒加载隔离 592KB chunk 不污染首屏）
    - 通过率趋势（按天补零）、不稳定用例排行（rate 分档着色）、慢用例排行
    - 聚合放在 Go 内存做（跨 mysql/sqlite 驱动一致）；「先取 run_id 集合再聚合 case_result」避免无外键 JOIN 漏行
    - 验收：4 项单测 + CDP [`scripts/ui/stats-smoke.mjs`](scripts/ui/stats-smoke.mjs) 11 项全绿
  - ✅ **M4-b 导入**：`internal/service/importsvc.go` + `POST /projects/{id}/import/{preview,commit}` + 前端 `ImportDialog.vue`
    - 走 `hrp convert --to-yaml` 真实转换（HAR / Postman / curl 自动识别 + 扩展名兜底），decompiler 扩展支持字典形态断言
    - 两段式（预览 → 确认导入），name/code 自动去重，逐用例成败报告不做整体回滚
    - ⭐ 实测（A23）：`-d` 输出目录必须预建、GA4 遥测每次拖 5s（给 60s 兜底）、HAR 的 validate 是字典形态
    - 验收：15+ 项单测 + [`scripts/smoke-import.mjs`](scripts/smoke-import.mjs) 20 项全绿
  - ✅ **M4-c 导出**：`internal/service/exportsvc.go` + `GET /projects/{id}/export` + 前端「导出」按钮
    - 走 `compiler.Render` 纯渲染（不落盘、只读、永远反映 DB 当前内容），单用例失败写 README 不阻断整体
    - 返回 `application/zip` 二进制流 + 附件名；失败返回统一 JSON，前端按 Content-Type 区分
    - 验收：3 项单测 + [`scripts/smoke-export.mjs`](scripts/smoke-export.mjs) 8 项全绿
  - ✅ **M4-d 基线对比**：`internal/service/baseline.go` + `GET /projects/{id}/baseline` + 前端 `BaselineDialog.vue`
    - 选定用例拉取最近 N 次执行并排对比（状态/归因/耗时/步骤失败数），相邻差异信号（broke / recovered / 耗时差）由后端给「事实」、前端可视化
    - 复用现有 run_record + case_result + step_result 历史，**不新增表**；「先取 run 再批量补步骤数」避免 N+1
    - 验收：6 项单测 + [`scripts/smoke-baseline.mjs`](scripts/smoke-baseline.mjs) 9 项全绿

## 快速开始（单文件交付）

```bash
bash scripts/build.sh          # 前端 → go:embed → 一个可执行文件
./httprunnerplatform.exe server
```

浏览器打开 <http://127.0.0.1:8080> 即可（默认账号 `admin` / `admin123`，**请尽快修改**）。
前端已内嵌，**不需要 Node**，也不需要另起进程 —— 这是推荐的交付方式。

```bash
bash scripts/build.sh /tmp/xxx     # 指定输出路径
SKIP_NPM=1 bash scripts/build.sh   # 复用已有 web/dist，只重新内嵌与编译
```

> **为什么构建必须走这个脚本，而不能直接 `go build`**
>
> `vite` 产出在 `web/dist`，而 `go:embed` 读的是 `internal/webui/dist`。
> 这两者之间的"同步"如果靠人记得手动做，早晚会出现「代码改了、界面没变」
> 的幽灵问题 —— 所以固化成脚本，并在编译前校验 `web/dist/index.html` 存在。
>
> 全新克隆（还没构建过前端）也能 `go build` / `go test ./...`：内嵌目录里
> 提交了一个 `.gitkeep` 占位。此时启动服务访问 `/` 会看到一个明确的
> 「前端未构建」提示页，而不是白屏。

## 前端开发

前端在 `web/`（Vue 3 + Vite + TypeScript + Element Plus + Pinia）。
**改前端代码时用开发服务器**（改动即时生效，不用每次重新编译二进制）：

```bash
cd web
npm install
npm run dev        # http://127.0.0.1:5173，/api 自动代理到 127.0.0.1:8080
```

```bash
npm run typecheck  # vue-tsc --noEmit
npm run build      # 先类型检查，再产出 web/dist
```

> **为什么走 Vite 代理而不是后端开 CORS**
>
> 1. 会话 Cookie 是 `HttpOnly` + `SameSite=Lax`。`5173` 与 `8080` 虽属同一站点
>    （同 host、不同端口），但浏览器对"同站"的判定本身就依赖这个前提；一旦
>    以后换成不同 host 部署，CORS 方案还需要额外处理 `SameSite=None; Secure`。
> 2. 走代理后前端只认 `/api` 这一个同源前缀，不需要把后端地址写进构建产物里。
>
> 代理目标可用环境变量覆盖：`HRP_API_TARGET=http://127.0.0.1:9000 npm run dev`。

## 浏览器验收（CDP，零依赖）

`scripts/ui/` 下是真实浏览器的端到端验收脚本：用本机 Chrome + Node 自带的
`WebSocket` 直接驱动 DevTools Protocol，**不需要**下载 Playwright / agent-browser 的
Chromium（省掉约 500 MB 与十几分钟安装）。

```bash
# 前置：后端在跑（默认 127.0.0.1:8080 或经 HRP_API_TARGET 指定），前端 dev server 在 5173
HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/auth-smoke.mjs
```

| 文件 | 说明 |
|:---|:---|
| `scripts/ui/cdp.mjs` | 驱动：`launchChrome` / `connect` / `goto` / `evaluate` / `waitFor` / `screenshot` / `shutdown` |
| `scripts/ui/auth-smoke.mjs` | 账号与权限验收（44 项）：角色隔离、删项目被拒、**会话吊销**、改密码 |

> **为什么这类改动必须走浏览器，而不能只打接口**
>
> 有三处问题只在页面上才看得见：侧边栏按角色显隐、被吊销会话后前端是否真的退回登录页、
> 以及"改完密码当前会话要保留"。接口全绿但用户被莫名登出，只有这条路能发现。
>
> 脚本用**两份会话 Cookie**（通过 CDP 的 `Network.getCookies` / `setCookie` 在同一个
> 浏览器里来回切换）来模拟两台设备，因此可以在单浏览器内证明
> "禁用某人 → 他正在用的那个会话立刻失效"，而不是"等 TTL 到点"。
>
> 断言失败时脚本 `exit 1`，可直接接进 CI。

## 环境准备

```bash
# 引擎二进制与 Go 工具链放在仓库外的固定位置（不入 Git）
export HRP_PLATFORM_TOOLS=F:/httprunnerplatform-tools

# 载入开发环境（Go 工具链 + hrp 路径探测）
source scripts/dev-env.sh

# 完整构建（含前端内嵌）—— 交付与自检都用它
bash scripts/build.sh
./httprunnerplatform.exe doctor
```

> 只改后端、想快速拿一个能跑的二进制时，可以跳过前端构建：
> `SKIP_NPM=1 bash scripts/build.sh`（复用上次的 `web/dist`）。
> 直接 `go build ./cmd/server` 也是合法的，但产物**不含前端**，
> 访问 `/` 只会看到"前端未构建"提示页。

> **磁盘注意事项**：Go 的构建缓存与临时目录默认在 `%LOCALAPPDATA%`（C 盘）。
> 若 C 盘空间紧张，`go build` 会以 `There is not enough space on the disk` 失败。
> 本项目已将其重定向到工具目录：
>
> ```bash
> go env -w GOCACHE=F:/httprunnerplatform-tools/gocache \
>           GOTMPDIR=F:/httprunnerplatform-tools/tmp/gotmp
> ```

## License

[MIT](LICENSE)
