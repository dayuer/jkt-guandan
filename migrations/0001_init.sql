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
CREATE INDEX games_starts_at_idx ON games(starts_at);
CREATE INDEX games_host_id_idx ON games(host_id);

CREATE TABLE seats (
  game_id    TEXT NOT NULL REFERENCES games(id),
  seat_no    INTEGER NOT NULL CHECK (seat_no BETWEEN 1 AND 4),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (game_id, seat_no),
  UNIQUE (game_id, user_id)
);
CREATE INDEX seats_user_id_idx ON seats(user_id);

CREATE TABLE push_subscriptions (
  endpoint   TEXT PRIMARY KEY,
  p256dh     TEXT NOT NULL,
  auth       TEXT NOT NULL,
  user_id    TEXT NOT NULL REFERENCES users(id),
  device_id  TEXT NOT NULL REFERENCES devices(id),
  fail_count INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

-- 提醒都写在 outbox 里，重启不丢（§5.3）。dedupe_key = 局 + 人 + 类型 + 局版本号。
CREATE TABLE outbox (
  id         INTEGER PRIMARY KEY,
  game_id    TEXT NOT NULL REFERENCES games(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  kind       TEXT NOT NULL,                -- full / t_minus_1h / seat_left / rescheduled
  send_at    INTEGER NOT NULL,
  sent_at    INTEGER,
  dedupe_key TEXT NOT NULL UNIQUE
);
CREATE INDEX outbox_due ON outbox(send_at) WHERE sent_at IS NULL;

CREATE TABLE kv (
  k TEXT PRIMARY KEY,
  v BLOB NOT NULL                          -- 存 VAPID 密钥等
);
