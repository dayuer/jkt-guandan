# 随时掼掼蛋

雅加达华人线下掼蛋约局，H5 + PWA。方案见 [docs/h5-pwa-plan.md](docs/h5-pwa-plan.md)，服务端设计见
[docs/server-design.md](docs/server-design.md)，协作协议见 [AGENTS.md](AGENTS.md)。

服务端是一个 Go 进程，里面装着 HTTP 服务、内嵌的前端页面、SQLite 数据库和定时提醒，
整个打包成一个 Docker 镜像。

## 本地开发

```bash
make run        # 构建后监听 http://localhost:8080 ，数据写在 ./.data
```

改前端时开着 Vite 的热更新，API 仍然走上面那个 Go 进程：

```bash
make run                  # 一个终端
cd web && npm run dev     # 另一个终端，http://localhost:5173
```

测试和构建：

```bash
make test       # go vet + 后端测试 + 前端测试
make build      # 二进制在 bin/guandan
make docker     # 镜像 guandan:local
```

## 结构

| 路径 | 内容 |
|---|---|
| `cmd/guandan/` | 入口：读配置 → 打开数据库 → 执行迁移 → 启动 HTTP |
| `internal/config/` | 环境变量 |
| `internal/store/` | SQLite 连接、迁移、写事务和「提交后回调」；所有 SQL 只写在这里 |
| `internal/httpapi/` | 路由、中间件、内嵌的静态页面 |
| `internal/realtime/` | WebSocket 实时推送（#18 实现） |
| `migrations/` | SQL 迁移，用 `embed` 打进二进制，启动时按文件名顺序执行 |
| `web/` | 前端源码（Preact + Vite），构建产物 `web/dist` 也被 `embed` |

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `GUANDAN_DATA` | `/data` | 数据库和备份所在目录，唯一的持久状态 |
| `GUANDAN_ADDR` | `:8080` | 不启用内置 TLS 时的监听地址 |
| `GUANDAN_DOMAIN` | 空 | 填了就用 autocert 签 HTTPS，监听 :80 和 :443 |
| `GUANDAN_PUBLIC_URL` | 按域名推导 | 生成分享链接和 OG 图 URL 时用 |
| `GUANDAN_ADMIN_EMAIL` | 空 | Let's Encrypt 通知地址 |
| `ANTHROPIC_API_KEY` | 空 | 蛋蛋用（phase:3） |

## 部署

```bash
docker run -d --name guandan --restart unless-stopped \
  -p 80:80 -p 443:443 -v guandan-data:/data \
  -e GUANDAN_DOMAIN=guandan.id -e GUANDAN_ADMIN_EMAIL=you@example.com \
  ghcr.io/dayuer/jkt-guandan:<tag>
```

起来之后 `/healthz` 返回 `{"status":"ok"}` 就说明数据库也通了。迁移在容器启动时自动执行。
