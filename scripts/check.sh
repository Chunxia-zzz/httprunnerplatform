#!/usr/bin/env bash
# 本地一键检查：与 Makefile 的 make check 完全同源。
#
# Windows 上没有 make，Git Bash 直接跑这个：
#   bash scripts/check.sh
#
# 任何一步失败立即退出（set -e），退出码非 0。
set -euo pipefail
cd "$(dirname "$0")/.."

# 本机 PATH 里没有 go 时，尝试走仓库约定的工具链（dev-env.sh）。
if ! command -v go >/dev/null 2>&1; then
  # dev-env.sh 用 [ -z "$HRP_BINARY_PATH" ] 判断注入；set -u 下先补默认值。
  : "${HRP_BINARY_PATH:=}"
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
fi

echo "==== 1/5 gofmt -l（应无输出） ===="
out="$(gofmt -l .)"
if [ -n "$out" ]; then
  echo "$out"
  echo "FAIL：以上文件未格式化（gofmt -w 修复后重跑）"
  exit 1
fi
echo "OK：所有 .go 文件已格式化"

echo ""
echo "==== 2/5 go vet ===="
go vet ./...
echo "OK"

echo ""
echo "==== 3/5 go test -cover ===="
go test -cover ./...
echo "OK"

echo ""
echo "==== 4/5 前端单测（vitest） ===="
(cd web && npm test)
echo "OK"

echo ""
echo "==== 5/5 前端类型（vue-tsc） ===="
(cd web && npm run typecheck)
echo "OK"

echo ""
echo "==== check 全部通过 ===="
