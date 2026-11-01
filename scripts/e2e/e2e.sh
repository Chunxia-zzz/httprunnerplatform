#!/usr/bin/env bash
# 平台 API 端到端冒烟
#   建项目 → 建环境 → 建用例 → 校验 → 预览 YAML → 执行 → 看步骤/断言 → 报告/日志
#
# 用法：bash scripts/e2e/e2e.sh   （在仓库内任意位置，路径自适应）
#
# 可用环境变量：
#   HRP_E2E_ROOT   数据根目录（默认 F:/httprunnerplatform-e2e，历史遗留位置，
#                  每次运行在其下新建 run/<时间戳>；换机器时指到任意可写目录即可）
#   HRP_PYTHON     mock 服务用的 python（默认依次找 PATH 里的 python / python3）
#
# 设计约束：
#   1. 每次运行使用**全新的数据根**（$E2E/run/<时间戳>），不依赖删除旧目录——
#      清理动作会触碰安全策略，脚本必须能在"什么都不能删"的环境里跑通。
#   2. 被测 mock 服务自举：端口不通就自己拉起来。
#   3. 任何一次取字段失败都立即失败并打印原始响应，绝不带着空变量往下跑。
#
# 2026-11-01 从 F:/httprunnerplatform-e2e/e2e.sh 迁入仓库（原位置只留数据）。
set -u

# 仓库根：脚本位于 scripts/e2e/ 下，上两级即仓库根。不再硬编码盘符路径。
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
E2E="${HRP_E2E_ROOT:-F:/httprunnerplatform-e2e}"
BASE="http://127.0.0.1:18080/api/v1"
MOCK_PORT=8899
MOCK="http://127.0.0.1:$MOCK_PORT"

# python：优先环境变量，其次 PATH；WorkBuddy 自带 python 作为最后兜底。
if [ -n "${HRP_PYTHON:-}" ]; then
  PY="$HRP_PYTHON"
elif command -v python >/dev/null 2>&1; then
  PY="$(command -v python)"
elif command -v python3 >/dev/null 2>&1; then
  PY="$(command -v python3)"
else
  PY="C:/Users/Administrator/.workbuddy/binaries/python/versions/3.13.12/python.exe"
fi

# Go 工具链：走仓库既定的 dev-env.sh 约定（HRP_PLATFORM_TOOLS，默认仓库同级）。
: "${HRP_BINARY_PATH:=}"
# shellcheck disable=SC1091
source "$ROOT/scripts/dev-env.sh" >/dev/null

TS=$(date +%Y%m%d-%H%M%S)
DATA="$E2E/run/$TS"
COOKIE="$DATA/cookie.txt"
mkdir -p "$DATA" || exit 1

SRV_PID=""
MOCK_PID=""
FAIL=0
PASS=0

cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null
  if [ -n "$MOCK_PID" ]; then kill "$MOCK_PID" 2>/dev/null; fi
}
trap cleanup EXIT

# ---------------------------------------------------------------- 工具函数

# jget <json> <a.b.c>    数字段视为数组下标；任何一步取不到就报错退出
jget() {
  printf '%s' "$1" | "$PY" -c '
import sys, json
raw = sys.stdin.read()
try:
    d = json.loads(raw)
except Exception as e:
    sys.stderr.write("jget: 响应不是合法 JSON (%s)\n原始响应: %s\n" % (e, raw[:800]))
    sys.exit(2)
for k in sys.argv[1].split("."):
    if d is None:
        sys.stderr.write("jget: 路径 %s 中途遇到 null\n原始响应: %s\n" % (sys.argv[1], raw[:800]))
        sys.exit(3)
    if k.lstrip("-").isdigit():
        d = d[int(k)]
    elif isinstance(d, dict) and k in d:
        d = d[k]
    else:
        sys.stderr.write("jget: 路径 %s 的 %r 段不存在\n原始响应: %s\n" % (sys.argv[1], k, raw[:800]))
        sys.exit(4)
print(d)
' "$2"
}

# must <键路径>  —— 从 stdin 读响应并取字段；取不到就返回 1（错误信息走 stderr，不污染 stdout）
must() {
  local key="$1" resp out
  resp=$(cat)
  if ! out=$(jget "$resp" "$key"); then
    echo "❌ 取字段 $key 失败" >&2
    return 1
  fi
  printf '%s' "$out"
}

ok()   { PASS=$((PASS + 1)); echo "  ✅ $*"; }
bad()  { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

# expect_code <期望> <响应>
expect_code() {
  local want="$1" got
  got=$(jget "$2" code 2>/dev/null) || got="<解析失败>"
  if [ "$got" = "$want" ]; then ok "code=$want"; else bad "期望 code=$want，实际 code=$got | $2"; fi
}

# expect_http <期望> <url> [额外 curl 参数...]
expect_http() {
  local want="$1" url="$2"; shift 2
  local got
  got=$(curl -s -o /dev/null -w '%{http_code}' "$@" "$url")
  if [ "$got" = "$want" ]; then ok "HTTP $want"; else bad "期望 HTTP $want，实际 $got ($url)"; fi
}

step() { echo; echo "======== $* ========"; }

# 轮询执行到终态
# wait_run <run_id>  → 回显终态
wait_run() {
  local id="$1" st="" d=""
  for _ in $(seq 1 120); do
    d=$(curl -s -b "$COOKIE" "$BASE/runs/$id")
    st=$(jget "$d" data.run.status 2>/dev/null) || st=""
    [ -n "$st" ] || break
    [ "$st" = "running" ] || [ "$st" = "queued" ] || break
    sleep 0.5
  done
  printf '%s' "$st"
}

echo "数据根: $DATA"

# ---------------------------------------------------------------- 被测服务
step "准备被测服务（$MOCK）"
# 复用仓库自带的 echo 服务（testdata/local-echo/echo_server.py）：
# 它对**任意路径**都返回 200，因此不会因为「结尾斜杠」这类细节让冒烟结果产生歧义；
# 而且它是入库版本管理的夹具，不在这里另造一个功能重叠的实现。
if curl -s -m 2 -o /dev/null "$MOCK/get"; then
  echo "  复用已在监听的 echo 服务"
else
  "$PY" "$ROOT/testdata/local-echo/echo_server.py" "$MOCK_PORT" >"$DATA/echo.log" 2>&1 &
  MOCK_PID=$!
  for _ in $(seq 1 30); do
    curl -s -m 1 -o /dev/null "$MOCK/get" && break
    sleep 0.3
  done
  echo "  已启动 local-echo (pid=$MOCK_PID)"
fi
echo "  探针: $(curl -s -m 3 "$MOCK/get?probe=1" | head -c 120)"

# ---------------------------------------------------------------- 启动服务
step "构建并启动平台服务（$BASE）"
cd "$ROOT" || exit 1
# 二进制固定放在 $E2E/bin 下复用，不放进每次运行的数据根：
# 它约 40MB，按运行各存一份纯属浪费（之前 4 次运行就攒了 160MB）。
BIN="$E2E/bin"
mkdir -p "$BIN"
if ! go build -o "$BIN/server.exe" ./cmd/server 2>"$DATA/build.log"; then
  echo "构建失败："; cat "$DATA/build.log"; exit 1
fi
echo "  构建 OK"

export HRP_PLATFORM_SERVER_PORT=18080
export HRP_PLATFORM_DB_DRIVER=sqlite
export HRP_PLATFORM_DB_DSN="$DATA/platform.db"
export HRP_PLATFORM_WORKSPACE_ROOT="$DATA/ws"
export HRP_PLATFORM_LOG_LEVEL=warn

"$BIN/server.exe" server >"$DATA/server.log" 2>&1 &
SRV_PID=$!

for _ in $(seq 1 60); do
  curl -s -m 1 -o /dev/null "$BASE/healthz" && break
  sleep 0.3
done
if ! curl -s -m 2 "$BASE/healthz" | grep -q '"status":"ok"'; then
  echo "服务未就绪，日志："; cat "$DATA/server.log"; exit 1
fi
echo "  healthz OK (pid=$SRV_PID)"

# ---------------------------------------------------------------- 认证
step "未登录访问受保护接口（应 HTTP 401）"
expect_http 401 "$BASE/projects"

step "登录"
LOGIN=$(curl -s -c "$COOKIE" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' "$BASE/auth/login")
expect_code 0 "$LOGIN"

step "GET /auth/me"
curl -s -b "$COOKIE" "$BASE/auth/me"; echo

step "GET /engine/status"
ENGINE=$(curl -s -b "$COOKIE" "$BASE/engine/status")
echo "$ENGINE"
VERS=$(jget "$ENGINE" data.version 2>/dev/null) || VERS=""
[ -n "$VERS" ] && ok "引擎版本 $VERS" || bad "引擎不可用"

# ---------------------------------------------------------------- 项目
step "创建项目"
PROJ=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d '{"code":"e2e","name":"端到端项目","description":"冒烟"}' "$BASE/projects")
echo "$PROJ"
PID=$(must data.id <<<"$PROJ") || exit 1
echo "  project_id=$PID"

step "项目工作区目录树（应含 api/testcases/suites/data）"
find "$DATA/ws/workspaces/e2e" -maxdepth 1 2>/dev/null | sed 's#^#  #'

step "创建项目：code 非法（应 HTTP 400 + code 40004）"
BAD=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d '{"code":"../bad","name":"x"}' "$BASE/projects")
expect_http 400 "$BASE/projects" -b "$COOKIE" -H 'Content-Type: application/json' -d '{"code":"../bad","name":"x"}'
expect_code 40004 "$BAD"

step "创建项目：重复 code（应 code 40001）"
expect_code 40001 "$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d '{"code":"e2e","name":"再来一次"}' "$BASE/projects")"

# ---------------------------------------------------------------- 环境
step "创建环境"
ENVJSON=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d "{\"name\":\"local\",\"base_url\":\"$MOCK\",\"environs\":{\"api_key\":\"super-secret\",\"plain\":\"v\"},\"global_headers\":{\"X-From\":\"hrp\"},\"verify_ssl\":false,\"is_default\":true}" \
  "$BASE/projects/$PID/environments")
echo "$ENVJSON"
EID=$(must data.id <<<"$ENVJSON") || exit 1
echo "  env_id=$EID"

step "环境列表"
curl -s -b "$COOKIE" "$BASE/projects/$PID/environments"; echo

# ---------------------------------------------------------------- 用例
step "创建用例 A（应通过，两步 + 提取变量）"
# ⚠️ 步骤 2 刻意带上 params：实测 A1 证明「URL 不带 query」的请求会被引擎
# 补上结尾斜杠（/get → /get/），mock 服务会因此 404。这不是平台 bug，
# 而是引擎行为；平台已用 TRAILING_SLASH_ADDED 预警把它显性化（见用例 D）。
CASE_A=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' -d '{
  "code":"tc_pass","name":"通过用例","module":"冒烟","priority":"P0","tags":"smoke",
  "config":{"verify":false,"variables":{"who":"tester"}},
  "steps":[
    {"seq":1,"step_type":"request","name":"步骤1 取列表","enabled":true,
     "request":{"method":"GET","url":"$base_url/get","params":{"who":"tester"}},
     "extract":[{"name":"m","object":"body","expression":"method"}],
     "validate":[{"check":"status_code","assert":"eq","expect":200},
                 {"check":"body.method","assert":"eq","expect":"GET"}]},
    {"seq":2,"step_type":"request","name":"步骤2 复用提取值","enabled":true,
     "request":{"method":"GET","url":"$base_url/get","params":{"m":"$m"}},
     "validate":[{"check":"status_code","assert":"eq","expect":200},
                 {"check":"body.args.m","assert":"eq","expect":"GET"}]}
  ]}' "$BASE/projects/$PID/cases")
echo "$CASE_A" | head -c 300; echo
AID=$(must data.id <<<"$CASE_A") || exit 1
echo "  case_id(A)=$AID"

step "创建用例 B（断言 201 但实际 200，引擎断言失败）"
CASE_B=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' -d '{
  "code":"tc_fail","name":"断言失败用例","module":"冒烟","priority":"P1",
  "steps":[
    {"seq":1,"step_type":"request","name":"步骤1 期望201却返回200","enabled":true,
     "request":{"method":"GET","url":"$base_url/get"},
     "validate":[{"check":"status_code","assert":"eq","expect":201}]}
  ]}' "$BASE/projects/$PID/cases")
echo "$CASE_B" | head -c 200; echo
BID=$(must data.id <<<"$CASE_B") || exit 1
echo "  case_id(B)=$BID"

step "创建用例 C（未定义变量 + 禁用步骤，供校验器拦截）"
CASE_C=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' -d '{
  "code":"tc_bad","name":"有问题的用例","module":"冒烟",
  "steps":[
    {"seq":1,"step_type":"request","name":"引用了不存在的变量","enabled":true,
     "request":{"method":"GET","url":"$base_url/$nope","headers":{"X-T":"$missing_var"}},
     "validate":[{"check":"status_code","assert":"eq","expect":200}]},
    {"seq":2,"step_type":"request","name":"被禁用的步骤","enabled":false,
     "request":{"method":"GET","url":"$base_url/skipped"}}
  ]}' "$BASE/projects/$PID/cases")
echo "$CASE_C" | head -c 200; echo
CID=$(must data.id <<<"$CASE_C") || exit 1
echo "  case_id(C)=$CID"

step "创建用例 D（URL 不带 query，触发引擎结尾斜杠预警）"
CASE_D=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' -d '{
  "code":"tc_slash","name":"结尾斜杠预警用例","module":"冒烟","priority":"P2",
  "steps":[
    {"seq":1,"step_type":"request","name":"裸路径无 query","enabled":true,
     "request":{"method":"GET","url":"$base_url/get"},
     "validate":[{"check":"status_code","assert":"eq","expect":200}]}
  ]}' "$BASE/projects/$PID/cases")
DID=$(must data.id <<<"$CASE_D") || exit 1
echo "  case_id(D)=$DID"

step "用例列表（step_count 只数启用步骤）"
CLIST=$(curl -s -b "$COOKIE" "$BASE/projects/$PID/cases")
echo "$CLIST" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
print('total =', d['total'])
for c in d['list']:
    print('  %-8s %-14s module=%-4s pri=%-3s steps=%s status=%s' % (
        c['code'], c['name'], c['module'], c['priority'], c['step_count'], c['status']))
"
CNT_C=$(echo "$CLIST" | "$PY" -c "
import sys, json
for c in json.load(sys.stdin)['data']['list']:
    if c['code'] == 'tc_bad': print(c['step_count'])
")
[ "$CNT_C" = "1" ] && ok "禁用步骤未计入 step_count（=1）" || bad "tc_bad step_count=$CNT_C，期望 1"

step "用例树"
curl -s -b "$COOKIE" "$BASE/projects/$PID/cases/tree"; echo

# ---------------------------------------------------------------- YAML 预览
step "GET /cases/{A}/yaml（应与实际执行逐字节一致）"
curl -s -b "$COOKIE" "$BASE/cases/$AID/yaml?env_id=$EID" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
print('filename:', d['filename'])
print('--- yaml ---'); print(d['yaml'])
print('--- env ---'); print(d['env'])
"

# ---------------------------------------------------------------- 校验
step "POST /cases/{A}/validate（应 ok）"
VA=$(curl -s -b "$COOKIE" -X POST "$BASE/cases/$AID/validate?env_id=$EID")
expect_code 0 "$VA"
echo "$VA" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
print('  ok =', d['ok'], '| issues =', len(d['issues']))
"

step "POST /cases/{C}/validate（应 code 50004 + issues）"
VC=$(curl -s -b "$COOKIE" -X POST "$BASE/cases/$CID/validate?env_id=$EID")
expect_code 50004 "$VC"
echo "$VC" | "$PY" -c "
import sys, json
r = json.load(sys.stdin)
print('  ok =', r['data']['ok'])
for i in r['data']['issues']:
    print('  [%s] %s (scope=%s, seq=%s, field=%s)' % (i['level'], i['code'], i['scope'], i['seq'], i['field']))
    print('       %s' % i['message'])
    print('       hint: %s' % i['hint'])
"

step "POST /cases/{D}/validate（应 code 0 + TRAILING_SLASH_ADDED 预警）"
VD=$(curl -s -b "$COOKIE" -X POST "$BASE/cases/$DID/validate?env_id=$EID")
expect_code 0 "$VD"
if echo "$VD" | "$PY" -c "
import sys, json
r = json.load(sys.stdin)['data']
print('  ok =', r['ok'], '(warning 级不阻断保存与执行)')
codes = [i['code'] for i in r['issues']]
print('  codes =', codes)
for i in r['issues']:
    print('  [%s] %s (seq=%s, field=%s)' % (i['level'], i['code'], i['seq'], i['field']))
    print('       %s' % i['message'])
    print('       hint: %s' % i['hint'])
assert 'TRAILING_SLASH_ADDED' in codes, '缺少结尾斜杠预警：%s' % codes
"; then
  ok "结尾斜杠预警命中"
else
  bad "结尾斜杠预警未命中"
fi

# ---------------------------------------------------------------- 执行 A
step "POST /runs 执行用例 A"
RUN_A=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d "{\"project_id\":$PID,\"target_type\":\"case\",\"target_id\":$AID,\"env_id\":$EID}" "$BASE/runs")
echo "$RUN_A"
RA=$(must data.run_id <<<"$RUN_A") || exit 1
STA=$(wait_run "$RA")
echo "  run_id=$RA 最终状态=$STA"
[ "$STA" = "success" ] && ok "用例 A 执行成功" || bad "用例 A 最终状态=$STA，期望 success"

step "GET /runs/{A} 详情"
DA=$(curl -s -b "$COOKIE" "$BASE/runs/$RA")
if echo "$DA" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
r, c = d['run'], d['cases'][0]
print('run : status=%s attribution=%s expected=%s actual=%s mismatch=%s duration=%sms' % (
    r['status'], r['attribution'], r['expected_case_count'], r['actual_case_count'],
    r['count_mismatch'], r['duration_ms']))
print('      error_msg=%r' % r['error_msg'])
print('case: status=%s exit=%s panic=%s clean_exit=%s has_report=%s has_summary=%s' % (
    c['status'], c['exit_code'], c['panic'], c['clean_exit'], c['has_report'], c['has_summary']))
print('      attribution=%s (%s)' % (c['attribution'], c['attribution_label']))
print('      step_total=%s passed=%s failed=%s error=%s' % (
    c['step_total'], c['step_passed'], c['step_failed'], c['step_error']))
assert r['error_msg'] == '', '成功的执行不该带错误摘要（遥测噪音泄露）: %s' % r['error_msg']
"; then
  ok "成功执行没有错误摘要（遥测已在子进程环境里关掉）"
else
  bad "成功执行却带着错误摘要"
fi

step "遥测回归：stderr 里不该出现 GA4 / Sentry"
curl -s -b "$COOKIE" "$BASE/runs/$RA/logs" >"$DATA/logs_a.json"
if grep -qi "GA4\|sentry" "$DATA/logs_a.json"; then
  bad "stdout/stderr 里出现了遥测噪音（DISABLE_GA / DISABLE_SENTRY 未生效）"
else
  ok "双流日志里没有遥测噪音"
fi

step "GET /runs/{A}/cases 步骤与断言明细"
curl -s -b "$COOKIE" "$BASE/runs/$RA/cases" | "$PY" -c "
import sys, json
for cs in json.load(sys.stdin)['data']:
    print('用例 %s (%s) status=%s attribution=%s' % (cs['case_code'], cs['config_name'], cs['status'], cs['attribution']))
    for st in cs['steps']:
        print('  步骤 %s %-22s status=%-6s inferred_failed=%-5s elapsed=%sms' % (
            st['seq'], st['step_name'], st['status'], st['inferred_failed'], st['elapsed_ms']))
        print('    final_url=%s' % st['final_url'])
        for a in st['assertions']:
            print('    断言 rebuilt=%-5s passed=%-5s %s %s | 期望 %s / 实际 %s %s' % (
                a['rebuilt'], a['passed'], a['check_expr'], a['assert_method'],
                a['expect_value'], a['check_value'], a['msg']))
"

step "GET /runs/{A}/report（应 HTTP 200 + text/html）"
# 注意：引擎的报告模板直接以 <head> 开头，没有 <!DOCTYPE> / <html>，
# 所以判据用「响应头 content-type + 报告标题」，不能靠找 <html> 标签。
RPT=$(curl -s -b "$COOKIE" -o "$DATA/report_a.html" \
  -w 'HTTP %{http_code}  type=%{content_type}  bytes=%{size_download}' \
  "$BASE/runs/$RA/report")
echo "  $RPT"
case "$RPT" in
  *"HTTP 200"*"text/html"*) ok "报告响应头正确（text/html）" ;;
  *) bad "报告响应头异常：$RPT" ;;
esac
if grep -q '<title>TestReport</title>' "$DATA/report_a.html"; then
  ok "报告内容是引擎真实报告（含 TestReport 标题），可页面内直接内嵌"
else
  bad "报告内容不含 TestReport 标题，可能是错误页"
fi

step "GET /runs/{A}/logs"
curl -s -b "$COOKIE" "$BASE/runs/$RA/logs" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
print('  truncated =', d['truncated'])
print('  stdout 前 300 字:'); print(d['stdout'][:300])
print('  stderr 末 300 字:'); print(d['stderr'][-300:])
"

# ---------------------------------------------------------------- 执行 B
step "POST /runs 执行用例 B（断言失败）"
RUN_B=$(curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d "{\"project_id\":$PID,\"target_type\":\"case\",\"target_id\":$BID,\"env_id\":$EID}" "$BASE/runs")
RB=$(must data.run_id <<<"$RUN_B") || exit 1
STB=$(wait_run "$RB")
echo "  run_id=$RB 最终状态=$STB"
[ "$STB" = "failed" ] && ok "断言失败被正确判为 failed" || bad "用例 B 最终状态=$STB，期望 failed"

step "GET /runs/{B} 详情（归因应为 system_under_test）"
curl -s -b "$COOKIE" "$BASE/runs/$RB" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
r, c = d['run'], d['cases'][0]
print('run : status=%s attribution=%s expected=%s actual=%s mismatch=%s' % (
    r['status'], r['attribution'], r['expected_case_count'], r['actual_case_count'], r['count_mismatch']))
print('case: status=%s exit_code=%s panic=%s clean_exit=%s has_report=%s has_summary=%s' % (
    c['status'], c['exit_code'], c['panic'], c['clean_exit'], c['has_report'], c['has_summary']))
print('      attribution=%s (%s)' % (c['attribution'], c['attribution_label']))
print('      reason=%s' % c['attribution_reason'])
print('      step_total=%s passed=%s failed=%s error=%s' % (
    c['step_total'], c['step_passed'], c['step_failed'], c['step_error']))
print('      error_msg=%s' % c['error_msg'])
"

step "GET /runs/{B}/cases（从日志重建的步骤与断言）"
curl -s -b "$COOKIE" "$BASE/runs/$RB/cases" | "$PY" -c "
import sys, json
for cs in json.load(sys.stdin)['data']:
    for st in cs['steps']:
        print('步骤 %s %-22s status=%-6s inferred_failed=%s elapsed=%sms' % (
            st['seq'], st['step_name'], st['status'], st['inferred_failed'], st['elapsed_ms']))
        print('   final_url=%s' % st['final_url'])
        print('   request_snapshot =%s' % json.dumps(st.get('request_snapshot'), ensure_ascii=False)[:150])
        print('   response_snapshot=%s' % json.dumps(st.get('response_snapshot'), ensure_ascii=False)[:150])
        for a in st['assertions']:
            print('   断言 rebuilt=%-5s passed=%-5s %s %s | 期望 %s / 实际 %s' % (
                a['rebuilt'], a['passed'], a['check_expr'], a['assert_method'],
                a['expect_value'], a['check_value']))
        print('   error_msg=%s' % st['error_msg'])
"

step "GET /runs/{B}/report（断言失败时不产报告，应 HTTP 404 + code 50006）"
expect_http 404 "$BASE/runs/$RB/report" -b "$COOKIE"
curl -s -b "$COOKIE" "$BASE/runs/$RB/report"; echo

# ---------------------------------------------------------------- 列表 / 终态取消 / 错误码
step "GET /runs 列表"
curl -s -b "$COOKIE" "$BASE/runs?project_id=$PID" | "$PY" -c "
import sys, json
d = json.load(sys.stdin)['data']
print('  total =', d['total'])
for r in d['list']:
    print('  run %s %-10s %-10s total=%s passed=%s failed=%s error=%s mismatch=%s %sms' % (
        r['id'], r['target_name'], r['status'], r['total'], r['passed'],
        r['failed'], r['error'], r['count_mismatch'], r['duration_ms']))
"

step "POST /runs/{A}/cancel（已是终态，应 code 40003）"
expect_code 40003 "$(curl -s -b "$COOKIE" -X POST "$BASE/runs/$RA/cancel")"

step "GET /runs/{A}/report?case_result_id=999999（应 code 40002）"
expect_code 40002 "$(curl -s -b "$COOKIE" "$BASE/runs/$RA/report?case_result_id=999999")"

step "DELETE /cases/999999（不存在，应 code 40002）"
expect_code 40002 "$(curl -s -b "$COOKIE" -X DELETE "$BASE/cases/999999")"

step "DELETE /projects/{PID}（有执行记录在跑/存在，应被拒或成功）"
echo "$(curl -s -b "$COOKIE" -X DELETE "$BASE/projects/$PID")"

step "日志噪音检查（正常 404 不应留下 record not found）"
NOISE=$(grep -c "record not found" "$DATA/server.log" 2>/dev/null || true)
NOISE=${NOISE:-0}
if [ "$NOISE" = "0" ]; then
  ok "日志里没有 record not found（GORM IgnoreRecordNotFoundError 生效）"
else
  bad "日志里有 $NOISE 行 record not found：$(grep -n 'record not found' "$DATA/server.log" | head -3)"
fi

# ---------------------------------------------------------------- 汇总
echo
echo "======== 服务端日志（末 25 行）========"
tail -25 "$DATA/server.log"

echo
echo "======== 产物目录 ========"
find "$DATA" -maxdepth 6 -type f 2>/dev/null | head -40

echo
echo "======== 结果：通过 $PASS 项 / 失败 $FAIL 项 ========"
[ "$FAIL" = "0" ] || exit 1
