# syntax=docker/dockerfile:1.7

############################ 1) 前端构建 ############################
FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# build 内含 vue-tsc --noEmit，类型不过即失败
RUN npm run build && test -f dist/index.html

############################ 2) 后端构建 ############################
FROM golang:1.26-bookworm AS go
ARG HRP_VERSION=v4.3.6
ARG VERSION=dev
ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 与 scripts/build.sh 第 2 步等价：web/dist → internal/webui/dist（go:embed 目录）
RUN rm -rf internal/webui/dist && mkdir -p internal/webui/dist
COPY --from=web /src/web/dist/ internal/webui/dist/
RUN date '+%Y-%m-%d %H:%M:%S' > internal/webui/dist/build-id \
 && : > internal/webui/dist/.gitkeep

RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/httprunnerplatform ./cmd/server

# hrp 引擎（Linux 版，平台以子进程方式调用）
RUN curl -fsSL -o /tmp/hrp.tgz \
      "https://github.com/httprunner/httprunner/releases/download/${HRP_VERSION}/hrp-${HRP_VERSION}-linux-amd64.tar.gz" \
 && mkdir -p /tmp/hrp && tar -xzf /tmp/hrp.tgz -C /tmp/hrp \
 && cp "$(find /tmp/hrp -type f -name hrp | head -n1)" /out/hrp \
 && chmod +x /out/hrp \
 && /out/hrp -v

############################ 3) 运行镜像 ############################
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -u 10001 -m app \
 && mkdir -p /app/data /app/configs && chown -R app:app /app

ENV TZ=Asia/Shanghai \
    HRP_BINARY_PATH=/usr/local/bin/hrp \
    HRP_PLATFORM_WORKSPACE_ROOT=/app/data \
    HRP_PLATFORM_DB_DRIVER=sqlite \
    HRP_PLATFORM_DB_DSN=/app/data/platform.db \
    HRP_PLATFORM_SERVER_PORT=8080

COPY --from=go /out/httprunnerplatform /usr/local/bin/httprunnerplatform
COPY --from=go /out/hrp /usr/local/bin/hrp

WORKDIR /app
USER app
EXPOSE 8080
VOLUME ["/app/data"]

HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/api/v1/healthz || exit 1

ENTRYPOINT ["httprunnerplatform"]
CMD ["server", "--migrate"]
