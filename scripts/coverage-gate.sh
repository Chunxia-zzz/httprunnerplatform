#!/usr/bin/env bash
# 覆盖率门禁：每个包的覆盖率不得低于既有水位（防倒退）。
#
# 阈值 = 2026-10-31 实测水位减约 2 个百分点的余量（双 OS 下平台相关
# 代码分支计数会略有浮动，见 docs/项目进度检查报告.md §4.3）。
# ⭐ 规则：覆盖率提升后请把阈值同步上调 —— 门禁的意义是「只能涨不能跌」；
#    新增包默认不做门禁（不在下表中），补了测试后再登记进来。
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v go >/dev/null 2>&1; then
  : "${HRP_BINARY_PATH:=}"
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

# 包名 → 最低覆盖率（%）
min_for() {
  case "$1" in
    internal/api)             echo 35 ;;
    internal/api/middleware)  echo 80 ;;
    internal/auth)            echo 69 ;;
    internal/compiler)        echo 81 ;;
    internal/config)          echo 84 ;;
    internal/decompiler)      echo 68 ;;
    internal/executor)        echo 85 ;;
    internal/model)           echo 54 ;;
    internal/parser)          echo 82 ;;
    internal/repo)            echo 76 ;;
    internal/scheduler)       echo 71 ;;
    internal/service)         echo 62 ;;
    internal/validator)       echo 84 ;;
    internal/webui)           echo 86 ;;
    pkg/hrpclient)            echo 87 ;;
    pkg/jsonx)                echo 79 ;;
    pkg/logx)                 echo 93 ;;
    pkg/response)             echo 100 ;;
    *)                        echo -1 ;;  # 未登记的包（如 cmd/server）不做门禁
  esac
}

output="$(go test -cover ./...)"
printf '%s\n' "$output"

fail=0
while read -r _ pkg rest; do
  [ -z "${pkg:-}" ] && continue
  case "$rest" in *"coverage: "*) ;; *) continue ;; esac
  cov="$(printf '%s' "$rest" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p')"
  [ -z "$cov" ] && continue
  short="${pkg#github.com/Chunxia-zzz/httprunnerplatform/}"
  min="$(min_for "$short")"
  [ "$min" = "-1" ] && continue
  bad="$(awk -v c="$cov" -v m="$min" 'BEGIN { print (c + 0 < m + 0) ? 1 : 0 }')"
  if [ "$bad" = "1" ]; then
    echo "FAIL：$short 覆盖率 ${cov}% 低于门槛 ${min}%"
    fail=1
  fi
done <<< "$output"

if [ "$fail" = "1" ]; then
  echo ""
  echo "覆盖率门禁未通过：请补测试，或（覆盖率确实应下降时）说明理由并下调阈值"
  exit 1
fi
echo ""
echo "覆盖率门禁通过：所有登记包均不低于既有水位"
