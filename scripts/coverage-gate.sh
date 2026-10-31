#!/usr/bin/env bash
# 覆盖率门禁：全仓库总覆盖率不得低于阈值（防倒退）。
#
# 为什么用「总覆盖率」而不是逐包阈值（2026-10-31 的踩坑教训）：
#   单包覆盖率受平台分支影响很大 —— kill_unix/kill_windows、hrp 是否在位
#   （importsvc 端到端）、web/dist 是否存在（webui），同一份代码在
#   Windows 本地 / ubuntu runner 上能差出好几个点，逐包阈值必然误伤。
#   总覆盖率把这些波动摊薄到 18 个包里，跨平台稳定，门禁语义不变：
#   **只能涨不能跌**。
#
# 阈值 = 63.2%（2026-10-31 实测）- 3pp 余量。提升覆盖率后请上调本值。
set -euo pipefail
cd "$(dirname "$0")/.."

MIN_TOTAL=60

if ! command -v go >/dev/null 2>&1; then
  : "${HRP_BINARY_PATH:=}"
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

output="$(go test -cover ./...)"
printf '%s\n' "$output"

# 总覆盖率：需要真实 profile 才能用 go tool cover 汇总。
# 用仓库内相对路径做临时文件：Windows 上 mktemp 给出带盘符的绝对路径，
# 部分环境对删绝对路径临时文件有安全限制，相对路径两边都干净。
tmp=".coverage-tmp.out"
trap 'rm -f "$tmp"' EXIT
go test -coverprofile="$tmp" ./... >/dev/null
total="$(go tool cover -func="$tmp" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"

echo ""
echo "全仓库总覆盖率：${total}%（门槛 ${MIN_TOTAL}%）"
bad="$(awk -v c="$total" -v m="$MIN_TOTAL" 'BEGIN { print (c + 0 < m + 0) ? 1 : 0 }')"
if [ "$bad" = "1" ]; then
  echo "FAIL：总覆盖率低于门槛 —— 请补测试；确属应下降时，说明理由并下调 MIN_TOTAL"
  exit 1
fi
echo "覆盖率门禁通过"
