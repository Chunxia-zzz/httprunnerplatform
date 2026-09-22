#!/usr/bin/env bash
#
# 构建单文件交付：前端 → go:embed → 一个可执行文件。
#
# 用法（在仓库根目录）：
#   bash scripts/build.sh              # 产出 httprunnerplatform.exe
#   bash scripts/build.sh /tmp/xxx     # 指定输出路径
#   SKIP_NPM=1 bash scripts/build.sh   # 复用已有 web/dist，只重新内嵌与编译
#
# ⚠️ 为什么必须有这个脚本，而不能直接 `go build`：
#   go:embed 内嵌的是 internal/webui/dist 里的内容，而 vite 产出在 web/dist。
#   两者之间的"同步"这一步如果靠人记得手动做，早晚会出现
#   「代码改了、界面没变」的幽灵问题。这里把它固化成一个可复现的流程。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT="${1:-httprunnerplatform.exe}"
EMBED_DIR="internal/webui/dist"
TOOLS_ROOT="${HRP_PLATFORM_TOOLS:-F:/httprunnerplatform-tools}"
NPM_CACHE="$TOOLS_ROOT/npm-cache"

# 载入工具链（Go 的 PATH、GOPROXY、HRP_BINARY_PATH）。
#
# ⚠️ 必须临时关掉 set -u：dev-env.sh 是给人手动 source 的，里面会直接
# 引用 $HRP_BINARY_PATH 判断是否已设置。在 nounset 下那行会直接报
# "unbound variable" 并终止脚本（表现为构建到最后一步才炸）。
if [ -f scripts/dev-env.sh ]; then
  set +u
  # shellcheck disable=SC1091
  source scripts/dev-env.sh >/dev/null
  set -u
fi

# ---------------------------------------------------------------- 1/3 前端
if [ "${SKIP_NPM:-0}" = "1" ]; then
  echo "==> 1/3 跳过前端构建（SKIP_NPM=1）"
else
  echo "==> 1/3 构建前端"
  if [ ! -d web/node_modules ]; then
    echo "    安装依赖（node_modules 不存在）"
    # npm 缓存也放到仓库外：C 盘空间紧张时默认缓存目录会拖累构建
    (cd web && npm ci --cache "$NPM_CACHE")
  fi
  # npm run build 里已经串了 vue-tsc --noEmit，类型不过就构建不出产物
  (cd web && npm run build)
fi

if [ ! -f web/dist/index.html ]; then
  echo "!! web/dist/index.html 不存在，前端构建没有产出内容" >&2
  exit 1
fi

# ------------------------------------------------- 2/3 同步到 go:embed 目录
echo "==> 2/3 同步到内嵌目录 $EMBED_DIR"
# 先清空再拷贝：留着上一次的旧 bundle 会让二进制约来越大
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -r web/dist/. "$EMBED_DIR/"

# 构建时间写进内嵌目录，启动日志会打出来。
# 没有它就只能靠猜"手里这个 exe 带的是哪一版前端"。
date '+%Y-%m-%d %H:%M:%S' > "$EMBED_DIR/build-id"

# 占位文件必须留：它是全新克隆能 go build / go test 的前提（见 .gitignore）
: > "$EMBED_DIR/.gitkeep"

# ------------------------------------------------------------ 3/3 后端编译
echo "==> 3/3 编译后端"
go build -ldflags "-X main.version=$(date +%Y.%m.%d)" -o "$OUT" ./cmd/server

SIZE="$(du -h "$OUT" 2>/dev/null | cut -f1 || echo '?')"
echo
echo "✅ 完成：$OUT（$SIZE）"
echo "   内嵌前端：$(find "$EMBED_DIR" -type f | wc -l | tr -d ' ') 个文件"
echo "   启动：./$OUT server"
