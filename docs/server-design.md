# 服务端设计：Go 单一二进制 + 内嵌 SQLite + 单个 Docker 镜像

> 2026-10-13 定稿。仓库所有者已决定：服务端用 Go 写，编译成一个可执行文件；整个服务端连同数据库打包进一个 Docker 镜像部署。本文取代 `docs/h5-pwa-plan.md` §7 原先的 Netlify + Supabase 方案。

## 1. 原则

- **一个进程，一个镜像，一个数据卷。** HTTP 服务、静态页面、数据库、定时提醒、推送、OG 图渲染，全部在同一个 Go 进程里完成。所有持久状态都放在 `/data` 这一个目录里。
- **只跑单实例。** SQLite 和进程内的定时任务都要求同一时刻只有一个实例在跑。按这个项目的规模（雅加达牌友圈，最多几千人），单实例绰绰有余。将来需要多实例时，再换成 Postgres 加外部队列；数据访问统一走 `store` 包，换库时改动集中在这一层。
- **零外部依赖即可运行。** 不需要外部数据库、对象存储或消息队列。外部服务只有三个，都是可选的：Web Push 服务（FCM / Apple）、Claude API（蛋蛋，phase:3），以及异地备份目标（phase:2）。

## 2. 技术选型

| 关注点 | 选择 | 理由 |
|---|---|---|
| 语言 | Go 1.23+ | 单个静态二进制，交叉编译方便，标准库就足以写 HTTP 服务 |
| HTTP | 标准库 `net/http`，路由用 Go 1.22 起的 `ServeMux` 模式匹配 | 不引入 Web 框架 |
| 数据库 | SQLite（WAL 模式），驱动 `modernc.org/sqlite` | 纯 Go 实现、无需 CGO，所以能用 `distroless/static` 镜像；数据库就是 `/data` 下的一个文件 |
| 迁移 | `migrations/*.sql` 用 `embed` 打进二进制，启动时按序执行，执行记录存在 `schema_migrations` 表 | 部署时不需要额外的迁移工具 |
| 前端 | Preact + Vite 构建出的 `web/dist`，用 `embed.FS` 打进二进制 | 前端和 API 同源，cookie 是第一方的 |
| TLS | 两种模式：① 内置 `autocert` 自动申请 Let's Encrypt 证书，监听 80/443；② 只监听 HTTP，放在反向代理后面 | 模式 ① 不需要额外的容器 |
| 推送 | `github.com/SherClockHolmes/webpush-go` | VAPID 密钥在首次启动时生成，存进数据库 |
| OG 图和海报 | `golang.org/x/image` + `opentype`，内嵌思源宋体子集和一个无衬线字体 | 服务端出图；渲染结果按「局 + 版本号」缓存 |
| 日志 | 标准库 `log/slog`，输出 JSON 到 stdout | 用 `docker logs` 就能看 |

## 3. 仓库结构

```
cmd/guandan/main.go        入口：读配置 → 打开数据库 → 执行迁移 → 启动 HTTP 和定时任务
internal/config            环境变量
internal/store             SQLite 连接、迁移、事务封装（所有 SQL 只写在这里）
internal/identity          用户、设备 token、claim token、认领码、身份合并
internal/games             牌局、报名（take_seat）、退出、改时间
internal/notify            outbox 定时任务、Web Push 发送、订阅清理
internal/share             /g/:code 页面的 OG 标签注入、OG 图和海报渲染、.ics 生成
internal/agent             蛋蛋（phase:3）
internal/httpapi           路由、中间件（身份、限流、CSRF）、JSON 编解码
migrations/                0001_init.sql …
web/                       前端源码（Vite）；构建产物 web/dist 被 embed
Dockerfile, Makefile
```

## 4. 数据模型（SQLite）

```sql
-- 时间统一存 UTC 的 Unix 秒（INTEGER），显示时转成 Asia/Jakarta（WIB）。
CREATE TABLE users (
  id          TEXT PRIMARY KEY,            -- ULID
  nickname    TEXT NOT NULL,
  wa_phone    TEXT,                        -- 可选，绑定后用于找回
  merged_into TEXT REFERENCES users(id),
  created_at  INTEGER NOT NULL
);
CREATE TABLE devices (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  token_hash BLOB NOT NULL UNIQUE,         -- sha256(device_token)，不存明文
  ua         TEXT,
  last_seen  INTEGER NOT NULL
);
CREATE TABLE claim_tokens (
  token_hash BLOB PRIMARY KEY,             -- sha256(claim_token)
  code       TEXT NOT NULL,                -- 4 位认领码
  user_id    TEXT NOT NULL REFERENCES users(id),
  expires_at INTEGER NOT NULL,             -- 签发后 10 分钟
  used_at    INTEGER,
  attempts   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX claim_code_live ON claim_tokens(code) WHERE used_at IS NULL;
CREATE TABLE games (
  id         TEXT PRIMARY KEY,
  short_code TEXT NOT NULL UNIQUE,         -- 链接里的短码，如 /g/8K2
  host_id    TEXT NOT NULL REFERENCES users(id),
  starts_at  INTEGER NOT NULL,
  area       TEXT NOT NULL,                -- SCBD / PIK / ...
  venue      TEXT NOT NULL,
  kind       TEXT NOT NULL,                -- classic / no-level / teaching
  note       TEXT,
  status     TEXT NOT NULL DEFAULT 'open', -- open / full / cancelled / done
  version    INTEGER NOT NULL DEFAULT 1,   -- 改时间、改地点时 +1
  created_at INTEGER NOT NULL
);
CREATE TABLE seats (
  game_id    TEXT NOT NULL REFERENCES games(id),
  seat_no    INTEGER NOT NULL CHECK (seat_no BETWEEN 1 AND 4),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (game_id, seat_no),
  UNIQUE (game_id, user_id)
);
CREATE TABLE push_subscriptions (
  endpoint   TEXT PRIMARY KEY,
  p256dh     TEXT NOT NULL,
  auth       TEXT NOT NULL,
  user_id    TEXT NOT NULL REFERENCES users(id),
  device_id  TEXT NOT NULL REFERENCES devices(id),
  fail_count INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE outbox (
  id         INTEGER PRIMARY KEY,
  game_id    TEXT NOT NULL REFERENCES games(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  kind       TEXT NOT NULL,                -- full / t_minus_1h / seat_left / rescheduled
  send_at    INTEGER NOT NULL,
  sent_at    INTEGER,
  dedupe_key TEXT NOT NULL UNIQUE          -- 局 + 人 + 类型 + 局版本号
);
CREATE INDEX outbox_due ON outbox(send_at) WHERE sent_at IS NULL;
CREATE TABLE kv (k TEXT PRIMARY KEY, v BLOB NOT NULL);  -- 存 VAPID 密钥等
```

连接设置：
- 写连接池只开 **1 个连接**（`SetMaxOpenConns(1)`），写事务一律用 `BEGIN IMMEDIATE`；读用另一个连接池。
- 每个连接执行：`PRAGMA journal_mode=WAL; busy_timeout=5000; foreign_keys=ON; synchronous=NORMAL`。

## 5. 关键流程

### 5.1 报名：`take_seat`（解决抢座并发）

在同一个写事务里（`BEGIN IMMEDIATE` 会拿到写锁，这一段天然串行）：

1. 读出牌局，状态不是 `open` 就返回 409；
2. 这个用户已经坐下了，就直接返回已有座位（重复提交也安全）；
3. 选出最小的空座号插入；表上的主键和唯一约束兜底，不会出现两个人坐同一个座位；
4. 插入后满 4 人：把牌局改成 `full`，同时给 4 个人各写一条 `kind=full` 的 outbox；
5. 给这个用户写一条 `t_minus_1h` 的 outbox，`send_at = starts_at - 3600`；
6. 提交。

退出报名：删除座位，牌局从 `full` 回到 `open`；给仍在局里的人写 `seat_left`，同时作废退出者自己的 `t_minus_1h`。

改时间：`version + 1`；作废所有未发送的 `t_minus_1h`，按新时间重新生成；给所有人写 `rescheduled`。

### 5.2 身份

- **首次填称呼**（`POST /api/me`）：创建 user 和 device，生成 32 字节随机的 `device_token`，以 cookie 形式下发：`gd_dev`，`HttpOnly; Secure; SameSite=Lax; Max-Age=400 天`。数据库只存它的 sha256。
- **跳出微信前**（`POST /api/claim`）：生成 16 字节随机的 `claim_token` 和 4 位认领码，10 分钟内有效。前端把 token 拼进跳出链接：`?claim=…`。
- **兑换**（`POST /api/claim/redeem`）：
  - 凭 token 兑换：`UPDATE claim_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL AND expires_at>? RETURNING user_id`。这一条语句同时保证「只能用一次」和「过期作废」。兑换成功后给新设备下发 cookie，前端立刻用 `history.replaceState` 清掉 URL 里的参数。
  - 凭认领码兑换：只允许**还没有身份的设备**使用。每个码最多试 5 次（`attempts` 字段计数）；同一 IP 每分钟最多 10 次，用内存令牌桶限流（单实例，所以内存限流够用）。
- **身份合并**：把旧用户的座位和推送订阅迁移到新用户。如果迁移会违反唯一约束（两个身份都报了同一局），保留较早的那个座位。旧用户只标记 `merged_into`，不删除。

### 5.3 提醒（outbox 模式）

- 一个后台 goroutine，每 30 秒检查一次：取出 `sent_at IS NULL AND send_at <= now` 的记录，每批最多 100 条。
- 对每条记录：找到这个用户的全部推送订阅，逐个发送；发完把 `sent_at` 写上。
- 推送服务返回 404 或 410：删除这个订阅。返回 5xx：`fail_count + 1`，超过 5 次就删除。
- 进程重启不会丢提醒，因为提醒都存在数据库里。重启后补发时，`t_minus_1h` 如果已经过了开局时间，就跳过不发。
- VAPID 密钥：首次启动时生成并写进 `kv` 表，公钥通过 `GET /api/push/key` 下发给前端。**密钥一旦换掉，所有已有的订阅都会失效**，所以必须包含在备份里。

### 5.4 分享

- `GET /g/{code}`：读出 `index.html` 模板，把这一局的 `og:title`、`og:description`、`og:image` 写进去再返回。微信和 WhatsApp 的链接抓取器不执行 JS，只有这样才能拿到每局各自的预览卡片。
- `GET /g/{code}/og.png`（1200×630）和 `GET /g/{code}/poster.png`（1080×1440）：服务端渲染。结果先缓存在内存里，键是 `code + version + 座位数`，同时设 `Cache-Control: public, max-age=60`。
- `GET /g/{code}/game.ics`：日历文件。时区写 `Asia/Jakarta`，`UID` 用牌局 id，`SEQUENCE` 用版本号，这样改时间后重新导入，日历里会更新原有事件而不是新增一条。

## 6. HTTP 接口

```
GET  /healthz                         存活检查（会执行一次 SELECT 1）
GET  /api/push/key                    VAPID 公钥
POST /api/me                          首次填称呼
GET  /api/me                          当前身份
POST /api/claim                       签发 claim token 和认领码
POST /api/claim/redeem                凭 {token} 或 {code} 兑换身份
GET  /api/games?area=&day=            首页列表
POST /api/games                       开一局
GET  /api/games/{code}                牌局详情
PATCH /api/games/{code}               局头修改（版本号 +1）
POST /api/games/{code}/seat           报名
DELETE /api/games/{code}/seat         退出
POST /api/push/subscribe              保存推送订阅（phase:2）
DELETE /api/push/subscribe            取消订阅
POST /api/agent                       蛋蛋（phase:3）：只返回草稿，不写库
GET  /g/{code} · /g/{code}/og.png · /g/{code}/poster.png · /g/{code}/game.ics
GET  /*                               内嵌的前端页面（单页应用，未知路径回落到 index.html）
```

中间件：
- 身份：从 `gd_dev` cookie 解析出用户；
- CSRF：会改数据的请求，必须带 `Content-Type: application/json`，并且 `Origin` 等于本站；
- 限流：按 IP 和设备；
- 统一的 JSON 错误格式：`{"error":{"code":"seat_full","message":"…"}}`；
- 访问日志（slog）。

## 7. 配置（环境变量）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `GUANDAN_DATA` | `/data` | 数据库、备份、证书缓存的目录 |
| `GUANDAN_ADDR` | `:8080` | 不用内置 TLS 时的监听地址 |
| `GUANDAN_DOMAIN` | 空 | 设置后启用 autocert，监听 :80 和 :443；可以写多个域名，逗号分隔（主域名加备用域名） |
| `GUANDAN_PUBLIC_URL` | 根据域名推导 | 生成分享链接和 OG 图 URL 时用 |
| `GUANDAN_ADMIN_EMAIL` | 空 | 填给 Let's Encrypt，也用作 VAPID 的 subject |
| `ANTHROPIC_API_KEY` | 空 | 为空时蛋蛋不可用，前端退回到表单（phase:3） |

## 8. 打包与部署

```dockerfile
# 1) 前端
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2) Go（CGO_ENABLED=0，因为 modernc.org/sqlite 是纯 Go）
FROM golang:1.23-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/guandan ./cmd/guandan

# 3) 运行时
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/guandan /guandan
VOLUME /data
EXPOSE 80 443 8080
ENTRYPOINT ["/guandan"]
```

部署（服务器选雅加达或新加坡的 VPS，用户访问延迟最低）：

```bash
docker run -d --name guandan --restart unless-stopped \
  -p 80:80 -p 443:443 -v guandan-data:/data \
  -e GUANDAN_DOMAIN=guandan.id,backup-domain.id -e GUANDAN_ADMIN_EMAIL=… \
  ghcr.io/dayuer/jkt-guandan:<tag>
```

- **备份**：进程内每天执行一次 `VACUUM INTO /data/backup/guandan-YYYYMMDD.db`，保留最近 7 份。备份文件包含 VAPID 密钥。phase:2 再加异地备份，上传到任意 S3 兼容存储。
- **升级**：拉新镜像，然后 `docker stop`，再用新镜像 `docker run`。数据库迁移在启动时自动执行，只做向前兼容的变更。收到 SIGTERM 后，进程会等进行中的请求处理完、让定时任务停下，再关闭数据库。
- **镜像**：CI 构建镜像并推送到 GHCR，tag 用提交 SHA。

## 9. 测试

- `go test ./...`：每个测试用 `t.TempDir()` 新建一个 SQLite 文件，执行真实的迁移，不用 mock。
- 必须覆盖：
  - `take_seat` 并发：50 个 goroutine 抢 4 个座位，结果必须正好 4 人，并且只产生一组 `full` 提醒；
  - claim token：只能用一次、过期作废、认领码超过尝试上限后作废；
  - outbox：去重；改时间后旧提醒作废；重启后补发。
- 前端：Vitest。
- CI：`go vet`、`go test`、前端构建、`docker build`，job 名为 `test`，已加入分支保护的必过检查。

## 10. 与原方案相比的影响

| 原方案 | 现在 |
|---|---|
| Netlify 托管 + Functions | Go 进程同时提供页面和 API |
| Supabase Postgres | 内嵌 SQLite，数据库文件在 `/data` |
| Netlify Scheduled Functions | 进程内的 outbox 定时任务 |
| Netlify Edge Function 出 OG 图 | Go 服务端渲染 |
| 函数区域和数据库区域可能不在一处 | 服务器直接放在雅加达或新加坡，没有这个问题 |
| 托管平台负责高可用 | 单台 VPS：要自己负责备份和监控（`/healthz` 加外部探活） |
