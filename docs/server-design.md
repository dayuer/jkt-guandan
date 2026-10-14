# 服务端设计：Go 单一二进制 + 内嵌 SQLite + 单个 Docker 镜像

> 2026-10-13 定稿。仓库所有者已决定：服务端用 Go 写，编译成一个可执行文件；整个服务端连同数据库打包进一个 Docker 镜像部署。本文取代 `docs/h5-pwa-plan.md` §7 原先的 Netlify + Supabase 方案。

## 1. 原则

- **一个进程，一个镜像，一个数据卷。** HTTP 服务、静态页面、数据库、定时提醒、推送、OG 图渲染，全部在同一个 Go 进程里完成。所有持久状态都放在 `/data` 这一个目录里。
- **只跑单实例。** SQLite 和进程内的定时任务都要求同一时刻只有一个实例在跑。按这个项目的规模（雅加达牌友圈，最多几千人），单实例绰绰有余。将来需要多实例时，再换成 Postgres 加外部队列；数据访问统一走 `store` 包，换库时改动集中在这一层。
- **零外部依赖即可运行。** 不需要外部数据库、对象存储或消息队列。外部服务只有三个，都是可选的：Web Push 服务（FCM / Apple）、Claude API（蛋蛋，phase:3），以及异地备份目标（phase:2）。

## 2. 技术选型

| 关注点 | 选择 | 理由 |
|---|---|---|
| 语言 | Go 1.27.2+ | 单个静态二进制，交叉编译方便，标准库就足以写 HTTP 服务 |
| HTTP | 标准库 `net/http`，路由用 Go 1.22 起的 `ServeMux` 模式匹配 | 不引入 Web 框架 |
| 数据库 | SQLite（WAL 模式），驱动 `modernc.org/sqlite` | 纯 Go 实现、无需 CGO，所以能用 `distroless/static` 镜像；数据库就是 `/data` 下的一个文件 |
| 迁移 | `migrations/*.sql` 用 `embed` 打进二进制，启动时按序执行，执行记录存在 `schema_migrations` 表 | 部署时不需要额外的迁移工具 |
| 前端 | Preact + Vite 构建出的 `web/dist`，用 `embed.FS` 打进二进制 | 前端和 API 同源，cookie 是第一方的 |
| TLS | 两种模式：① 内置 `autocert` 自动申请 Let's Encrypt 证书，监听 80/443；② 只监听 HTTP，放在反向代理后面 | 模式 ① 不需要额外的容器 |
| 推送 | `github.com/SherClockHolmes/webpush-go` | VAPID 密钥在首次启动时生成，存进数据库 |
| OG 图和海报 | `golang.org/x/image` + `opentype`，内嵌思源宋体子集和一个无衬线字体 | 服务端出图；渲染结果按「局 + 版本号」缓存 |
| 安全 | `__Host-` HttpOnly cookie、`html/template`、CSP、授权矩阵测试 | 详见 §10 |
| 实时 | WebSocket，库用 `github.com/coder/websocket`；进程内 Hub 按主题分发 | 接口基于 context，和标准库 `net/http` 直接配合；单实例，所以不需要 Redis 之类的 pub/sub |
| 日志 | 标准库 `log/slog`，输出 JSON 到 stdout | 用 `docker logs` 就能看 |

## 3. 仓库结构

```
cmd/guandan/main.go        入口：读配置 → 打开数据库 → 执行迁移 → 启动 HTTP 和定时任务
internal/config            环境变量
internal/store             SQLite 连接、迁移、事务封装。业务包（identity、games…）可以写自己的 SQL，但只能通过 store.Write / store.Read 拿到的事务或连接执行；store 以外不允许持有 *sql.DB
internal/identity          用户、设备 token、claim token、认领码、设备切换（合并只留给 admin）
internal/games             牌局、报名（take_seat）、退出、改时间
internal/notify            outbox 定时任务、Web Push 发送、订阅清理
internal/realtime          WebSocket 接入、Hub（主题订阅与广播）、心跳、限流
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
  merged_into TEXT REFERENCES users(id),   -- 只有 admin 合并会写入（§5.2：不提供面向用户的合并）
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
  code       TEXT NOT NULL,                -- 6 位认领码（#31；0001_init.sql 的注释仍写 4 位，以代码 ClaimCodeDigits = 6 为准）
  user_id    TEXT NOT NULL REFERENCES users(id),
  expires_at INTEGER NOT NULL,             -- 签发后 10 分钟
  used_at    INTEGER,
  attempts   INTEGER NOT NULL DEFAULT 0,   -- 已弃用：代码不再读写（#95），留着是为了回滚到旧版本时 INSERT 还能用；以后在计划好的 breaking 发布里再删
  issued_net TEXT                          -- 签发时的客户端网络，形如 203.0.113.0/24、2001:db8::/64；兑换认领码时必须一致（0002_claim_net.sql）。为空（0002 之前签发、或 IP 解析不出）时不做网络检查
);
-- 有效码唯一（0002_claim_net.sql 改为 UNIQUE；作废旧码时写 used_at 把号码让出来）
CREATE UNIQUE INDEX claim_code_live ON claim_tokens(code) WHERE used_at IS NULL;
CREATE TABLE games (
  id         TEXT PRIMARY KEY,
  short_code TEXT NOT NULL UNIQUE,         -- 链接里的短码：6 位 Crockford base32 随机串（如 /g/8K2QXM），防止逐个猜出所有局
  host_id    TEXT NOT NULL REFERENCES users(id),
  starts_at  INTEGER NOT NULL,
  area       TEXT NOT NULL,                -- SCBD / PIK / ...
  venue      TEXT NOT NULL,
  kind       TEXT NOT NULL,                -- classic / no-level / teaching
  note       TEXT,
  status     TEXT NOT NULL DEFAULT 'open'
             CHECK (status IN ('open','full','cancelled','done')),
  version    INTEGER NOT NULL DEFAULT 1,   -- 牌局有任何变化（改时间、改地点、座位变动、换局头）都 +1，用于乐观锁和 WebSocket 消息排序
  created_at INTEGER NOT NULL,
  ended_at   INTEGER                       -- 变成 cancelled 或 done 的时间
);
CREATE INDEX games_feed ON games(area, starts_at) WHERE status IN ('open','full');
CREATE TABLE seats (
  game_id    TEXT NOT NULL REFERENCES games(id),
  seat_no    INTEGER NOT NULL CHECK (seat_no BETWEEN 1 AND 4),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (game_id, seat_no),
  UNIQUE (game_id, user_id)
);
-- 候补：局满后报名的人按先后顺序排队；有人退出时，队首自动补上。
CREATE TABLE waitlist (
  game_id    TEXT NOT NULL REFERENCES games(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,             -- 排队顺序以它为准（相同时再按 rowid）
  PRIMARY KEY (game_id, user_id)
);
CREATE INDEX waitlist_order ON waitlist(game_id, created_at);
-- 同一个人在一局里只能占一个位置：不能既在座位上又在候补里。由触发器在数据库层强制，不靠应用代码。
CREATE TRIGGER seat_not_waitlisted BEFORE INSERT ON seats
  WHEN EXISTS (SELECT 1 FROM waitlist WHERE game_id = NEW.game_id AND user_id = NEW.user_id)
  BEGIN SELECT RAISE(ABORT, 'already_waitlisted'); END;
CREATE TRIGGER waitlist_not_seated BEFORE INSERT ON waitlist
  WHEN EXISTS (SELECT 1 FROM seats WHERE game_id = NEW.game_id AND user_id = NEW.user_id)
  BEGIN SELECT RAISE(ABORT, 'already_seated'); END;
-- 被局头移出的人不能再报这一局。
CREATE TABLE game_bans (
  game_id TEXT NOT NULL REFERENCES games(id),
  user_id TEXT NOT NULL REFERENCES users(id),
  PRIMARY KEY (game_id, user_id)
);
-- 08 屏的「邀请谁」。
CREATE TABLE invites (
  game_id    TEXT NOT NULL REFERENCES games(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  invited_by TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (game_id, user_id)
);
-- 09 屏的星标牌友。「一起打过 N 局」从 seats 实时算，不单独存。
CREATE TABLE follows (
  user_id    TEXT NOT NULL REFERENCES users(id),
  target_id  TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, target_id)
);
-- 幂等键：同一个请求被重复提交（双击、超时后重试），只执行一次，之后返回第一次的结果（见 §11.4）。
CREATE TABLE idempotency_keys (
  key        TEXT NOT NULL,
  user_id    TEXT NOT NULL,
  route      TEXT NOT NULL,
  status     INTEGER NOT NULL,
  response   BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, key)
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
  kind       TEXT NOT NULL,                -- 见 §5.3 的提醒类型表
  send_at    INTEGER NOT NULL,
  next_try   INTEGER NOT NULL,             -- 下次尝试时间；第一次等于 send_at，失败后退避
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  sent_at    INTEGER,
  dropped_at INTEGER,                      -- 放弃发送（过期、作废、重试耗尽）
  dedupe_key TEXT NOT NULL UNIQUE          -- 局 + 人 + 类型 + 局版本号
);
CREATE INDEX outbox_due ON outbox(next_try) WHERE sent_at IS NULL AND dropped_at IS NULL;
CREATE TABLE kv (k TEXT PRIMARY KEY, v BLOB NOT NULL);  -- 存 VAPID 密钥等
-- 埋点（phase:2 起使用，表在 0001 就建好）
CREATE TABLE events (
  id      INTEGER PRIMARY KEY,
  at      INTEGER NOT NULL,
  user_id TEXT,
  name    TEXT NOT NULL,                   -- 如 seat_taken / push_enabled / claim_redeemed
  env     TEXT,                            -- wechat / whatsapp / safari / chrome / pwa / other
  props   TEXT                             -- JSON
);
CREATE INDEX events_name_at ON events(name, at);
```

连接设置和并发规则见 §11。

**数据不变量**：每次写事务结束前，以及测试里每一步操作之后，下面这些条件都必须成立。`store.CheckInvariants(gameID)` 负责检查；生产环境在写事务提交前调用，检查不通过就回滚并记 error 日志。
1. 一局的座位数在 0 到 4 之间；牌局状态为 `open` 或 `full` 时，`status='full'` 当且仅当座位数为 4（`done` 和 `cancelled` 的局保留座位记录，不受这条约束）。
2. 候补不为空时，座位数一定是 4（有空座就不该有人在候补）。
3. 牌局状态为 `open` 或 `full` 时，局头一定坐在这一局的某个座位上。
4. 同一个人在同一局里，不会同时出现在座位和候补中（触发器保证）。
5. 牌局状态为 `cancelled` 或 `done` 时，没有未发送、且发送时间晚于结束时间的 `t_minus_1h` 提醒。

## 5. 关键流程

### 5.1 报名、候补、退出、局头转让、移出

下面每个操作都是**一个写事务**，开头用 `BEGIN IMMEDIATE` 拿到写锁，所以所有改动天然串行（见 §11.1）。每个事务都写完以下几样再提交：牌局变化、`version + 1`、outbox 提醒、提交后的 WebSocket 发布回调（§5.5）。

**报名 `join(game, user)`**

1. 读出牌局。状态是 `cancelled` 或 `done` → 返回 409 `game_closed`；用户在 `game_bans` 里 → 返回 403 `banned`。
2. 用户已经在座位上或候补里 → 直接返回当前状态（重复提交也安全）。
3. 有空座：取最小的空座号插入，再给这个用户写一条 `t_minus_1h` 提醒，给局头写一条 `host_seat_taken`。插入后坐满 4 人：状态改为 `full`，给 4 人各写一条 `full`。
4. 没有空座：插入候补，返回排队位置（「你是候补第 2 位」），给局头写一条 `host_waitlist`。
5. 开局前不到 1 小时才报名的：不写 `t_minus_1h`，报名成功的页面上直接显示开局时间（§13.2）。

**退出 `leave(game, user)`**

1. 用户在候补里：删掉候补记录就结束。
2. 用户在座位上：删除座位，作废他的 `t_minus_1h`（写上 `dropped_at`），然后：
   - **候补补位**：候补不为空时，按 `created_at` 取队首，插入到刚空出的座号，删掉他的候补记录，给他写 `promoted`（「有人退出，你补上了」）和 `t_minus_1h`，状态保持 `full`；
   - 候补为空：状态从 `full` 回到 `open`，给剩下的人写 `seat_left`；
   - **局头转让**：如果退出的是局头，先补位，再把 `host_id` 转给座位上 `created_at` 最早的人，给新局头写 `host_transferred`，给其他人写 `host_changed`；
   - 局头退出后座位上已经没有人：把牌局改为 `cancelled`，给所有候补写 `cancelled`。

**局头取消 `cancel(game)`**：状态改为 `cancelled`；作废所有未发送的提醒；给座位上的人和候补各写一条 `cancelled`。

**局头移出 `kick(game, target)`**：效果等同于目标用户「退出」（照样触发补位），另外把他写进 `game_bans`，并给他写 `kicked`。局头不能移出自己，要离开请用「退出」，退出会触发转让。

**修改 `update(game, patch, if_version)`**：只有局头能改。请求必须带 `If-Match: <version>`，版本号不一致就返回 409 `version_conflict`，同时返回最新的牌局数据，由前端提示「局已被修改，请确认」。改了时间时：作废所有未发送的 `t_minus_1h`，按新时间重新生成；给座位上的人写 `rescheduled`。

### 5.2 身份

> 2026-10-14 按 #31（实现）、#32（认领码撞库）和 coord 在 PR #31 里的裁定修订；同日按 #86 逐条对照 `internal/identity` 的代码校正。

- **首次填称呼**（`POST /api/me`）：创建 user 和 device，生成 32 字节随机的 `device_token`，**只通过 HttpOnly cookie 下发**，任何响应体里都不出现它；前端只在 localStorage 里存一个非敏感标记 `gd_has_identity=1`。数据库只存 token 的 sha256。cookie 的名字和属性只在一个地方决定（§10.3）：
  - 生产环境：`__Host-gd_dev`，`HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=400 天`；
  - 开发模式（`GUANDAN_DEV=1`）：`gd_dev`，不带 Secure。真机通过局域网 IP 调试时，浏览器不把这类地址当作安全上下文，带 Secure 的 cookie 会被丢掉。
- **跳出微信前**（`POST /api/claim`）：生成 16 字节随机的 `claim_token`，以及一个 **6 位**认领码（`crypto/rand` + `rand.Int`，没有取模偏差）。两者都是 10 分钟内有效，并记下签发时的客户端网络 `issued_net`（IPv4 取 /24，IPv6 取 /64）。约束：
  - **每个用户同时只有一个有效码**，签发新码时，旧码写上 `used_at` 作废；
  - 签发**每个用户每小时最多 5 次**（令牌桶：容量 5，每 12 分钟补 1 个；超出返回 `429 rate_limited`）；
  - 必须已有身份，没有身份返回 401；
  - 响应体里有 `claim_token`、`code`、`expires_at`（Unix 秒）。这是全站唯一一个在响应体里返回凭证的接口：它本来就要交给前端去拼跳出链接、显示认领码；
  - 有效码之间不重复：签发时在写事务里查重；数据库上还有一个 UNIQUE 部分索引 `claim_code_live(code) WHERE used_at IS NULL` 兜底，作废旧码和签发查重用的判断条件与这个索引一致。
- **凭 token 兑换**（`POST /api/claim/redeem {token, confirm?}`）：
  - 当前设备**没有身份**：直接兑换，给这台设备新挂一条 device 记录，并通过 Set-Cookie 下发新的 device_token。一次性和过期检查由一条 `UPDATE … RETURNING` 完成。前端用 `replaceState` 去掉 URL 里的参数，并在「✓ 已认回身份：X」旁边放一个「不是我」，点了就退出（#36）。
  - 当前设备**已经是 token 的主人**（同一个人在自己的设备上兑换自己的 token）：照样**消耗** token，但沿用原来的 device 记录，不换 cookie，也不新挂设备（#36）。所以签发 token 的那个页面如果自己把它兑换了，token 就用掉了——前端在应用内置浏览器里不自动兑换，正是为了避开这一点（#70）。
  - 当前设备**已有另一个身份**：**不合并，也不自动切换**。服务端返回 `409 claim_conflict`，附上 token 主人的称呼，**此时 token 不会被消耗**，这个请求是只读的。前端显示确认屏，默认选项是「保持当前身份」。用户确认后，前端带上 `confirm:true` 再提交一次，这时才消耗 token，并只把**这台设备**的 `user_id` 换过去：
    - 原来那个身份的数据不迁移、不删除，它的其他设备照常可用；
    - 这台设备上以旧身份开的推送订阅会被删掉，由前端以新身份重新订阅，否则旧身份的提醒会继续发到这台设备（#48 复审）；
    - 这台设备已有的 WebSocket 连接在提交后被断开，让它以新身份重连（#43）；
    - cookie 不换，原来那个 device_token 继续有效。
  - 兑换成功（新挂设备或切换设备）后，向 `user:{token 主人}` 发一条 `claim.redeemed` 事件。
  - 只有 POST 会消耗 token，GET 打开带 `?claim=` 的页面不会。所以微信、WhatsApp 的链接预览抓取器拿到这个链接，也用不掉它。
- **凭认领码兑换**（`POST /api/claim/redeem {code}`）：只允许还没有身份的设备使用；已有身份时同样返回 `400 claim_invalid`，不透露这个码是否存在。兑换时只查有效记录（`used_at IS NULL AND expires_at > now`），成功后给这台设备新挂一条 device 记录并下发 cookie。防护有以下几层：
  1. **只能在签发时的同一个网络兑换**：兑换请求的客户端 IP（§10.13）归一成 /24 或 /64 之后，必须和 `issued_net` 完全相同。`issued_net` 为空的旧记录不做这项检查。这一条只用来挡远程撞库，不是安全边界：印尼大量使用运营商级 NAT，同一个出口后面有很多互不相干的人；
  2. 同一个 IP 每分钟最多 10 次（令牌桶，容量 10，超出返回 `429 rate_limited`，不计入下面的失败预算）；
  3. **全站失败预算**：从窗口开始算起 1 分钟内全站失败超过 20 次，就暂停凭码兑换 5 分钟，同时记 warn 日志。格式不对的码、暂停期间的请求都算失败，所以持续撞库会让暂停一直延续下去。预算只在进程内存里，重启后清零。凭 token 兑换不受这个限制；
  4. 所有失败都返回同一个 `400 claim_invalid`。

  **关于「每个码最多试 5 次」**：这个计数器从来没有生效过，已在 #95 删除（`ClaimCodeMaxAttempts` 常量和 `RedeemByCode` 里的读写都去掉了）。猜错的码查不到记录，本来就数不出每个码被猜了几次；网络不对等失败会让整个写事务回滚，计数也跟着回滚。`claim_tokens.attempts` 列暂时留在库里不再读写，以后在计划好的 breaking 发布里再删。真正限制撞库的是上面第 1–3 条。

  按这些限制估算：6 位码共 10⁶ 种，全站每 10 分钟最多大约 200 次失败尝试，而且攻击者必须和受害者在同一个网段，远程撞库几乎不可能成功。
- **不提供面向用户的身份合并。** 代码里有 `mergeUsers`（`internal/identity/merge.go`），但目前只有测试调用它，`guandan admin` 也还没有合并子命令（§13.6）；任何用户能触发的路径都调不到它。将来做成 admin 命令时，语义以代码为准：
  - 以**先注册**的身份为主（`created_at` 更早；相同时取 id 更小的）；
  - 牌局的局头、座位、候补、禁止名单、邀请、幂等键、星标（两个方向）都迁到主身份上。两边在同一局都有座位时，保留更早坐下的那个；其余主键冲突的记录直接丢弃；
  - 设备、推送订阅、outbox 整体改挂到主身份；
  - 旧身份只写 `merged_into`，不删除。之后带着旧身份的请求，按 `merged_into` 链（最多走 8 步）解析成最终的主身份处理（§10.4）。

### 5.3 提醒（outbox 模式）

**提醒类型**

| kind | 收件人 | 何时 |
|---|---|---|
| `t_minus_1h` | 座位上的每个人 | 开局前 1 小时 |
| `full` | 座位上的 4 人 | 刚坐满 |
| `seat_left` | 座位上剩下的人 | 有人退出且候补为空 |
| `promoted` | 补上座位的候补 | 候补自动补位 |
| `host_seat_taken` / `host_waitlist` | 局头 | 有人报名或进候补 |
| `host_transferred` / `host_changed` | 新局头 / 其他人 | 局头转让 |
| `rescheduled` | 座位上的人 | 局头改了时间 |
| `cancelled` | 座位上的人和候补 | 局取消 |
| `kicked` | 被移出的人 | 局头移出 |
| `invited` | 被邀请的人 | 08 屏发起邀请 |

**发送（单个 worker，至少送达一次）**

- 一个后台 goroutine（`notify.Worker`），每 15 秒取一批：`sent_at IS NULL AND dropped_at IS NULL AND next_try <= now`，每批最多 100 条。
- 发送前先检查：这条提醒是否已经没有意义（例如 `t_minus_1h` 已经过了开局时间，或者牌局已取消）。没有意义的写 `dropped_at`，不发送。
- **网络发送不在写事务里进行**（§11.1）。流程是：读出这批提醒 → 在事务外并发发送（最多 8 个并发，每个请求 10 秒超时）→ 再开一个短的写事务，统一写回结果。
- 发送结果的处理：
  - 成功：写 `sent_at`；
  - 推送服务返回 404 或 410：删除这个订阅，这条提醒视为成功；
  - 返回 429 或 5xx，或者网络错误：`attempts + 1`，按指数退避设置 `next_try`（30 秒、2 分、10 分、30 分……），记下 `last_error`；超过 8 次，或已过开局时间，就写 `dropped_at`。
- **重复送达**：进程在「已发送」和「写回结果」之间崩溃，重启后同一条提醒会再发一次，这是至少送达一次的代价。为了不让用户看到两条通知，Web Push 请求带上 `Topic: <dedupe_key 的哈希>`，通知设置 `tag = dedupe_key`，同一条提醒在设备上只会显示一次。
- VAPID 密钥：首次启动时生成并写进 `kv` 表，公钥通过 `GET /api/push/key` 下发。**密钥一旦更换，所有已有订阅都会失效**，所以必须包含在备份里（§13.3）。

### 5.4 分享

- `GET /g/{code}`：读出 `index.html` 模板，把这一局的 `og:title`、`og:description`、`og:image` 写进去再返回。微信和 WhatsApp 的链接抓取器不执行 JS，只有这样才能拿到每局各自的预览卡片。
- `GET /g/{code}/og.png`（1200×630）和 `GET /g/{code}/poster.png`（1080×1440）：服务端渲染。结果先缓存在内存里，键是 `code + version + 座位数 + 雅加达当天日期`（图上写着「今天」「明天」，跨过午夜要换图），同时设 `Cache-Control: public, max-age=60`。OG 标签里的图片地址是 `og.png?v={version}-{座位数}-{YYYYMMDD}`，让聊天软件按地址缓存的旧图也跟着换；服务端不读 `v`。字体子集含 GB2312 和 Big5 一级常用字，字体里没有的字跳过并记 warn 日志（带短码和码点）。渲染失败时返回同样尺寸的通用图。
- `GET /g/{code}/game.ics`：日历文件。时区写 `Asia/Jakarta`，`UID` 用牌局 id，`SEQUENCE` 用版本号，这样改时间后重新导入，日历里会更新原有事件而不是新增一条。

### 5.5 实时：WebSocket

页面在前台时，通过 WebSocket 实时看到变化；页面不在前台时（iOS 会挂起后台页面，连接随之断开），靠 Web Push 和日历提醒。**REST 接口始终是事实来源**：WebSocket 只负责「告诉你哪里变了」，并附上变化后的数据。断线重连后，客户端一律重新拉一次 REST，不依赖补发历史消息。

**带来的实时交互**

| 场景 | 主题 | 事件 |
|---|---|---|
| 03 牌局详情：别人报名或退出时，座位圆点实时「落座」或「离座」，「差 N 人」随之变化 | `game:{code}` | `seat.taken` · `seat.left` · `game.full` |
| 03 牌局详情：局头改时间或地点时，顶部出现横幅「局头改到 20:00 了」 | `game:{code}` | `game.updated` |
| 03 牌局详情：「3 人正在看这局」 | `game:{code}` | `presence`（人数在服务端合并，同一局每 2 秒最多推一次，#46） |
| 02 首页：列表里每局的「差 N 人」实时变化，有新开的局时出现「有 1 个新局」提示条 | `feed:{area}` | `feed.game_changed` · `feed.game_added` |
| 12 跳出引导：在 Safari 或 Chrome 里认领成功后，微信里那一页自动变成「✓ 已在浏览器中打开」 | `user:{id}` | `claim.redeemed` |
| 10 我的：在另一台设备上开启了提醒或认领了身份，这里的状态同步更新 | `user:{id}` | `me.updated` |

**协议**

- 地址是 `GET /ws`，用 `__Host-gd_dev` cookie 识别身份（没有身份也能连，只是不能订阅 `user:*`）。
- 握手时**必须校验 `Origin` 是本站**，防止跨站 WebSocket 劫持。
- 消息都是 JSON，单条最大 4 KB：
  ```json
  // 客户端发给服务端
  {"op":"sub","topics":["game:8K2","feed:SCBD"]}
  {"op":"unsub","topics":["feed:SCBD"]}
  // 服务端发给客户端
  {"topic":"game:8K2","type":"seat.taken","v":7,"data":{"seats":[...],"missing":1}}
  {"type":"hello","server_time":1760367600}
  ```
- `v` 是牌局的版本号，加上座位变化计数。客户端收到 `v` 不大于本地已有版本的消息时直接丢弃，所以消息乱序或重复都不会出错。
- **首页事件**（`feed:{area}`，#46）：`data` 就是 `GET /api/games` 列表里的一项，外加 `version`，客户端按 `short_code` 直接插入或替换那张卡片，不用再拉 REST：
  ```json
  {"topic":"feed:SCBD","type":"feed.game_changed","v":9,
   "data":{"short_code":"8K2","starts_at":"…","area":"SCBD","venue":"…","kind":"…","status":"open",
           "seat_list":[...],"seats":3,"missing":1,"host":{"id":"…","nickname":"…"},"version":9}}
  ```
  - `feed.game_added`：新开的局（`v` 为 1）；`feed.game_changed`：报名、退出、局头改信息、取消等任何变化之后的最新状态。
  - 去重按 `short_code` 分别比 `v`：同一局 `v` 不大于本地已有的就丢弃（首页一个主题里有很多局，不能像 `game:*` 那样整个主题共用一个版本号）。
  - 局头把局改到别的片区时，原片区和新片区各收到一条 `feed.game_changed`；原片区收到的条目里 `area` 已经是新片区，客户端看到 `area` 不是本片区（或者 `status` 不再是列表里要显示的状态），就把这张卡片移除。
- **在看人数**（`presence`，#46）：只在 `game:*` 主题上有，没有 `v`：
  ```json
  {"topic":"game:8K2","type":"presence","data":{"viewers":3}}
  ```
  - `viewers` 是此刻订阅了这局的**设备数**，包含自己：同一设备开多个标签页算一个；没有身份的连接认不出设备，每条算一个。
  - 订阅一局后，这条连接会收到一条当前人数（和 `subscribed` 回执的先后不固定）；之后人数变了才广播，同一局两次广播至少隔 2 秒，中间的变化合并成一次。客户端直接用最新收到的一条覆盖。
  - presence 可以丢：连接的发送队列快满时服务端直接丢掉这条，不会因为它断开连接；下一次人数变化会纠正。
  - 人数只在进程内存里，不落库；服务重启后随重连重新算出来。
- 订阅权限：`game:*` 和 `feed:*` 是公开的（和公开的 REST 接口能看到的数据一致）；`user:{id}` 只能订阅自己的。每个连接最多订阅 20 个主题。

**服务端实现（`internal/realtime`）**

- **Hub**：一个 `map[topic]set[*conn]`，用一把互斥锁保护。`Publish(topic, msg)` 只把消息投进每个连接的发送队列，不在锁里做网络写。
- **只在事务提交后发布**：`games` 和 `identity` 包在写事务提交**之后**才调用 `Publish`。事务回滚就不发，客户端不会看到「幽灵座位」。实现方式是在 `store` 层登记一个提交后回调。
- **每个连接两个 goroutine**：一个读（处理 `sub`/`unsub`），一个写（从发送队列取消息写出，再加心跳）。发送队列容量 32，**写满就直接断开这个连接**；慢客户端重连后会重新拉 REST，不会拖慢别人。
- **心跳**：服务端每 25 秒发一次 ping，10 秒内没收到 pong 就断开。移动网络和微信内置浏览器常在空闲 60 秒左右掐断连接，25 秒的间隔比它短。
- **在看人数**：Hub 在 `game:*` 主题的订阅者变化（订阅、退订、断开）时排一次广播：离上次广播不到 2 秒就用定时器等到满 2 秒再发，发之前重新数一遍，人数没变就不发。计数状态只在内存里，没人看了就删掉；关闭时停掉所有定时器。
- **限制**：同一 IP 最多 20 个连接；每个连接每秒最多 10 条客户端消息。
- **优雅关闭**：收到 SIGTERM 后，向所有连接发送关闭码 `1012`（服务重启），客户端随后重连。

**客户端约定（前端要实现的部分）**

- 全站只开一个 WebSocket 连接，由各页面登记自己关心的主题；切换页面时更新订阅。
- 断线重连：指数退避（1 秒起步，上限 30 秒）加随机抖动。重连成功后，先重新订阅，再重新拉一次当前页面的 REST。
- 监听 `visibilitychange`：页面回到前台时，如果连接已断，立即重连并刷新数据。
- **降级**：连续 3 次连不上，就改为每 15 秒轮询一次 REST，只在 02 和 03 页面轮询，并在页面上悄悄标一个「离线」小点。

**部署注意**：用内置 autocert 直接对外时不需要额外配置。如果放在反向代理后面，代理要透传 `Upgrade` 和 `Connection` 头，读超时设到 60 秒以上。

## 6. HTTP 接口

```
GET  /healthz                         存活检查：进程还在、能处理请求即可
GET  /readyz                          就绪检查：数据库可写、后台任务的心跳没超时、磁盘剩余空间充足（§12.5）
GET  /api/push/key                    VAPID 公钥
POST /api/me                          首次填称呼
GET  /api/me                          当前身份
POST /api/claim                       签发 claim token 和认领码
POST /api/claim/redeem                凭 {token, confirm?} 或 {code} 兑换身份；设备已有别的身份时返回 409 claim_conflict（§5.2）
DELETE /api/me/devices/{id}           退出这台以外的某个设备（§10.3「退出其他设备」）
GET  /api/games?area=&day=            首页列表
POST /api/games                       开一局
GET  /api/games/{code}                牌局详情
PATCH /api/games/{code}               局头修改（版本号 +1）
POST /api/games/{code}/seat           报名（有空座就落座，没有就进候补）
DELETE /api/games/{code}/seat         退出（座位或候补）
POST /api/games/{code}/cancel         局头取消
POST /api/games/{code}/kick           局头移出 {user_id}
POST /api/games/{code}/invites        邀请 {user_ids}
PUT/DELETE /api/follows/{user_id}     星标牌友
GET  /api/friends                     牌友列表（含一起打过几局）
POST /api/events                      埋点批量上报（phase:2）
POST /api/client-errors               前端错误上报
DELETE /api/me                        注销并删除个人数据（§14）
POST /api/push/subscribe              保存推送订阅（phase:2）
DELETE /api/push/subscribe            取消订阅
POST /api/agent                       蛋蛋（phase:3）：只返回草稿，不写库
GET  /ws                              WebSocket（见 §5.5）
GET  /g/{code} · /g/{code}/og.png · /g/{code}/poster.png · /g/{code}/game.ics
GET  /*                               内嵌的前端页面（单页应用，未知路径回落到 index.html）
```

**蛋蛋 `POST /api/agent`**（#11，§10.10）：只出草稿，不写库。

- 请求：`{"text":"周四晚上 PIK 打一局"}`，1–300 个字。没有身份也能用。
- 不能用时直接返回 JSON 错误，前端退回 08 表单：
  - 没配 `ANTHROPIC_API_KEY`：`503 agent_disabled`；
  - 模型还没吐出第一个字就出错或超时：`503 agent_unavailable`；
  - 同时在调模型的请求超过 4 个：`503 agent_busy`；
  - 超出额度：`429 rate_limited`；
  - 输入不合法：`400 invalid_input`。
- 能用时返回 `text/event-stream`，按顺序推送：
  ```
  event: reply
  data: {"text":"好嘞，周四 19:30 在 PIK"}          ← 蛋蛋的回复，分几片推，前端拼起来显示（最多 200 字）

  event: draft
  data: {"draft":{"starts_at":1791980000,"area":"PIK","venue":"PIK Avenue","kind":"classic"},
         "valid":true}
  ```
  - 最后一条是 `draft` 或 `error`。`error` 的 `data` 是 `{"code":"agent_unavailable","message":"蛋蛋走神了，直接填表吧"}`，表示推到一半时模型出错、超时或者输出不合格，前端同样退回 08 表单。
  - `draft` 的字段和 `POST /api/games` 的请求体一致，认不出来的字段不出现（比如没有备注就没有 `note`）；`kind` 总有值，认不出来时是 `classic`。草稿按开局接口的同一套规则校验：`valid` 为假时，`problem` 说明原因（比如缺地点、时间太近），前端把草稿填进 08 表单让用户补；`valid` 为真时，用户确认后前端照常调用 `POST /api/games`。
- 一次请求总共最多 20 秒（比 `WriteTimeout` 短），超时就按上面的规则退回表单。
- 模型的输出格式是「一句回复 + `===` + 草稿 JSON」，服务端逐字段校验：片区、玩法不在列表里的丢掉，过长的截断。模型拿不到任何工具（§10.10）。

**路由表**（#41）：所有路由在一张表里（`httpapi.Table`：基础设施的几条，加上 `cmd/guandan` 里拼起来的各包 `Routes`，最后是前端页面兜底）。每条路由声明自己对身份的要求，注册、身份识别、授权都按这张表来：

| 要求 | 含义 | 例子 |
|---|---|---|
| `anonymous` | 不识别身份，不读 cookie、不受封禁影响 | `/healthz`、`/g/{code}/*`、`GET /api/push/key`、前端页面 |
| `public` | 识别身份，访客也能用；被封禁 403 `banned` | `GET /api/games`、`POST /api/me`、`POST /api/agent`、`GET /ws` |
| `user` | 必须有身份，访客 401 `unauthorized`；被封禁 403 `banned` | 报名、开局、`GET /api/me` |

`GET /ws` 是唯一的例外（`DeferBan`）：中间件只把封禁状态记进 ctx，握手先查 Origin 和连接数，再以 403 拒绝被封禁的身份。「是不是这一局的局头」要看具体是哪一局，属于业务逻辑，在 games 的 service 里判。`cmd/guandan` 的路由表测试把整张表（方法、路径、身份要求、handler）逐条列出；授权矩阵测试按表对每条路由用访客、普通用户、局头、被封禁的用户各打一次。

中间件：
- 身份：唯一的入口（`httpapi.withIdentity`），按路由表的要求从 `__Host-gd_dev` cookie 解析出用户、查一次封禁，放进 ctx；各包用 `identity.FromContext` 取，不再各自查；
- CSRF：会改数据的请求，必须带 `Content-Type: application/json`，并且 `Origin` 等于本站；
- 限流：按 IP 和设备；
- 统一的 JSON 错误格式：`{"error":{"code":"game_closed","message":"…"}}`；
- 幂等键：`POST` 请求可以带 `Idempotency-Key` 头，前端每次「提交」都生成一个新键（§11.4）；
- panic 恢复：处理请求时如果 panic，返回 500 并记录堆栈，进程不退出（§12.2）；
- 过载保护：同时在处理的请求数有上限（§12.3）；
- 访问日志（slog）。

## 7. 配置（环境变量）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `GUANDAN_DATA` | `/data` | 数据库、备份、证书缓存的目录 |
| `GUANDAN_ADDR` | `:8080` | 不用内置 TLS 时的监听地址 |
| `GUANDAN_DOMAIN` | 空 | 设置后启用 autocert：容器内监听 :8080（ACME 验证和跳转 HTTPS）和 :8443（TLS），由宿主机的 80 和 443 映射过来（nonroot 用户不能监听 1024 以下的端口）；可以写多个域名，逗号分隔（主域名加备用域名） |
| `GUANDAN_PUBLIC_URL` | 根据域名推导 | 生成分享链接和 OG 图 URL 时用 |
| `GUANDAN_ADMIN_EMAIL` | 空 | 填给 Let's Encrypt，也用作 VAPID 的 subject |
| `GUANDAN_TRUSTED_PROXIES` | 空 | 可信反向代理的网段，逗号分隔。只有对端在其中时，才采用 `X-Real-IP`（或 `X-Forwarded-For` 最右边那个不可信的地址）作为客户端 IP，用于按 IP 限流。**只有前面有反向代理、并且代理会覆盖 `X-Real-IP` 时才设置**，否则任何人都能伪造 IP。生产拓扑（#12）设为 `172.17.0.1/32` |
| `GUANDAN_TRUSTED_PROXIES` | 空（谁都不信任） | 只有前面有反向代理、并且代理会覆盖 `X-Real-IP` 时才设置（§10.13）。生产环境：`172.17.0.1/32`（docker 网桥网关） |
| `GUANDAN_METRICS_ADDR` | `127.0.0.1:9090` | `/metrics` 单独监听的地址，不经过对外的路由（§13.7） |
| `GUANDAN_DEV` | 空 | 设为 `1` 时进入开发模式：不启用 TLS，cookie 不加 `Secure`，日志用文本格式（§13.5） |
| `GUANDAN_BACKUP_URL` | 空 | 异地备份目标，S3 兼容地址（§13.3） |
| `GUANDAN_BACKUP_KEY` | 空 | 备份加密用的 age 公钥；设置了备份地址时必须提供 |
| `ANTHROPIC_API_KEY` | 空 | 为空时蛋蛋不可用（`POST /api/agent` 返回 `503 agent_disabled`），前端退回到表单（phase:3） |
| `GUANDAN_AGENT_MODEL` | `claude-sonnet-5-5` | 蛋蛋用的 Claude 模型 |

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
FROM golang:1.27-alpine AS go
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
EXPOSE 8080 8443
ENTRYPOINT ["/guandan"]
```

部署（服务器选雅加达或新加坡的 VPS，用户访问延迟最低）：

```bash
docker run -d --name guandan --restart unless-stopped \
  --read-only --cap-drop ALL --stop-timeout 30 \
  --log-opt max-size=10m --log-opt max-file=5 \
  -p 80:8080 -p 443:8443 -v guandan-data:/data \
  -e GUANDAN_DOMAIN=example.com,backup.example.com -e GUANDAN_ADMIN_EMAIL=… \
  -e GUANDAN_BACKUP_URL=… -e GUANDAN_BACKUP_KEY=age1… \
  ghcr.io/dayuer/jkt-guandan:<tag>
```

- **备份**：每小时一次本地快照，并且**上线前就要配置**加密的异地备份（§13.3）。
- **升级**：执行 `scripts/deploy.sh`（§13.4），其中包括优雅关闭（§12.4）、启动时自动迁移，以及就绪检查失败时自动回滚。
- **镜像**：CI 构建镜像并推送到 GHCR，tag 用提交 SHA。

### 8.1 生产环境（2026-10-14 起，详见 #12）

上面的单容器加 autocert 是通用方案。**生产环境**部署在一台新加坡的共享主机上（`guandan.synon.ai`），同一台机器上还跑着其他站点，所以改用 nginx 在前、容器在后：
- **nginx**：负责 TLS（certbot 用 webroot 方式，目录 `/var/www/certbot`）和静态前端。静态文件放在 `/var/www/guandan/<sha>`，由 `current` 符号链接指向当前版本，发布时原子切换；本站关闭了 `open_file_cache`。`/api/`、`/g/`、`/healthz`、`/readyz` 和 `/ws` 反向代理到 `127.0.0.1:18200`。**每个写了 `proxy_set_header` 的 location，都要把 Host、X-Real-IP、X-Forwarded-For、X-Forwarded-Proto 全部写全**，因为 nginx 遇到 location 里有任何一条 `proxy_set_header`，就不再继承 server 级的设置。
- **容器**：`-p 127.0.0.1:18200:8080 --read-only --cap-drop ALL --security-opt no-new-privileges --memory 256m --env-file /etc/guandan/guandan.env`，容器内只跑 HTTP。
- 发布和回滚用 `scripts/deploy.sh`，nginx 配置的模板在 `deploy/nginx/`（#20 / #34）。

## 9. 测试

所有测试都用 `t.TempDir()` 下的真实 SQLite 文件，执行真实的迁移，不 mock 数据库。时间通过 `clock` 接口注入，测试里可以随意快进。

**并发（CI 中用 `go test -race` 运行；race 检测需要 CGO，所以只在测试时打开，发布构建仍然不用 CGO）**
- 50 个 goroutine 同时抢 4 个座位：结果正好 4 人坐下、46 人进候补，候补顺序与成功拿到写锁的顺序一致，`full` 提醒只产生一组。
- 一边有人报名，一边有人退出，同时进行：每一步之后数据不变量（§4）都成立；候补补位既不丢人也不重复。
- 局头退出与其他人退出同时发生：最后一定有合法的局头，或者牌局变为 `cancelled`。
- 同一个幂等键并发提交两次：只执行一次，两次请求拿到相同的响应。
- 局头在两台设备上同时修改牌局：一个成功，另一个收到 `409 version_conflict`。

**随机操作序列（属性测试）**
- 随机生成 1000 步操作（报名、退出、移出、取消、修改、设备切换身份），涉及 10 个用户和 3 个局。每一步之后检查全部不变量，失败时打印出能复现问题的最短操作序列。

**故障注入**
- 在写事务中途让进程 panic：重启后数据仍处于提交前的状态，不变量成立。
- 推送服务连续返回 500 后恢复：提醒按退避间隔重试，最终送达；超过重试上限的写 `dropped_at`。
- 把「发送成功」和「写回结果」分开执行，中间重启：同一条提醒最多重发一次，且请求带着相同的 `Topic`。
- 数据库写失败（模拟磁盘满）：接口返回 503，`/readyz` 变为失败，进程不崩溃。
- 用同一个数据目录启动第二个实例：第二个实例拒绝启动（§12.1）。

**WebSocket**（§5.5）
- 两个客户端订阅同一局，一个报名后，另一个必须在 1 秒内收到 `seat.taken`；事务回滚时不发任何事件；不读消息的慢客户端会被断开，且不影响其他客户端；跨站 `Origin` 的握手被拒绝。
- 用 `go.uber.org/goleak` 检查：连接断开、服务关闭之后，没有遗留的 goroutine。

**压测**（不进 CI，发布前手动跑）
- 模拟 200 个客户端，在 50 个局上随机报名或退出，同时保持 500 条 WebSocket 连接：写接口的 p99 延迟低于 200 毫秒，没有请求因为等写锁超时。

**前端**：Vitest。
**CI**：`go vet`、`go test -race`、前端构建、`docker build`，job 名为 `test`。

## 10. API 安全

### 10.1 前提：前端代码是公开的

H5 的 JS 谁都能下载、阅读、改写，所以**前端藏不住任何秘密**。「在 JS 里放一个 key，用它给请求签名」这类做法没有安全性，只会制造安全的错觉，本项目不采用。所有安全保证都由服务端提供：

1. **身份**：只认 HttpOnly cookie，前端 JS 读不到它（§10.3）；
2. **授权**：每个请求都在服务端检查「这个人能不能对这个对象做这件事」（§10.4）；
3. **防滥用**：限流、配额、封禁（§10.8）；
4. **输出转义**：所有用户输入的内容，在输出到 HTML、OG 标签、.ics 时都要转义（§10.6）。

### 10.2 威胁模型

| 攻击者想做的事 | 例子 | 主要防线 |
|---|---|---|
| 恶意占座 | 用脚本或大量身份把局占满，或者一个人同时报很多局 | 配额、限流、局头移出、管理员封禁（§10.8） |
| 冒充他人 | 偷 cookie，猜认领码，拿到别人的 claim 链接 | HttpOnly、`__Host-` cookie、claim 一次性且只能用 POST 兑换、认领码有尝试上限（§10.3） |
| 越权操作 | 不是局头却修改、取消牌局，或者移出别人 | 对每个对象做授权检查，配合授权矩阵测试（§10.4、§10.12） |
| 跨站请求 | 其他网站诱导已登录用户报名 | SameSite + Origin 校验 + 只接受 JSON（§10.5） |
| 注入 | 在称呼或地点里放脚本、在 .ics 字段里插入换行 | 默认转义、CSP、`html/template`、字段转义（§10.6） |
| 批量抓取 | 逐个猜短码遍历所有局，收集用户信息 | 6 位随机短码；接口不返回他人的设备信息和手机号；没有搜索用户的接口（§10.9） |
| 拖垮服务 | 大量请求、大量 WebSocket 连接 | 过载保护和限流（§12.3） |
| 窃取密钥 | VAPID 私钥、Claude API key | 只放在服务端；备份加密；日志脱敏（§10.11） |

### 10.3 身份与会话

- **cookie**：名称用 `__Host-gd_dev`，属性为 `HttpOnly; Secure; SameSite=Lax; Path=/`，不设 `Domain`。`__Host-` 前缀让浏览器拒绝子域名或非 HTTPS 页面写入这个 cookie。token 是 32 字节随机数，数据库只存它的 sha256。
- **cookie 被盗就等于身份被盗**，因为是免注册设计，没有第二道验证。所以：
  - 全站只走 HTTPS，并开启 HSTS（§10.7）；
  - 用 HttpOnly 加 CSP（§10.6），让 XSS 读不到 cookie；
  - 10「我的」页面列出所有设备，提供「退出其他设备」，也就是删除其他设备的记录。
- **claim token 会出现在 URL 里**，URL 可能经过 Referer、日志、聊天记录泄露，所以：
  - **只能由前端用 POST 兑换**，打开带 token 的页面（GET）不消耗它。微信或 WhatsApp 的链接预览抓取器只会 GET 这个 URL，不执行 JS，因此不会把一次性 token 用掉；
  - 带 claim 参数的页面返回 `Referrer-Policy: no-referrer`，前端兑换成功后立即用 `replaceState` 去掉参数；
  - 访问日志只记路径（`r.URL.Path`），不记查询参数，所以 `claim=` 不会进日志；
  - 前端在应用内置浏览器里不自动兑换 URL 上的 token（那是为「跳出去」签发的），引导屏关闭时也会把它从地址栏去掉（#70）；
  - token 是 16 字节随机数，数据库只存 sha256；有效期 10 分钟，只能用一次，同一个人在自己设备上兑换也算用掉（§5.2）。
- **认领码**：6 位数字（`crypto/rand` 均匀取值），只给没有身份的设备用，必须在签发时的同一网络（/24、/64）兑换，有每 IP 限流和全站失败预算，详见 §5.2。所有失败一律返回同一个 `400 claim_invalid`，不区分「没有这个码」「过期」「网络不对」。注意每个码的尝试次数上限目前并不生效（§5.2）。
- **claim 不合并身份**：设备已有别的身份时，必须用户确认后才切换，见 §5.2。切换只改这台设备的 `user_id`，不碰两个身份的任何数据。这样，「把自己的 claim 链接发到群里，接管点开链接的人的身份」这条攻击路径就不存在了（#31 审计 P0）。
- **device_token 不出现在前端可读的地方**：只放在 HttpOnly cookie 里，不写 localStorage，任何响应体里也不出现。claim token 和认领码是例外，它们由 `POST /api/claim` 在响应体里返回给本人，因为本来就要显示或拼进链接；它们短期有效、一次性，换到的也只是一个新的 device_token。

### 10.4 授权

- 授权逻辑集中在业务层的一个函数里，形式是 `authz.Can(actor, action, object)`。接口层不写零散的 `if`。

| 动作 | 允许谁 |
|---|---|
| 查看牌局、首页列表 | 任何人，包括还没有身份的访客 |
| 报名、退出 | 有身份的用户，并且没有被这一局禁止 |
| 修改、取消牌局，移出某人 | 只有这一局的当前局头 |
| 订阅 WebSocket 的 `user:{id}` 主题，查看或修改 `/api/me` | 只能是本人 |
| 保存推送订阅 | 只能绑定到当前设备和当前用户 |
| `/metrics` 和管理命令 | 只能从本机执行，不对外开放 |

- **响应只返回白名单字段**：每个接口有自己的响应结构体，不直接把数据库行序列化成 JSON。返回给别人的用户信息只有 `id`、称呼、等级、一起打过几局；`wa_phone`、设备信息、`merged_into` 永远不出现在别人能看到的响应里。
- 被合并的用户（`merged_into` 不为空）发来的请求，统一按合并后的那个用户处理。

### 10.5 跨站请求伪造（CSRF）

- cookie 设置了 `SameSite=Lax`，其他网站发起的 POST 请求不会带上它。
- 会改数据的请求，还必须同时满足：`Content-Type: application/json`，以及 `Origin` 头等于本站的某个域名（主域名或备用域名）。
- WebSocket 握手同样校验 `Origin`（§5.5）。

### 10.6 注入与跨站脚本（XSS）

- **前端**：Preact 默认会转义输出，禁止使用 `dangerouslySetInnerHTML`（用 lint 规则禁止）。
- **`/g/{code}` 往 HTML 里注入 OG 标签**：必须用 Go 的 `html/template` 渲染，它会根据上下文自动转义，**禁止用字符串拼接**。地点和称呼都是用户输入的，如果拼接进去，就是一个存储型 XSS 漏洞。
- **.ics**：对所有文本字段按 RFC 5545 转义，包括 `\`、`;`、`,` 和换行，防止插入额外的日历字段。
- **CSP 响应头**（所有 HTML 响应都带）：
  ```
  default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:; connect-src 'self' wss:; font-src 'self';
  object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'
  ```
  前端不加载任何第三方脚本。
- SQL 一律使用参数化查询，所有 SQL 只写在 `store` 包里。

### 10.7 传输层与安全响应头

- 只提供 HTTPS 服务。80 端口只负责证书签发时的 ACME 验证，以及把请求 301 跳转到 HTTPS。
- `Strict-Transport-Security: max-age=31536000`。**先不加 `includeSubDomains` 和 `preload`**，等主域名和备用域名都确定下来再说。
- `X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: strict-origin-when-cross-origin`（claim 页面例外，见上文）、`Permissions-Policy: camera=(), microphone=(), geolocation=()`。

### 10.8 防滥用

这一块是本项目实际面临的主要威胁，比技术性攻击更现实。

- **配额**：
  - 每个用户同时最多占 5 个未开始局的座位或候补位，防止一个人占满很多局；
  - 每个用户每天最多开 10 局；
  - 其余限流见 §12.3。
- **印尼的移动网络大量使用运营商级 NAT**，很多人共用同一个出口 IP。所以按 IP 的限流只能设得宽松，主要限制按设备和用户计算；按 IP 的限制只用来挡住明显异常的流量。
- **处理问题的手段，从轻到重**：
  1. 局头移出（§5.1）；
  2. 管理员封禁用户或设备（§13.6）；
  3. 对新身份设置冷却期，例如新身份 10 分钟内只能报 1 局；
  4. 出现脚本批量注册时，再在「创建身份」这一步加人机验证。

  第 4 步先不做：人机验证在微信内置浏览器里的兼容性需要单独验证，而且会损害「填个称呼就行」的体验。
- 敏感词过滤，防止涉及金钱（§14）。

### 10.9 防遍历与隐私

- 短码是 6 位 Crockford base32 随机串（约 10 亿种组合），不能顺着编号遍历。
- 首页列表只返回未来 7 天内的牌局，并且分页。不提供按称呼搜索用户的接口；牌友列表只包含和你一起打过局的人。
- 推送内容经过 Web Push 端到端加密。锁屏上会显示称呼和地点，这是用户开启提醒时就预期到的。

### 10.10 蛋蛋（LLM）

- Claude API key 只放在服务端。
- 用户的输入可能包含提示注入。为此，模型的输出**只能是一个牌局草稿 JSON**，服务端按 schema 校验后返回给前端，由用户确认后再走普通的开局接口。模型拿不到任何能直接写数据库或读取他人数据的工具。

### 10.11 密钥与日志

- 密钥通过环境变量注入，或者首次启动时生成后存进数据库（VAPID），不提交到仓库。备份用 age 加密（§13.3）。
- 日志脱敏：不记录 cookie、`Authorization`、claim token、认领码、手机号；请求体只在 debug 级别、并且是开发模式下才记录。

### 10.12 安全测试与供应链

- **授权矩阵测试**：从路由表自动生成测试（`cmd/guandan/routes_test.go`），对每个路由分别用「访客、普通用户、局头、被封禁的用户」四种身份发请求，断言授权结果。新增接口时必须在路由表里声明身份要求，路由表测试会把它逐条列出来，漏写或改了要求都会失败。
- **模糊测试**（Go fuzz）：覆盖输入校验函数、.ics 转义、OG 模板渲染、WebSocket 消息解析。
- 检查所有 HTML 响应都带有 CSP 和其他安全头。
- CI 运行 `govulncheck` 和 `npm audit --omit=dev`，开启 Dependabot。
- **容器**：
  - 使用 distroless 的 nonroot 用户，并且根文件系统只读（`--read-only`，只有 `/data` 可写）；
  - 去掉所有 Linux capabilities（`--cap-drop ALL`）；
  - nonroot 用户不能监听 1024 以下的端口，所以容器内监听 8080 和 8443，由端口映射到宿主机的 80 和 443（§8）。

### 10.13 真实客户端 IP

限流、认领码的网络绑定和访问日志，都通过同一个函数取客户端 IP（#35）：
- 只有当对端地址落在 `GUANDAN_TRUSTED_PROXIES` 里时，才采用 `X-Real-IP`；没有这个头时，取 `X-Forwarded-For` 中从右往左第一个不在可信网段里的地址。对端不可信时，完全不看这两个头。
- **默认不信任任何代理。** 如果容器直接对外暴露、经过 docker-proxy，所有请求的对端都是网桥地址，这时信任网桥网段就等于允许任何人伪造 IP。
- 带 zone 的 IPv6 地址视为非法；以 IPv4 映射形式写的可信网段，会换算成对应的 IPv4 网段。

## 11. 并发模型

### 11.1 数据库：一个写入口，多个读连接

- **写**：整个进程只有一个写连接（`SetMaxOpenConns(1)`），所有写操作都通过 `store.Write(ctx, func(tx) error)` 这一个入口。它负责：开 `BEGIN IMMEDIATE`、调用业务函数、检查不变量、提交，提交成功后再执行登记过的回调（发布 WebSocket 事件、清理缓存）。
- **读**：另开一个读连接池（4 个连接）。WAL 模式下读和写互不阻塞，每次读事务看到的都是一个一致的快照。
- **每个连接**都执行：`PRAGMA journal_mode=WAL; busy_timeout=5000; foreign_keys=ON; synchronous=NORMAL`。
- **写事务里不做任何 I/O**：不发推送、不调用 LLM、不渲染图片、不写 WebSocket。这些都放到事务前或提交后的回调里执行。写事务越短越好，目标是 1 毫秒量级，单个写连接在这个规模下就足够了。代码评审时把这一条当硬规则检查。
- **等待写锁有上限**：`store.Write` 会给传入的 ctx 加上 2 秒超时。超时就返回 `503 busy`，带 `Retry-After: 1`，不让请求越积越多（§12.3）。
- **`SQLITE_BUSY` 的来源**只有备份和外部工具（应用内只有一个写连接，自己不会和自己冲突）。遇到时，对幂等的事务最多重试 3 次，间隔加随机抖动。

### 11.2 业务层：靠事务串行化，不在应用里加锁

- 牌局的所有状态变化（报名、退出、补位、转让、移出、取消、修改）都在 §5.1 的单个写事务里完成，**应用层不需要任何互斥锁**。写锁本身就保证了串行，结果完全确定。
- 数据库约束是最后一道防线：座位主键、`UNIQUE(game_id, user_id)`、座位和候补互斥的触发器、`CHECK(status …)`。应用层的检查出了漏洞，数据库也会拒绝写入。
- 并发写入的先后顺序以拿到写锁的先后为准。候补顺序记的是 `created_at`，这个时间在事务里读取，所以和拿锁的顺序一致。

### 11.3 乐观锁

- 牌局的任何变化都会让 `version + 1`。局头修改牌局（PATCH）时必须带上 `If-Match: <version>`，版本号不一致就返回 409 和最新数据。这样局头在两台手机上同时修改时，不会悄悄覆盖掉另一边的改动。
- WebSocket 消息带的是同一个 `version`，客户端收到不比本地新的版本就丢弃（§5.5）。

### 11.4 幂等

- 微信内置浏览器里经常出现「点两下」和「超时后用户再点一次」。`POST /api/games`（开局）本身不幂等，重复执行会开出两个一样的局。
- 前端每次提交都生成一个 `Idempotency-Key`。服务端在**同一个写事务**里先查 `idempotency_keys`：查到了，直接返回保存的响应；没查到，执行业务，再把响应写进表里。保存 24 小时，由清理任务删除。
- 报名、退出、取消本身就是幂等的（重复执行结果不变），但也一并接受幂等键，保证重复请求拿到的响应也完全一样。

### 11.5 进程内的 goroutine

| 组件 | 并发方式 | 要点 |
|---|---|---|
| HTTP 处理 | 每个请求一个 goroutine（标准库） | 同时处理的请求数有上限（§12.3） |
| WebSocket | 每个连接两个 goroutine（读、写） | 只有写 goroutine 往连接上写；Hub 加锁时只往发送队列里放消息，不做网络写 |
| 提醒 worker | 一个调度 goroutine，加最多 8 个发送 goroutine | 同一时刻只有一个调度者，所以不会两个人同时处理同一条提醒 |
| 清理任务 | 一个 goroutine，每 10 分钟一次 | 见 §13.1 |
| 备份 | 一个 goroutine，每小时一次 | 读快照，不阻塞写 |
| OG 图和海报渲染 | 最多 2 个并发，同一个缓存键用 `singleflight` 合并 | 链接发进 200 人的群时，抓取器和用户会同时来请求这张图；合并后只渲染一次 |

- 所有后台 goroutine 都通过 `supervise(name, fn)` 启动：panic 时恢复，记日志，按退避时间重新启动；每次循环更新一次心跳时间，`/readyz` 会检查心跳（§12.5）。
- 所有 goroutine 都接收根 ctx，关闭时统一取消（§12.4）。测试中用 goleak 检查有没有泄漏。
- 内存中的结构（限流令牌桶、渲染缓存、Hub）都有容量上限，并定期清扫，内存占用不会无限增长。

## 12. 鲁棒性

### 12.1 启动时检查

1. 解析配置：缺少必填项，或值不合法（例如设置了备份地址却没有加密公钥），直接退出并说明原因。
2. **用独占文件锁锁住数据目录**（对 `/data/.lock` 加 `flock`）。拿不到锁说明已有实例在用这个目录，立即退出。这一步保证「只有一个实例」，不靠口头约定：两个容器挂载同一个卷时，第二个起不来。
3. 检查数据目录可写，并且剩余空间超过 500 MB。
4. 打开数据库，执行 `PRAGMA quick_check`。检查失败就退出，并提示从备份恢复（§13.3）。
5. 执行迁移，失败就退出。迁移只做向前兼容的修改：只加表和列，删列分两次发布完成。
   - **编号唯一**：数字前缀由 coord 分配，前缀重复时拒绝启动（包括前导零不同、数值相同的情况）。
   - **降级兼容**：旧二进制遇到不认识的迁移时**照常启动**，只记一条 warn 日志，这样发布失败后可以自动回滚。只有在文件开头写了 `-- breaking: <原因>` 的迁移，才会在 `schema_migrations` 里记一个 breaking 标记；旧二进制遇到不认识的 breaking 迁移时拒绝启动，并提示从发布前的快照恢复（#34）。
6. 后台任务都启动后，`/readyz` 才返回 200。

### 12.2 超时和错误隔离

- `http.Server`：`ReadHeaderTimeout` 5 秒、`ReadTimeout` 15 秒、`WriteTimeout` 30 秒、`IdleTimeout` 120 秒、`MaxHeaderBytes` 16 KB。WebSocket 连接接管之后不受这些超时影响，改由心跳管理（§5.5）。
- 请求体用 `http.MaxBytesReader` 限制在 64 KB 以内，单条 WebSocket 消息限制在 4 KB。
- 所有对外的 HTTP 调用都有自己的超时：推送 10 秒、Claude 30 秒、备份上传 5 分钟；只用自己配置过超时的 `http.Client`，不用 `http.DefaultClient`。
- panic 恢复中间件：单个请求出问题，返回 500，不影响其他请求。后台任务的 panic 由 `supervise` 兜住。
- **输入校验**放在接口入口：称呼 1–12 个字符，去掉控制字符和首尾空白；地点不超过 40 个字符；备注不超过 200 个字符；开局时间必须在现在之后 10 分钟到 30 天之间；片区只接受固定列表里的值。

### 12.3 过载保护

- 同时处理的请求数上限为 256（信号量）。超出时返回 `503`，带 `Retry-After`。
- 等写锁超过 2 秒的请求直接返回 `503 busy`（§11.1）。
- 限流（内存令牌桶，按 IP 和设备分别计数）：

| 动作 | 上限 |
|---|---|
| 创建身份 `POST /api/me` | 每个 IP 每小时 20 次 |
| 开局 | 每个用户每天 10 次 |
| 报名、退出 | 每个用户每分钟 30 次 |
| 认领码兑换 | 每个 IP 每分钟 10 次 |
| WebSocket | 每个 IP 最多 20 条连接，新连接每秒最多 5 条（防止部署后所有客户端同时重连） |
| 蛋蛋 | 每台设备每天 30 次，每个 IP 每天 60 次（没有身份时只按 IP 算）；同时最多 4 个请求在调模型 |

### 12.4 优雅关闭

收到 SIGTERM 后，按以下顺序执行（`docker run` 要加 `--stop-timeout 30`）：
1. `/readyz` 立即开始返回 503，同时停止接受新连接；
2. 向所有 WebSocket 连接发送关闭码 `1012`；
3. 等待正在处理的 HTTP 请求完成，最多 20 秒；
4. 通知提醒 worker 停止：当前这一批发完并写回结果后退出；
5. 执行一次 WAL checkpoint（`PRAGMA wal_checkpoint(TRUNCATE)`），然后关闭数据库；
6. 释放数据目录的文件锁，进程退出。

### 12.5 健康检查和降级

- `/healthz`（存活）：进程能响应就返回 200，供 Docker 的 `HEALTHCHECK` 和重启策略使用。
- `/readyz`（就绪）：同时满足以下条件才返回 200，供外部探活和告警使用：
  - 写事务能正常提交（写一条心跳记录到 `kv`）；
  - 每个后台任务的心跳都在 2 个周期以内；
  - 数据目录剩余空间超过 500 MB；
  - 最近一次备份在 2 小时以内（这是一个可注册的就绪检查：由备份任务（#20）启动时注册，未注册时不参与判断）。

外部依赖出问题时：

| 故障 | 表现 |
|---|---|
| 推送服务（FCM / Apple）不可用 | 提醒按退避重试（§5.3），其他功能不受影响 |
| Claude API 不可用或超时 | 蛋蛋返回 `503 agent_unavailable`，前端退回到 08 屏的表单 |
| Let's Encrypt 签发或续期失败 | 继续使用缓存的证书（autocert 会在到期前 30 天开始重试），同时发出告警 |
| OG 图渲染失败 | 返回一张内置的通用 OG 图，并记 error 日志 |
| 磁盘满（`SQLITE_FULL`） | 写接口返回 503，读接口照常，`/readyz` 失败触发告警 |
| WebSocket 连不上 | 前端改为轮询（§5.5） |
| 备份上传失败 | 本地快照照常保留，下个小时重试，连续 3 次失败就告警 |

## 13. 运维与生命周期

### 13.1 清理任务（每 10 分钟一次）

- 开局 6 小时后，状态是 `open` 或 `full` 的牌局改为 `done`，写上 `ended_at`；状态是 `open` 而且座位上只剩局头一人的，改为 `cancelled`。
- 删除过期的 `claim_tokens`（超过 1 天）、`idempotency_keys`（超过 24 小时），以及已发送或已放弃超过 30 天的 `outbox` 记录。
- 删除 180 天没有出现过的设备，以及属于这些设备的推送订阅。
- 每天执行一次 `PRAGMA optimize`。

### 13.2 时间

- 用 `import _ "time/tzdata"` 把时区数据编进二进制，不依赖镜像里有没有时区文件。
- 「今天」「明天」「周末」都在服务端按 `Asia/Jakarta` 计算，客户端只传筛选条件。
- 开局前不到 1 小时才报名的，不生成 `t_minus_1h`；成局提醒照常发送。
- 所有时间都从 `clock` 接口读取，方便测试（§9）。

### 13.3 备份和恢复

- **本地快照**：每小时执行一次 `VACUUM INTO /data/backup/guandan-<UTC 时间>.db`。打开快照文件，执行 `quick_check` 通过后才算成功。本地保留 48 个小时快照，再加 7 个每日快照。
- **异地备份（上线前必须配置）**：快照用 age 加密后上传到 `GUANDAN_BACKUP_URL`（任意 S3 兼容存储），保留 30 天。数据库里有 VAPID 私钥，所以备份一定要加密。
- **恢复**：`guandan restore <快照文件或远程对象>`。先停掉服务，再把快照校验后放回原位。恢复流程写进 `docs/runbook.md`，上线前至少演练一次。
- 能接受的数据损失：最多 1 小时。以后如果需要更小，可以考虑 SQLite 的 WAL 流式复制方案，但那需要再跑一个进程，所以现在不做。

### 13.4 发布

- CI 在 `main` 上构建镜像，推送到 GHCR，tag 用提交 SHA，同时更新 `:main` 标签。
- 部署到 VPS 有两种方式：手动执行 `scripts/deploy.sh <sha>`，或者在 GitHub Action 里通过 SSH 执行同一个脚本。脚本依次执行：拉取镜像、停掉旧容器（触发优雅关闭）、启动新容器、等 `/readyz` 返回 200；如果 60 秒内没有就绪，就回滚到上一个镜像。
- 停机时间只有几秒，期间 WebSocket 客户端会自动重连。
- **新旧前端兼容**：
  - 服务端在 `hello` 消息（WebSocket）和响应头 `X-App-Build` 里带上构建号。前端发现与自己的构建号不一致时，提示「有新版本，点我刷新」。
  - service worker 采用「先用网络，失败再用缓存」的策略加载 HTML；静态资源文件名带哈希。
  - API 只做向后兼容的修改。
- Docker 日志要限制大小：`--log-opt max-size=10m --log-opt max-file=5`。

### 13.5 本地开发和 PR 验证

换成自托管后，不再有 Netlify 自动生成的 PR 预览环境，改为本地验证：
- `GUANDAN_DEV=1 go run ./cmd/guandan`：开发模式，监听 `:8080`，不启用 TLS。前端开发时运行 Vite 的开发服务器，把 `/api` 和 `/ws` 代理到 `:8080`。
- `guandan seed`：写入设计稿里的示例数据（老王、Lily、阿杰，Pacific Place、PIK Avenue……），auditor 和 design 可以直接在页面上看效果。
- 提交一个只含 `.gitkeep` 的 `web/dist/` 占位目录，保证没装 node 的环境也能执行 `go build` 和 `go test`。
- PR 验证：在本地执行 `docker build` 和 `docker run`，再用 `guandan seed` 写入数据。auditor 在远程环境如果没有 Docker，就用 `go run` 加 `seed`。

### 13.6 管理（子命令，不做后台页面）

```
guandan admin ban <user|device>           封禁（之后的请求一律返回 403）
guandan admin game cancel <code>          强制取消牌局
guandan admin user delete <id>            删除用户数据（处理 §14 的删除请求）
guandan admin stats [--since 7d]          查看 §8 指标（来自 events 表）
guandan restore <快照>                    从备份恢复（§13.3）
```

执行方式：`docker exec guandan /guandan admin …`。这些命令和服务共用同一个数据库，写操作同样通过 `store.Write`，不会与正在运行的服务冲突。

### 13.7 监控

- 外部定时探测 `/readyz`（任何免费的探活服务都可以），失败时通知仓库所有者。
- 日志：`slog` 输出 JSON。每个请求带 `request_id`、路由、状态码、耗时、写锁等待时间。写锁等待超过 100 毫秒、`503 busy`、panic 都记为 warn 及以上级别。
- `/metrics` 只允许内网或 localhost 访问，内容包括：请求数和延迟、写锁等待时间、WebSocket 连接数、outbox 积压量和发送失败数、备份时间。

## 14. 合规（印尼）

- **个人数据保护法（UU PDP，2024 年 10 月起全面执行）**：
  - 采集的个人数据只有称呼、可选的 WhatsApp 号码、设备信息、埋点数据，不额外收集其他信息；
  - 提供隐私说明页面 `/privacy`，写明收集什么、用来做什么、保存多久、怎样删除；
  - `DELETE /api/me` 用于注销：删除称呼、WhatsApp 号码、设备和推送订阅；座位记录改成「已注销牌友」，这样历史局的人数统计不会出错；
  - 保留期限：设备 180 天未活跃就删除（§13.1）；埋点数据 1 年后删除；备份 30 天。
- **赌博监管**：页面上已经写明「不涉及金钱输赢」。服务端对牌局备注和地点做敏感词过滤（赌、钱、筹码、judi、taruhan……），命中就拒绝提交，并提示「请勿涉及金钱」。词表放在仓库里，可以随时更新。
- **域名**：`.id` 域名通常需要印尼身份证或本地公司才能注册，所以建议主域名用 `.com` 之类的通用后缀（见 #12）。
- **电子系统运营商登记（PSE）**：面向印尼用户的服务原则上需要向 Komdigi 登记。这个规模下被查的概率低，但已记入风险清单，用户量明显增长后再评估。

## 15. 与原方案相比的影响

| 原方案 | 现在 |
|---|---|
| Netlify 托管 + Functions | Go 进程同时提供页面和 API |
| Supabase Postgres | 内嵌 SQLite，数据库文件在 `/data` |
| Netlify Scheduled Functions | 进程内的 outbox 定时任务 |
| Netlify Edge Function 出 OG 图 | Go 服务端渲染 |
| 函数区域和数据库区域可能不在一处 | 服务器直接放在雅加达或新加坡，没有这个问题 |
| 托管平台负责高可用 | 单台 VPS：要自己负责备份和监控（`/healthz` 加外部探活） |
| 没有实时能力 | 内置 WebSocket（§5.5），进程内 Hub，单实例不需要 Redis |
| 只有报名和退出 | 候补自动补位、局头自动转让、局头移出（§5.1） |
| 平台托管 | API 安全（§10）；单写入口加事务串行化（§11）；启动检查、超时、过载保护、优雅关闭、降级（§12）；每小时加密异地备份（§13.3） |
