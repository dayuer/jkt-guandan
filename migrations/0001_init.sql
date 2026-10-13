-- 初始 schema，对应 docs/server-design.md §4。
-- 时间统一存 UTC 的 Unix 秒（INTEGER），显示时再转成 Asia/Jakarta（WIB）。

CREATE TABLE users (
  id          TEXT PRIMARY KEY,            -- ULID
  nickname    TEXT NOT NULL,
  wa_phone    TEXT,                        -- 可选，绑定后用于找回
  merged_into TEXT REFERENCES users(id),   -- 身份合并后指向主身份（§5.2）
  created_at  INTEGER NOT NULL
);

-- 每个浏览器环境（微信内置、Safari、主屏幕 PWA ……）一条。只存 device_token 的 sha256。
CREATE TABLE devices (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  token_hash BLOB NOT NULL UNIQUE,
  ua         TEXT,
  last_seen  INTEGER NOT NULL
);
CREATE INDEX devices_user_id_idx ON devices(user_id);

-- 跨环境交接用的一次性令牌：10 分钟有效、只能用一次；附 4 位认领码作兜底。
CREATE TABLE claim_tokens (
  token_hash BLOB PRIMARY KEY,             -- sha256(claim_token)
  code       TEXT NOT NULL,                -- 4 位认领码
  user_id    TEXT NOT NULL REFERENCES users(id),
  expires_at INTEGER NOT NULL,
  used_at    INTEGER,
  attempts   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX claim_code_live ON claim_tokens(code) WHERE used_at IS NULL;

CREATE TABLE games (
  id         TEXT PRIMARY KEY,
  -- 6 位 Crockford base32 随机串，防止顺着编号把所有的局都遍历出来（§10.9）
  short_code TEXT NOT NULL UNIQUE,
  host_id    TEXT NOT NULL REFERENCES users(id),
  starts_at  INTEGER NOT NULL,
  area       TEXT NOT NULL,                -- SCBD / PIK / ...
  venue      TEXT NOT NULL,
  kind       TEXT NOT NULL,                -- classic / no-level / teaching
  note       TEXT,
  status     TEXT NOT NULL DEFAULT 'open'
             CHECK (status IN ('open','full','cancelled','done')),
  -- 牌局有任何变化（改时间、改地点、座位变动、换局头）都 +1，用于乐观锁和消息排序
  version    INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  ended_at   INTEGER                       -- 变成 cancelled 或 done 的时间
);
CREATE INDEX games_host_id_idx ON games(host_id);
CREATE INDEX games_feed ON games(area, starts_at) WHERE status IN ('open','full');

CREATE TABLE seats (
  game_id    TEXT NOT NULL REFERENCES games(id),
  seat_no    INTEGER NOT NULL CHECK (seat_no BETWEEN 1 AND 4),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (game_id, seat_no),
  UNIQUE (game_id, user_id)
);
CREATE INDEX seats_user_id_idx ON seats(user_id);

-- 候补：局满后报名的人按先后顺序排队；有人退出时队首自动补上（§5.1）。
CREATE TABLE waitlist (
  game_id    TEXT NOT NULL REFERENCES games(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,             -- 排队顺序以它为准（相同时再按 rowid）
  PRIMARY KEY (game_id, user_id)
);
CREATE INDEX waitlist_order ON waitlist(game_id, created_at);

-- 同一个人在一局里只能占一个位置：不能既在座位上又在候补里。
-- 由触发器在数据库层强制，不靠应用代码（§11.2 的最后一道防线）。
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

-- 幂等键：同一个请求被重复提交（双击、超时后重试），只执行一次（§11.4）。
CREATE TABLE idempotency_keys (
  key        TEXT NOT NULL,
  user_id    TEXT NOT NULL,
  route      TEXT NOT NULL,
  status     INTEGER NOT NULL,
  response   BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_keys_created ON idempotency_keys(created_at);

CREATE TABLE push_subscriptions (
  endpoint   TEXT PRIMARY KEY,
  p256dh     TEXT NOT NULL,
  auth       TEXT NOT NULL,
  user_id    TEXT NOT NULL REFERENCES users(id),
  device_id  TEXT NOT NULL REFERENCES devices(id),
  fail_count INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

-- 提醒都写在 outbox 里，重启不丢（§5.3）。
-- dedupe_key = 局 + 人 + 类型 + 局版本号。
CREATE TABLE outbox (
  id         INTEGER PRIMARY KEY,
  game_id    TEXT NOT NULL REFERENCES games(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  kind       TEXT NOT NULL,                -- full / t_minus_1h / seat_left / rescheduled
  send_at    INTEGER NOT NULL,
  next_try   INTEGER NOT NULL,             -- 下次尝试时间；第一次等于 send_at，失败后退避
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  sent_at    INTEGER,
  dropped_at INTEGER,                      -- 放弃发送（过期、作废、重试耗尽）
  dedupe_key TEXT NOT NULL UNIQUE
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
