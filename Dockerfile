# 三阶段构建，对应 docs/server-design.md §8：前端 → Go → distroless 运行时。
# 一个进程、一个镜像、一个数据卷。

# 1) 前端：Preact + Vite，产物是 web/dist
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2) Go：CGO_ENABLED=0，因为 modernc.org/sqlite 是纯 Go 驱动，所以运行时镜像能用 distroless/static
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/guandan ./cmd/guandan
# 让运行时的 /data 属于 nonroot，这样挂上 named volume 后进程写得进去。
RUN mkdir -p /out/data

# 3) 运行时
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/guandan /guandan
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME /data
EXPOSE 80 443 8080
ENTRYPOINT ["/guandan"]
