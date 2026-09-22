#!/usr/bin/env bash
# httprunnerplatform 开发环境变量
#
# 用法：
#   source scripts/dev-env.sh      # Git Bash
#
# 说明：
#   工具链（Go、hrp）装在仓库之外，避免污染用户环境，也避免把二进制提交进 Git。
#   默认约定目录为 F:/httprunnerplatform-tools，可用 HRP_PLATFORM_TOOLS 覆盖。

TOOLS_ROOT="${HRP_PLATFORM_TOOLS:-F:/httprunnerplatform-tools}"

export GOROOT="$TOOLS_ROOT/go"
export GOPATH="$TOOLS_ROOT/gopath"
export GOBIN="$TOOLS_ROOT/gopath/bin"

# Git Bash 的 PATH 必须用 POSIX 形式（/f/...），Windows 形式（F:/...）无法解析。
# GOROOT/GOPATH 反过来要保留 Windows 形式供 go.exe 自己使用。
if command -v cygpath >/dev/null 2>&1; then
  export PATH="$(cygpath -u "$GOROOT")/bin:$(cygpath -u "$GOBIN"):$PATH"
else
  export PATH="$GOROOT/bin:$GOBIN:$PATH"
fi

# 国内代理（部分网络环境下 go.dev 直连会 SSL 握手失败）
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"

# hrp 引擎二进制路径 —— 平台通过此环境变量注入，代码中不硬编码
if [ -z "$HRP_BINARY_PATH" ]; then
  if [ -f "$TOOLS_ROOT/bin/hrp.exe" ]; then
    export HRP_BINARY_PATH="$TOOLS_ROOT/bin/hrp.exe"   # Windows
  elif [ -x "$TOOLS_ROOT/bin/hrp" ]; then
    export HRP_BINARY_PATH="$TOOLS_ROOT/bin/hrp"       # Linux / macOS
  fi
fi

echo "GOROOT          = $GOROOT"
echo "GOPATH          = $GOPATH"
echo "GOPROXY         = $GOPROXY"
echo "HRP_BINARY_PATH = ${HRP_BINARY_PATH:-<未找到>}"
