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
- ✅ **端到端冒烟**：`bash ../httprunnerplatform-e2e/e2e.sh`（22 项断言全绿，覆盖建项目 → 建环境 → 建用例 → 校验 → YAML 预览 → 执行 → 步骤/断言明细 → 报告 → 日志 → 错误码）
- ✅ **M1 前端**：`web/` 下的 Vue 3 页面全部完成（登录 / 项目 / 环境 / 用例列表 / 用例编辑器 / 执行记录 / 执行详情），`vue-tsc` 与 `vite build` 均无错误
- ✅ **M1 验收**：四条验收标准已由真实浏览器跑通（`node ../httprunnerplatform-ui/ui-smoke.mjs`，63 项断言全绿）
- ✅ **单文件交付**：`bash scripts/build.sh` 产出内嵌前端的单个可执行文件（42 MB，内嵌 28 个前端文件）；关掉 Vite 只跑这一个 exe，同一套 63 项断言仍然全绿

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
