# 随时掼掼蛋

雅加达华人线下掼蛋约局，H5 + PWA。方案见 [docs/h5-pwa-plan.md](docs/h5-pwa-plan.md)，服务端设计见
[docs/server-design.md](docs/server-design.md)，协作协议见 [AGENTS.md](AGENTS.md)。

服务端是一个 Go 进程：HTTP、内嵌的前端页面、SQLite 数据库、定时提醒都在里面，
整个打包成一个 Docker 镜像。

## 本地开发

```bash
make run        # 开发模式：GUANDAN_DEV=1，监听 http://localhost:8080 ，数据写在 ./.data
make seed       # 往 ./.data 写入设计稿里的示例数据（老王、Lily……，牌局短码 SEED01–SEED05）
```

改前端时开着 Vite 的热更新，API 仍然走上面那个 Go 进程：

```bash
make run                  # 一个终端
cd app/h5 && npm run dev     # 另一个终端，http://localhost:5173 ，/api 和 /healthz 代理到 :8080
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
| `server/cmd/guandan/` | 入口：读配置 → 锁数据目录 → 打开数据库 → 迁移 → 启动 HTTP 和后台任务 |
| `server/internal/config/` | 环境变量与校验 |
| `server/internal/store/` | SQLite 连接、迁移、唯一的写入口 `Write`、数据不变量；所有 SQL 只写在这里 |
| `server/internal/clock/` | 时钟接口，测试里可以快进 |
| `server/internal/supervise/` | 后台任务监管：panic 恢复、退避重启、心跳 |
| `server/internal/httpapi/` | 路由、中间件（CSRF、安全头、限流、日志）、`/healthz` 与 `/readyz`、静态页面 |
| `server/internal/realtime/` | WebSocket 实时推送（#18 实现，现在是占位） |
| `server/migrations/` | SQL 迁移，`embed` 进二进制，启动时按文件名顺序执行 |
| `app/h5/` | H5 前端源码（Preact + Vite），构建产物 `app/h5/dist` 也被 `embed` |
| `app/minigame/` | 微信小游戏（二期，#83 选型原型），用微信开发者工具打开这个目录 |

`app/h5/dist/` 里平时只有一个 `.gitkeep`，是为了让没装 node 的环境也能 `go build` 和 `go test`。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `GUANDAN_DATA` | `/data` | 数据库、备份、证书缓存，唯一的持久状态 |
| `GUANDAN_ADDR` | `:8080` | 不启用内置 TLS 时的监听地址 |
| `GUANDAN_DOMAIN` | 空 | 填了就用 autocert 签 HTTPS，容器内监听 :8080 和 :8443 |
| `GUANDAN_PUBLIC_URL` | 按域名推导 | 生成分享链接和 OG 图 URL 时用，也是 CSRF 校验认的本站来源；非开发模式下它和 `GUANDAN_DOMAIN` 至少要设一个，否则拒绝启动 |
| `GUANDAN_ADMIN_EMAIL` | 空 | Let's Encrypt 通知地址，也是 VAPID 的 subject |
| `GUANDAN_TRUSTED_PROXIES` | 空（不信任任何代理） | 可信反向代理的网段（逗号分隔）。只有对端在这里面时才采用 `X-Real-IP` / `X-Forwarded-For` 里的客户端 IP。**只有前面有反向代理、并且代理会用 `$remote_addr` 覆盖 `X-Real-IP` 时才设置**；容器直接对外时设了它，任何人都能伪造 IP 绕过限流。生产（nginx → docker 网桥）设为 `172.17.0.1/32`。启动日志会打印生效值 |
| `GUANDAN_DEV` | 空 | 设为 `1` 进入开发模式：不启用 TLS，cookie 不加 `Secure`，文本日志 |
| `GUANDAN_BACKUP_URL` | 空 | 异地备份目标：`https://<endpoint>/<bucket>[/<前缀>]`（S3 兼容，path-style）或 `file:///<目录>` |
| `GUANDAN_BACKUP_KEY` | 空 | 备份加密的 age 公钥（`guandan backup keygen` 生成）；填了备份地址就必须填它 |
| `AWS_REGION`、`AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY` | `us-east-1`、空、空 | S3 备份的区域和凭据 |
| `GUANDAN_METRICS_ADDR` | `127.0.0.1:9090` | `/metrics` 的监听地址，默认只有本机能访问；设为空字符串关闭 |
| `ANTHROPIC_API_KEY` | 空 | 蛋蛋用（phase:3） |
| `GUANDAN_WX_APPID` | 空 | 小游戏 AppID（#84），和 `GUANDAN_WX_SECRET` 要么都设、要么都不设。都不设时 `POST /api/wx/login` 返回 `503 wx_disabled`；开发模式下改为接受 `dev:<openid>` 形式的假 code |
| `GUANDAN_WX_SECRET` | 空 | 小游戏 AppSecret，只放环境变量 |

## 部署

生产环境 https://guandan.synon.ai ：nginx（宿主机）负责 TLS 和静态前端，容器只听 `127.0.0.1:18200`。拓扑、上线准备、发布、备份恢复和管理命令见 [docs/runbook.md](docs/runbook.md)，nginx 站点模板在 [deploy/nginx/](deploy/nginx/)。

```bash
scripts/deploy.sh          # 构建 → 传镜像和静态文件 → 换容器 → 等 /readyz → 切 current；失败自动回滚
```

启动时依次执行：锁数据目录 → 检查目录可写和剩余空间 → `quick_check` → 迁移（§12.1）。数据库里有本程序不认识的迁移（降级）时拒绝启动。
`/healthz` 返回 200 说明进程活着，`/readyz` 返回 200 说明写事务、后台任务心跳、磁盘空间和备份都正常；两者都不占并发名额。
镜像自带 `HEALTHCHECK`，调用 `/guandan healthcheck` 子命令请求本机的 `/healthz`（distroless 里没有 curl）。

## 关于 Go 版本

设计文档写的是 Go 1.23+，实际用到 **go 1.27.2**：

- `modernc.org/sqlite` 现在要求 go ≥ 1.26；
- 1.27.1 的标准库有 6 个可达的 http2 漏洞（`GO-2026-6603` 等），1.27.2 才修掉，
  而 CI 里 `govulncheck` 是必过检查。

所以 `go.mod` 是 `go 1.27.2`，Dockerfile 用 `golang:1.27-alpine`。
