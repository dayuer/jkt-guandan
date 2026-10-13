# 三阶段构建，对应 docs/server-design.md §8：前端 → Go → distroless 运行时。
# 一个进程、一个镜像、一个数据卷。
#
# 注意 Go 版本：设计里写的是 golang:1.23-alpine，但
#   1) 当前的 modernc.org/sqlite 要求 go ≥ 1.26；
#   2) 1.27.1 的标准库有 6 个可达的 http2 漏洞（GO-2026-6603/6609/6611/6612/6613/6617），1.27.2 才修掉；
# 所以这里用 golang:1.27-alpine，go.mod 里是 go 1.27.2。govulncheck 现在干净。

# 前两个阶段固定在构建机的平台上跑（$BUILDPLATFORM），Go 交叉编译到目标平台：
# 在 Apple Silicon 上为 amd64 服务器构建时（scripts/deploy.sh），不用走 QEMU 模拟。

# 1) 前端：Preact + Vite，产物是 web/dist（scripts/deploy.sh 用 --target web 单独取出来交给 nginx）
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2) Go：CGO_ENABLED=0，因为 modernc.org/sqlite 是纯 Go 驱动，所以运行时镜像能用 distroless/static
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
# 构建号（§13.4）：CI 传提交 SHA，响应头 X-App-Build 和 /metrics 的 guandan_build_info 用它。
ARG BUILD=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X github.com/dayuer/jkt-guandan/internal/buildinfo.Build=${BUILD}" \
      -o /out/guandan ./cmd/guandan
# 让运行时的 /data 属于 nonroot，这样挂上 named volume 后进程写得进去（--read-only 时只有 /data 可写）
RUN mkdir -p /out/data

# 3) 运行时。nonroot 不能监听 1024 以下的端口，所以容器内听 8080 和 8443，
# 由宿主机把 80 和 443 映射过来（§8、§10.12）。
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/guandan /guandan
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME /data
EXPOSE 8080 8443
# 存活检查走 /healthz（§12.5）。镜像里没有 curl，用二进制自带的 healthcheck 子命令请求本机。
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["/guandan", "healthcheck"]
ENTRYPOINT ["/guandan"]
