---
name: hrp-engine-probe
description: 对 HttpRunner v4.3.6（hrp 二进制）做行为实测的正确做法与已固化结论。当需要确认 hrp 的真实行为（日志流、退出码、产物路径、批量执行语义、URL 尾斜杠归一化、validate.check 的 jmespath 语义与数组下标写法、chunked 响应体的原始报文体封帧、遥测导致执行忽快忽慢、错误摘要里冒出 GA4/Sentry、Python 插件），或要为 httprunnerplatform 项目新增/回归引擎探针夹具时使用。含 Git Bash on Windows 生成夹具与后台服务的确定性避坑方式、C 盘满导致编译失败的排查、以及一套已验证的捕获双流的命令模板。
agent_created: true
---

# hrp 引擎行为实测

## 适用场景

- 要确认 `hrp` 的**真实**行为（官方文档多处错误/缺失，不能靠推断）
- 为 `F:\httprunnerplatform` 新增或回归引擎探针夹具
- 调试「测试结果对不上」的问题（假绿、结果丢失、归因错误）

## 关键路径

| 项 | 值 |
|---|---|
| 平台仓库 | `F:\httprunnerplatform` |
| hrp 二进制 | `F:\httprunnerplatform-tools\bin\hrp.exe`（v4.3.6，仓库外，不入 Git） |
| Go 工具链 | `F:\httprunnerplatform-tools\go`（1.27.1） |
| 实测结论归档 | `F:\httprunnerplatform\docs\引擎实测记录.md`（**结论以它为准**） |
| 本地被测服务 | `testdata/local-echo/echo_server.py`，端口 8899（**对任意路径都回 200**；不要再另造 mock） |
| 端到端冒烟 | `F:/httprunnerplatform-e2e/e2e.sh`（仓库外，22 项断言） |

## 铁律：Git Bash on Windows 避坑

这几条都真实踩过，会静默产出错误结果：

1. **不要用 `printf '...\n...'` 生成夹具文件** —— MSYS 会做路径转换，把 `\n` 改写成 `/n`，生成的 YAML 全是畸形内容。
   曾因此把「3 个文件静默丢弃」误判成「扩展名过滤异常」，白跑一轮。
   **生成任何文本夹具一律用写文件工具。** 需要 `rm` 重建时先删再写。
2. **不要用 `nohup ... &` 起后台服务** —— shell 退出即被回收，下次调用就是 connection refused。
   用后台任务方式启动长驻服务。
3. **shell 的 `timeout` 杀不掉 hrp 派生的 Windows 子进程**（`cmd.exe` / `python.exe`）。
   要给 hrp 限时，只能在进程树/Job Object 层面做，否则会留下孤儿进程 + 卡住整个调用。
4. **杀进程用 PowerShell**：`Get-Process | Where-Object { $_.ProcessName -match ... } | Stop-Process -Force`。
   注意**别把正在用的被测服务一起杀掉**（把 `python` 加进匹配列表就会连 echo 服务一起带走）。
   PowerShell 工具里**避免出现 `cmd` 字样**（会被安全策略拦），用 `-match '^(hrp|python|python3)$'` 这类精确匹配。
5. Go env：`source scripts/dev-env.sh`；PATH 必须用 `cygpath -u` 转 POSIX，`GOROOT/GOPATH` 保持 Windows 形式。
6. **Go 的构建缓存与临时目录默认在 C 盘**。C 盘一旦满，`go build` 会以
   `link.exe: resize output file failed: There is not enough space on the disk` 失败——
   报错完全不提磁盘，容易误判成权限或杀软问题。本项目已重定向：
   ```bash
   go env -w GOCACHE=F:/httprunnerplatform-tools/gocache \
             GOTMPDIR=F:/httprunnerplatform-tools/tmp/gotmp
   ```
   遇到**无法解释的编译/链接失败**，先 `df -h /c/` 看一眼 C 盘。
7. **`cat >> file` 挂在 `&&` 链尾时，前面任一命令失败会被短路** ——
   表现为"以为补了测试，其实没写进去"，而 `go test` 还是绿的（因为跑的旧代码）。
   补完测试后一律 `grep -n 'func TestXxx'` 确认真的落盘了。
8. **要写 mock 服务前先找仓库里有没有现成的**。`testdata/local-echo/echo_server.py`
   已经提供 httpbin 风格接口且**对任意路径都回 200**，重复造一个只会引入行为差异
   （曾另造一个对未知路径返回 404 的 mock，结果让"引擎补斜杠"这类现象表现得不一致）。

## 标准捕获模板

**必须分文件捕获 stdout 与 stderr**（两条流内容完全不同，合并就毁掉解析）：

```bash
HRP="F:/httprunnerplatform-tools/bin/hrp.exe"
cd <探针工程目录>            # cwd 就是 hrp 的项目根
timeout 60 "$HRP" run cases/x.yml -s --log-json >"$O/out.txt" 2>"$O/err.txt"
echo "EXIT=$?"
```

- `stderr` → JSON Lines 引擎日志（`grep -o '"message":"[^"]*"'` 看事件序列最有效）
- `stdout` → 纯文本请求/响应明细（`--log-requests-off` 可关）
- 加 `-s` 拿 `results/<ts>/summary.json`；加 `-g` 拿 `results/<ts>/report.html`

## 已固化的结论（不要再重复实测）

| 项 | 结论 |
|---|---|
| 日志流 | 日志 → **stderr**（JSON）；请求/响应明细 → **stdout**（纯文本） |
| `hrp exit N` | 打在 **stdout**，**只在退出码非 0 时出现**（`GetErrorCode` 在 `err==nil` 时提前 return，成功时**没有**这一行）。用途是「印证进程主动退出」，**不是**判断成功 |
| HTML 报告 | `results/<YYYYMMDDHHMMSS>/report.html`，**自包含单文件**；官方文档写的 `reports/...` 是错的 |
| 时间戳精度 | **只到秒** → 同秒并发会落到同一目录互相覆盖 → 必须独立工作区 |
| **断言失败** | **exit=2 且 panic**（`builtin/assertion.go:53` 未初始化 `*testing.common`）；**summary.json / 报告完全不产出** |
| 请求失败 | **exit=1**，无 panic，**summary.json 正常产出**（`success:false`） |
| 目录发现 | 递归子目录；**ASCII 字节序字典序**（`Z_upper` 在 `a_alpha` 前）；白名单 `.yml/.yaml/.json` |
| **假绿** | 畸形文件**静默丢弃**：零 warn、零 error、**退出码 0** |
| **批量执行** | `hrp run <目录>` **全有或全无**：一条断言失败 panic → 后续不执行、整批结果丢失。`-c` 无效 |
| summary.json | `details[].root_dir` **恒为空**、**无源文件路径**；唯一标识是 `config.name` |
| URL 尾斜杠 | 最终 URL **不带查询串**时自动补 `/`；带查询串则不补。⚠️ `params: {}` / `params: null` **也补**，没有写法能规避（详见下方第三轮） |
| **遥测（GA4 + Sentry）** | **默认开启**，无 CLI 开关，只能靠子进程环境变量关。不关则每次执行多花 0.3–3.5 秒（不稳定），且上报失败会被误判成用例错误（详见下方第四轮） |
| `locate plugin failed` | **每用例每次必出的 warn 噪音** → `level=warn` 不等于异常 |
| `debugtalk.py` | 不用 = 零开销；用 = **全局 venv** `~/.hrp/venv` + `funppy` 重依赖，实测**失败/无限挂起** |
| 项目根写入 | hrp 会写 `results/`、`.debugtalk_gen.py`、`__pycache__/` |

### 第二轮实测补充（M1 开发期，见实测记录第 8 节）

| 项 | 结论 |
|---|---|
| **未知字段** | **完全静默忽略**（config/request/teststep 三层验证）：不报错、不警告、只不生效。引擎**零 schema 校验** → 字段白名单必须由平台自己做 |
| **`timeout` 层级** | 只有 **`request.timeout`** 生效（单位秒，可为小数）；`teststep.timeout` **静默忽略** |
| **hooks 层级** | 只有 **teststep 层的 `setup_hooks`/`teardown_hooks`** 生效；放 `request` 层**静默忽略**（比报错危险） |
| **hook 失败退出码** | **23** = `CallFunctionError`（`function xxx is not found`），summary.json **正常产出** |
| **请求超时 vs 连接失败** | **退出码同为 1**（`1=GeneralFail` 是兜底码）。只能靠错误文本区分：超时含 `context deadline exceeded (Client.Timeout exceeded while awaiting headers)`；连接失败含 `dial tcp ... connectex` |
| **`config.parameters`** | 正常生效（`p1: [1,2]` → 请求带 `?p1=1`，取第一个数据集） |
| **失败步骤的 summary 形状** | **没有 `data` 段**；错误文本在顶层 **`attachments`** 字段。成功步骤才有 `data.req_resps` + `data.validators` |
| **`hrp exit N` 污染** | 请求失败时 stdout 只有 request 块 + `hrp exit 1` → 解析器必须把 `^hrp exit \d+$` 当**块终止符**，否则它变成"请求体" |
| **步骤状态判据** | 断言失败会 panic → **不会有配对的 `run step end`**。故"有 end 且 success=false" ⇒ 步骤没跑完（error）；"无 end" ⇒ fail |

### 第三轮：URL 尾斜杠的**不可规避性**（夹具 `testdata/slash-probe/cases/s3_params_empty.yml`）

补斜杠的触发条件是「**最终 URL 没有 query string**」，与 `params` 块**在不在**无关：

| 写法 | 实际发出 |
|---|---|
| `url: $base_url/get` | `GET /get/` ← 补 |
| `url: $base_url/get` + `params: {}` | `GET /get/` ← **照样补** |
| `url: $base_url/get` + `params:`（null） | `GET /get/` ← **照样补** |
| `url: $base_url/get` + `params: {a: 1}` | `GET /get?a=1` |
| `url: $base_url/get?inline=1` | `GET /get?inline=1` |
| `url: $base_url/get/` | `GET /get/`（本来就带斜杠，无变化） |

**推论**：唯一规避方式是**补一个真实存在的查询串**或让路径以 `/` 结尾。
`params: {}` 这条"看起来能骗过引擎"的路是死的 —— 别再试。

⚠️ **复现这条必须用 `testdata/local-echo/echo_server.py`**（任意路径都回 200）。
换一个对 `/anything/abc/` 返回 404 的服务，第一步就断言失败触发 failfast，
后面几步根本不执行，会误以为"只补了一次"。

### 第四轮 ⭐⭐⭐ 遥测（A20）：默认开启，既慢又会污染错误摘要

**开关只有环境变量，没有 CLI 参数**（`hrp run --help` 里没有任何 telemetry flag）：

```go
// hrp/internal/env/env.go —— 包级 var，在 init 阶段就读掉了
DISABLE_GA     = os.Getenv("DISABLE_GA")
DISABLE_SENTRY = os.Getenv("DISABLE_SENTRY")
// hrp/internal/sdk/ga4.go
if env.DISABLE_GA == "true" { return }        // ← 字符串相等比较，不是布尔解析
log.Error().Err(err).Msg("send GA4 event failed")   // ← 用 error 级打日志
```

三个必须记住的点：

1. 值必须是**字面量 `"true"`**：写 `1` / `TRUE` / `yes` 一律无效。
2. 必须在**子进程启动前**设好（包初始化阶段读取，运行中 `os.Setenv` 无效）。
3. **注入要用覆盖式**，不能直接 `append`：父进程已有同名键时，Windows 的
   `GetEnvironmentVariable` 取**第一个**匹配项，平台注入的值会被旧值顶掉。

**两笔账**（夹具 `testdata/telemetry-probe/cases/{pass,dead,fail_assert}.yml`，各跑 3 次取最快）：

| 夹具（退出码） | 引擎默认 | 两个都关 |
|---|---|---|
| `pass.yml`（0 通过） | 1958 ms | 1490 ms |
| `dead.yml`（1 请求失败） | 1741 ms | 1487 ms |
| `fail_assert.yml`（2 断言失败 panic） | 2200 ms | 1482 ms |

- **账一（慢，且不稳定）**：GA4 的 HTTP client 超时 **5 秒**。默认那列不同轮次能跑到
  1.3s–5.1s（取决于当时外网可达性），内网直接跑满。关掉后三条路径都稳定在 ~1.48s。
  → **判断这条时要看"是否稳定"，不能只看一次数字。**
- **账二（更糟）**：上报失败是 `log.Error()` 打的，在 stderr 里与"用例失败"**完全同形**。
  实测出现过一条 **`status=pass` 的成功执行，`error_msg` 却是
  `request GA4 failed: ... context deadline exceeded`**。
  按"最后一条 error 级日志"取错误摘要的解析器一定会踩到。

**正确做法**：起子进程时注入 `DISABLE_GA=true` / `DISABLE_SENTRY=true`（治本）；
解析器另按**前缀**过滤 `request GA4 failed` / `send GA4 event failed` /
`init sentry sdk failed` 作为兜底（用户显式打开遥测时仍会踩）。
只匹配前缀而不搜关键词：用户用例自己去请求 `google-analytics.com` 时报错文本是
`do request failed: Get "https://..."`，前缀不同，不会误伤。

> **元教训**：这个坑的成因不是"引擎没说"，而是配置文件里早写了
> "引擎会读取 DISABLE_GA / DISABLE_SENTRY"，**代码却从未接线**。
> 配置项只要没有测试断言它真的生效，就等于没有实现。

### 第五轮 ⭐⭐ `validate[].check` 走 **jmespath**：数组下标必须用方括号

**结论**：`check` 不是"平台自定义的点号路径"，而是交给 **jmespath** 求值的。

| 写法 | 引擎 |
|---|---|
| `body.data[0].name` | ✅ 唯一合法 |
| `body.data.0.name` | ❌ `SyntaxError: Expected identifier, lbracket, or lbrace` → `search jmespath failed` → **立即 panic** |

**探针法（比走平台快得多）**：把多个语法候选写成只差一个字符的最小 YAML，
直接跑 `hrp run cases/x.yml --log-json 2>&1 | grep -E 'checkExpr\|SyntaxError\|AssertionError'` 对比。

```yaml
validate:
    - eq: ["body.data[0].name", "Bitcoin"]   # ← 方括号：合法
```

成功时引擎日志会把判定全给出来，可直接当断言依据：

```
{"checkExpr":"body.data[0].name","assertMethod":"eq","expectValue":"Bitcoin",
 "expectValueType":"string","checkValue":"Bitcoin","checkValueType":"string","result":true,
 "message":"validate body.data[0].name"}
```

**配套事实：chunked 响应的「原始报文体」带分块封帧。**
httpx 打印的是原始报文，`Transfer-Encoding: chunked` 的体长这样：

```
4904                          ← 十六进制块长度
{ "data": [ ... ] }
0                             ← 结束块
```

⚠️ `json.Decode` 会把首行的 `4904` **当成一个裸数字解析成功**（不报错！），
于是 root 变成 `json.Number`，**所有 `body.*` 路径全部取不到值**。
症状与"数组下标解析不了"完全相同（实际值恒为 nil）但根因无关 ——
按路径取值前必须先判断体是否以 `{`/`[` 开头，否则抠出最外层 JSON 再解析。

> **元教训**：这两个坑叠加在一起时，只修其中一个仍然拿不到值。
> 排查"平台重建的实际值恒为空"这类问题时，**要把"路径切分"和"体解析"当成两个独立疑点分别验证**，
> 不要看到症状一样就认定同一个根因。
>
> 该夹具已入库：`internal/parser/testdata/assert_fail_https/`（真实执行产物，
> `exit_code=2`、无 summary、chunked 传输，一次覆盖两个根因）。

### ⭐ 官方退出码表（`hrp/internal/code/code.go`，权威，不要再猜）

拉取方式：`https://raw.githubusercontent.com/httprunner/httprunner/v4.3.6/hrp/internal/code/code.go`
（注意 `curl` 在本机走 schannel 会 `server closed abruptly`，用 WebFetch 工具取）

```text
general   [0,2)   0 Success / 1 GeneralFail（兜底码，任何未定义错误都落这里）
env       [2,10)  只有 9 = InvalidPython3Venv；2-8 预留未分配
loader    [10,20) 10 文件 / 11 JSON / 12 YAML / 13 .env / 14 CSV /
                  15 用例格式 / 16 扩展名 / 17 引用文件缺失 / 18 插件文件非法
parser    [20,30) 20 ParseError / 21 VariableNotFound / 22 ParseFunctionError /
                  23 CallFunctionError / 24 ParseVariablesError
runner    [30,40) 31 InitPluginFailed / 32 BuildGoPluginFailed /
                  33 BuildPyPluginFailed / 38 InterruptError / 39 TimeoutError
summary   [40,50) 空
移动端/CV  [50,90) iOS 50-56 / Android 60-66 / UI 70-78 / CV 80-85
```

**推论**：
- `exit=2` **不是环境码**，实测中出现的 `2` 全部来自 Go panic → **`2` 实质是 panic 签名**
- `38`/`39` 住在 runner 区间但语义是**中断/超时** → 按区间一刀切成"执行机问题"是**实质错误**
- `1` 不携带分类信息 → 必须按错误文本二次分流

## 生成回归夹具的正确姿势

要给解析器/执行器做回归基准，**必须隔离采集**（否则会串台）：

```bash
WORK=/tmp/probe-run
run() {  # name srcdir file
  name=$1; src=$2; file=$3
  rm -rf "$WORK"; cp -r "$ROOT/testdata/$src" "$WORK"; rm -rf "$WORK/results"
  mkdir -p "$OUT/$name"
  ( cd "$WORK" && hrp run "$file" -s --log-json >"$OUT/$name/stdout.txt" 2>"$OUT/$name/stderr.txt" )
  echo -n "$?" > "$OUT/$name/exit_code"
  s=$(find "$WORK/results" -name summary.json | head -1); [ -n "$s" ] && cp "$s" "$OUT/$name/summary.json"
}
```

**踩过的坑**：直接在原探针目录跑多次、再用 `find ... -newermt` 回收 summary.json，
会**跨场景串台**（`assert_fail` 本应「无 summary」，却捞到了别的场景的文件，
一度让我误以为 F8 结论不成立）。**每次运行都要用全新副本，并先 `rm -rf results`。**


## 新增探针夹具的约定

放在 `testdata/<probe-name>/`，每个工程自包含：

```
testdata/<probe-name>/
├── .env        # base_url=http://127.0.0.1:8899   ← 夹具本体，.gitignore 已开例外
├── proj.json   # {"hrp_version": "v4.3.6"}
└── cases/
```

- 只用**一个断言维度**做一个探针，便于归因
- 用例文件名加序号前缀（顺序即 ASCII 序）
- 跑完清理运行期产物：`find testdata -type d -name results -prune -exec rm -rf {} +`（同样处理 `__pycache__`、`.debugtalk_gen.py`）

## 验证清单

改完探针或阅读结论前，跑一遍确认环境没坏：

```bash
cd /f/httprunnerplatform && source scripts/dev-env.sh

curl -s -m 3 "http://127.0.0.1:8899/get?ping=1"        # 被测服务存活
./httprunnerplatform.exe doctor                          # hrp 二进制可被探测
go test ./...                                            # 解析器/编译器回归（用完夹具，不需要 hrp）
```

**端到端冒烟（首选）**：

```bash
bash /f/httprunnerplatform-e2e/e2e.sh
# 22 项断言；自举 echo 服务、构建并起平台、跑完建项目→建环境→建用例→校验→
# YAML 预览→执行→步骤/断言明细→报告→日志→错误码的完整链路。
# 每次运行使用全新数据根（$E2E/run/<时间戳>），不依赖删除旧目录。
```

> ⚠️ 该脚本在**仓库外**（`F:/httprunnerplatform-e2e/`），新克隆的仓库里没有 ——
> 它含本机路径（Go 工具链、managed Python）。若要纳入版本管理需要先参数化路径。

另有一条更细粒度的：把编译器产物交给真引擎跑（需要 echo 服务在 8899）

```bash
HRP_PLATFORM_E2E=1 go test ./internal/compiler/ -run E2E -v
```

> **判断「我的改动是否真的被引擎接受」的唯一权威方式是 E2E 测试**：
> 单元测试只能证明 YAML 合法，证明不了引擎认。字段放错层级时引擎**不报错**，
> 所以"编译通过 + 单测绿"完全不能说明产物可用。
