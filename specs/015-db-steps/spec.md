# R1 数据库步骤（db 步骤）：前置造数 / 后置校验落库

## Why

自动化测试最常见的漏测点是「接口返回 200 但数据没落库 / 落库内容不对」。
当前用例只能发起 HTTP/WS/GraphQL 请求，无法校验数据库状态，也无法用 SQL 造测试数据。
这是 Apifox 付费版的招牌能力（数据库操作），也是测试团队的高频刚需（产品规划 R1，P0）。

## Scope

- 步骤类型新增 `db`：填「连接（项目内 db_source 的 key）+ SQL + 参数」，
  查询结果可**提取**（与既有 extract 同语义）也可**断言**（与既有 validate 同语义）。
- 数据库连接为**项目级配置**（db_source 表），本期支持 MySQL / PostgreSQL / SQLite（文件型）。
  SQLite 零依赖，用于轻量场景与本验收。
- 本期不做：SQL 构建器、多数据源事务、SQL 执行结果的图形化 diff、
  调试台之外的 db 步骤单步调试（调试台直接由服务层执行，见下）。

## 核心设计决策：平台回环端点（不用引擎插件）

**方案**：db 步骤编译成一个普通的 HTTP POST 步骤，目标地址是**平台自身的回环端点**
`http://127.0.0.1:{server.port}/dbstep`，请求体携带 `{conn, sql, params}`；
平台执行 SQL 后返回 `{rows, affected, columns, duration_ms}`（JSON）。

**为什么这样**（而不是 debugtalk Python 插件函数）：

1. 断言/提取/重试/归因/对账**全部走引擎原生路径**——`body.rows[0].name` 就是
   jmespath，断言失败照样 panic→平台重建，SQL 错误照样 error→environment 归因；
   「不假绿」体系零成本继承。
2. SQL 执行在平台 Go 侧（database/sql + 三家驱动），不要求执行机装 Python DB 驱动。
3. 结构化优先：连接是配置项而不是脚本里的字符串，密码可掩码、可审计。

**安全约束**：

- `/dbstep` 挂在根级（引擎无会话），但要求请求头 `X-Dbstep-Token: <每 run 随机>`；
  token 由执行器在准备工作区时生成，写入该 run 的 `.env`（`dbstep_token=...`）并注册到
  进程内注册表；端点校验 token 对应的 run 尚未终态。终态即失效——执行结束后这条
  SQL 通道立刻关闭。
- conn 必须是**本项目**的 db_source key，跨项目/未知 key 一律 40300/40004。
- 每次执行记录「SQL 执行审计」？——本期不做独立审计表，步骤快照里已有 SQL 全文。

## Key Entities

- **db_source**（项目级连接配置）：`{project_id, name(key), driver(mysql|postgres|sqlite),
  host, port, dbname, username, password, dsn_extra?}`；password 展示掩码 `***`，
  提交 `***` 保持原值（与 F7 auth 同语义）。
- **test_step 新列**：`db_source_name`、`db_sql`、`db_params`（JSON）。
- **编译产物**：db 步骤 → `request: POST {loopback}/dbstep`，
  headers 带 `X-Dbstep-Token: $dbstep_token`，body=`{conn, sql, params}`；
  `step_type` 仍记 `db`（展示用），decl 层面按 request 步骤参与断言/提取校验。

## FR

- **FR-1**：db_source CRUD（管理员写；读全员，password 掩码）；项目隔离。
- **FR-2**：db 步骤保存与校验：conn 必须存在于本项目；SQL 非空；params 需合法 JSON。
  校验器对「前置写 SQL（INSERT/UPDATE/DELETE）出现在后置位」给 warning（旁路，不阻断）。
- **FR-3**：编译：db 步骤渲染为回环 POST；SQL 里的 `$var`/`{{...}}` 动态变量照常展开；
  断言与提取沿用既有配置（检查表达式基于 `body.rows`/`body.affected`）。
- **FR-4**：执行：单用例与用例集链路均注入 per-run token；终态后 token 失效。
- **FR-5**：调试台单步执行 db 步骤**不经引擎**，由服务层直接执行 SQL 并返回同构结果
  （体验一致，调试不产生引擎依赖）。
- **FR-6**：前端：步骤编辑器新增「数据库」类型（连接下拉=本项目 db_source、SQL 编辑、
  参数 JSON 校验）；环境页新增「数据库连接」区块（CRUD + 密码掩码）。
  断言/提取页签与 HTTP 步骤完全一致（检查表达式提示 `body.rows[0].xxx`）。

## Non-goals（本期明确不做）

- SQL 构建器/可视化编辑；多语句事务；连接池配置暴露；
- Oracle/SQLServer/Redis 等其它数据源；db 步骤的定时独立执行。

## Acceptance（端到端，全部走真实引擎）

1. SQLite：第 1 步 db `INSERT`（造数）→ 第 2 步 HTTP 调回显服务（带上第 1 步提取的 id）→
   第 3 步 db `SELECT` 断言落库字段与请求一致 → success；
2. 断言写错（期望值不符）→ failed + `system_under_test`，断言明细平台重建；
3. SQL 语法错误 → 步骤 `error`，错误信息可读（含数据库原生错误）；
4. conn 写成别的项目的 → 触发即拒（40004/40300）；
5. 无 token / 错 token 直接 curl `/dbstep` → 401；执行终态后旧 token 失效；
6. 调试台执行 db 步骤返回查询结果（不经引擎，不产生执行记录）；
7. password 掩码：非管理员/列表查询只见 `***`，回传 `***` 保持原值。
