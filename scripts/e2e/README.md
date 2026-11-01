# scripts/e2e —— 端到端验收脚本

来自开发期（M1，2026-09-22）的验收工作区（原 `F:/httprunnerplatform-e2e`、
`F:/httprunnerplatform-ui`），2026-11-01 脚本迁入仓库做版本管理；
**运行数据仍留在仓库外的原目录**（每次运行新建时间戳子目录，不清理）。

| 脚本 | 干什么 | 怎么跑 |
|:---|:---|:---|
| `e2e.sh` | API 全链路冒烟：建项目→建环境→建用例→校验→执行→看归因→报告/日志；自带 mock 被测服务 | `bash scripts/e2e/e2e.sh` |
| `ui-smoke.mjs` | 真实浏览器（CDP）走 M1 四条验收标准：页面建用例→执行→归因标签→报告降级→对账 | 先起前端(5173)+mock(8899)，再 `node scripts/e2e/ui-smoke.mjs` |
| `cdp.mjs` | 上面两者的公共底座：launchChrome / connect / evaluate / screenshot | 被导入，不直接跑 |

环境变量见各脚本头部注释（`HRP_E2E_ROOT` / `HRP_PYTHON` / `HRP_UI_BASE` / `HRP_UI_SHOTS`）。

> 这两个冒烟目前**不在 CI 里**（需要真浏览器与本机 Chrome），属本地手动验收工具；
> 未纳入的部分正是 P3 候选「Playwright E2E 进 CI」想补的。
