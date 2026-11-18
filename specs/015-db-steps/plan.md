# R1 技术方案（db 步骤）

## 架构

```
用例步骤: [db: INSERT ...] → [http: POST /orders] → [db: SELECT ... + 断言]
                    │                    │                   │
              编译为 POST          正常编译            编译为 POST
                    ▼                    ▼                   ▼
        平台回环端点 POST /dbstep   引擎原生          平台回环端点 POST /dbstep
        （平台执行 SQL）                              （平台执行 SQL → rows JSON）
                                                             ▼
                                                   引擎原生断言 body.rows[0].name
```

- 引擎视角：三个都是普通 HTTP 步骤。第二个打到 $base_url（被测系统），
  第一/三个打到平台回环 `/dbstep`（带每 run token）。
- 断言、提取、重试、归因、对账全部是引擎/平台既有机制，零新增概念。

## 数据模型

```go
// db_source：项目级连接配置
type DbSource struct {
    Base
    ProjectID uint64 `gorm:"not null;index"`
    Name      string `gorm:"size:64;not null;default:''"`   // 项目内唯一 key
    Driver    string `gorm:"size:16;not null"`              // mysql | postgres | sqlite
    Host      string `gorm:"size:128;not null;default:''"`
    Port      int    `gorm:"not null;default:0"`
    DBName    string `gorm:"size:128;not null;default:''"`  // sqlite 时为文件路径
    Username  string `gorm:"size:64;not null;default:''"`
    Password  string `gorm:"size:128;not null;default:''"`  // 库内明文，展示掩码（R8 再加密）
}
```

- `test_step` 新列：`db_source_name`、`db_sql`（text）、`db_params`（text JSON）。
- 布尔列无；新增列均带 `default:''`（SQLite 增量迁移约束，F4/F5 踩过）。

## 回环端点契约

```
POST /dbstep            （根级，引擎用；不挂会话鉴权）
Headers: X-Dbstep-Token: <per-run random>
Body:    {"conn": "paydb", "sql": "SELECT ... WHERE id=$1", "params": [...]}
→ 200 {"rows": [...], "columns": [...], "affected": N, "duration_ms": ms}
→ 401 token 无效/已终态
→ 403 conn 不属于该 token 对应 run 的项目（跨项目拒绝）
→ 400 SQL 为空；500 数据库原生错误（message 原样带回，让引擎步骤报错可读）
```

- token 注册表：`dbstepTokens map[uint64]dbstepEntry{token, projectID, done}` +
  mutex；run 进入终态时 `closeToken(runID)`。溢出安全：map 随终态清理，不泄漏。
- sqlite 驱动复用平台已有 `glebarez/sqlite`（纯 Go）；mysql `go-sql-driver/mysql`、
  postgres `lib/pq`（均已 go get）。
- DSN 构造：mysql `user:pass@tcp(host:port)/db?parseTime=true`；
  postgres `host=.. port=.. user=.. password=.. dbname=.. sslmode=disable`；
  sqlite 直接文件路径。连接**每次执行即开即用**（sql.Open + Ping + Close），
  不做池化（压测/高频场景 R7 再说）。

## 编译

- `renderStep` 增加 `case model.StepDb`：构造 RequestSpec
  `{Method: POST, URL: loopbackURL + "/dbstep", BodyType: json,
  Body: {conn, sql, params}}`，Headers 加 `X-Dbstep-Token: $dbstep_token`，
  复用 `renderRequestInto`（动态变量/认证/头合并全部免费获得）。
- loopbackURL 来源：`config.Server` → `http://127.0.0.1:{port}`（引擎与平台同机）。
- `step_type` 保存为 `db`（前端展示/校验用），decl 步骤类型也记 db（结果页展示原类型），
  但 YAML 产物是 request——解析器/执行器无需感知 db 存在。

## 校验器

- db 步骤：conn 在本项目 db_source 中存在（projectID 范围查表）、SQL 非空、
  params 为合法 JSON 数组；请求体引用检查照常。
- 写操作警告：SQL 以 INSERT/UPDATE/DELETE/ALTER/DROP 开头且步骤后**没有**任何
  HTTP 步骤时 warning「写库步骤之后没有接口调用，可能造了数却没测」——旁路不阻断。

## 调试台

- `DebugStep`：step_type=db 时不走引擎——服务层直接执行（复用 dbstepsvc 的
  执行函数），返回同构结果（rows/affected/断言不做——调试只看数据）。

## Token 注入点

- `run.go` / `run_suite.go` 准备工作区：生成 token → `.env` 追加
  `dbstep_token=<uuid>` → 注册表登记（runID, projectID）→ 终态关闭。
- 编译产物 header 引用 `$dbstep_token`（引擎运行时替换）。
- debug 路径不注册 token（FR-5 不走引擎）。

## 前端

- `StepEditor.vue`：类型下拉加「数据库」；DB 面板=连接下拉（dbSourceApi.list）
  + SQL textarea（等宽字体）+ params JSON（复用 gql variables 的校验/格式化交互）；
  断言/提取页签照旧（placeholder 提示 `body.rows[0].xxx` / `body.affected`）。
- `EnvironmentListView.vue`：新增「数据库连接」区块（列表/新增/编辑/删除，密码掩码）。
- 类型定义：`DbSource`、`StepReq/TestStep` 的 db 字段、db_source API 封装。

## 验证计划

1. 单测：DSN 构造三驱动、/dbstep 端点（token/项目隔离/SQL 空值/掩码回传）、
   编译产物（POST/头/体/动态变量）、校验器（conn 不存在/空 SQL/写操作警告）；
2. 端到端（真实引擎 + sqlite 文件库）：spec Acceptance 1~7 逐条；
3. `check.sh` 七步门禁。

## 风险与边界

- 引擎与平台**必须同机**（回环 127.0.0.1）——当前执行模型本就如此（executor
  本地起 hrp.exe），文档写明；未来分布式执行时回环端点随执行机走（R7 压测一起考虑）。
- 平台重启后进行中的 run token 表丢失 → token 失效 → db 步骤 401 → error：
  可接受（重启本就会打断进行中的执行）。
- `duration_ms` 命名与步骤耗时字段冲突？回环响应里的 duration_ms 指 SQL 执行耗时，
  引擎步骤耗时由引擎测——无冲突，响应字段改名 `sql_ms` 更稳。
