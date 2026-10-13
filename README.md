# 随时掼掼蛋

雅加达华人线下掼蛋约局，H5 + PWA。方案见 [docs/h5-pwa-plan.md](docs/h5-pwa-plan.md)，服务端设计见
[docs/server-design.md](docs/server-design.md)，协作协议见 [AGENTS.md](AGENTS.md)。

服务端是一个 Go 进程：HTTP、内嵌的前端页面、SQLite 数据库、定时提醒都在里面，
整个打包成一个 Docker 镜像。

## 本地开发

```bash
make run        # 开发模式：GUANDAN_DEV=1，监听 http://localhost:8080 ，数据写在 ./.data
```

改前端时开着 Vite 的热更新，API 仍然走上面那个 Go 进程：

```bash
make run                  # 一个终端
cd web && npm run dev     # 另一个终端，http://localhost:5173 ，/api 和 /healthz 代理到 :8080
```

测试和构建：

```bash
make test       # go vet + go test -race + 前端测试
make vuln       # govulncheck
make build      # 二进制在 bin/guandan
make docker     # 镜像 guandan:local
```

## 结构

| 路径 | 内容 |
|---|---|
| `cmd/guandan/` | 入口：读配置 → 锁数据目录 → 打开数据库 → 迁移 → 启动 HTTP 和后台任务 |
| `internal/config/` | 环境变量与校验 |
| `internal/store/` | SQLite 连接、迁移、唯一的写入口 `Write`、数据不变量；所有 SQL 只写在这里 |
| `internal/clock/` | 时钟接口，测试里可以快进 |
| `internal/supervise/` | 后台任务监管：panic 恢复、退避重启、心跳 |
| `internal/httpapi/` | 路由、中间件（CSRF、安全头、限流、日志）、`/healthz` 与 `/readyz`、静态页面 |
| `internal/realtime/` | WebSocket 实时推送（#18 实现，现在是占位） |
| `migrations/` | SQL 迁移，`embed` 进二进制，启动时按文件名顺序执行 |
| `web/` | 前端源码（Preact + Vite），构建产物 `web/dist` 也被 `embed` |

`web/dist/` 里平时只有一个 `.gitkeep`，是为了让没装 node 的环境也能 `go build` 和 `go test`。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `GUANDAN_DATA` | `/data` | 数据库、备份、证书缓存，唯一的持久状态 |
| `GUANDAN_ADDR` | `:8080` | 不启用内置 TLS 时的监听地址 |
| `GUANDAN_DOMAIN` | 空 | 填了就用 autocert 签 HTTPS，容器内监听 :8080 和 :8443 |
| `GUANDAN_PUBLIC_URL` | 按域名推导 | 生成分享链接和 OG 图 URL 时用 |
| `GUANDAN_ADMIN_EMAIL` | 空 | Let's Encrypt 通知地址，也是 VAPID 的 subject |
| `GUANDAN_DEV` | 空 | 设为 `1` 进入开发模式：不启用 TLS，cookie 不加 `Secure`，文本日志 |
| `GUANDAN_BACKUP_URL` | 空 | 异地备份目标（S3 兼容） |
| `GUANDAN_BACKUP_KEY` | 空 | 备份加密的 age 公钥；填了备份地址就必须填它 |
| `ANTHROPIC_API_KEY` | 空 | 蛋蛋用（phase:3） |

## 部署

```bash
docker run -d --name guandan --restart unless-stopped \
  --read-only --cap-drop ALL --stop-timeout 30 \
  --log-opt max-size=10m --log-opt max-file=5 \
  -p 80:8080 -p 443:8443 -v guandan-data:/data \
  -e GUANDAN_DOMAIN=example.com -e GUANDAN_ADMIN_EMAIL=you@example.com \
  ghcr.io/dayuer/jkt-guandan:<tag>
```

nonroot 用户不能监听 1024 以下的端口，所以容器内听 8080 和 8443，由端口映射到 80 和 443。
迁移在启动时自动执行。`/healthz` 返回 200 说明进程活着，`/readyz` 返回 200 说明写事务、
后台任务心跳和磁盘空间都正常。

## 关于 Go 版本

设计文档写的是 Go 1.23+，实际用到 **go 1.27.2**：

- `modernc.org/sqlite` 现在要求 go ≥ 1.26；
- 1.27.1 的标准库有 6 个可达的 http2 漏洞（`GO-2026-6603` 等），1.27.2 才修掉，
  而 CI 里 `govulncheck` 是必过检查。

所以 `go.mod` 是 `go 1.27.2`，Dockerfile 用 `golang:1.27-alpine`。
