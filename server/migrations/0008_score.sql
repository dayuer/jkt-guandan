-- 牌桌计分（#165，design/guandan-minigame/README.md §7，docs/server-design.md §5.6）。
--
-- 只存事实：开局约定、每局谁头游、搭档第几。级数、打谁的级、A 没过几次、第几场、
-- 是否结束都不落库，每次由 server/internal/scoring 从第 1 局重算（改一局 = 替换再重算）。
-- 不涉及金钱：没有分数、倍率、金额（#91）。
-- 只新建两张表，向前兼容：旧版本的程序不读它们。

-- 一局牌开始记分时写一行：开局约定。「再来一场」沿用同一份约定，场次由规则从各局推出来，
-- 所以一局牌只有一行（表名沿用设计稿 7.7 的 score_matches）。
CREATE TABLE score_matches (
  game_id      TEXT PRIMARY KEY REFERENCES games(id),
  kind         TEXT NOT NULL CHECK (kind IN ('classic','no-level')),  -- 开始时按牌局的 kind 定下，teaching 按 classic
  a_fail_limit INTEGER NOT NULL CHECK (a_fail_limit IN (0, 3)),        -- 打自己的 A 第几次没过回到 2；0 = 不回 2
  started_by   TEXT NOT NULL REFERENCES users(id),
  started_at   INTEGER NOT NULL,
  revision     INTEGER NOT NULL DEFAULT 1  -- 开始、记一局、改一局各 +1；实时事件按它去重（§5.5）
);

-- 每局的出游事实。局号从 1 连续往下编，跨场不重来；同一局号只能有一条（两人同时记同一局时，
-- 后到的拿到 409 和已经记下的那条，S12）。
CREATE TABLE score_rounds (
  game_id       TEXT NOT NULL REFERENCES score_matches(game_id),
  round_no      INTEGER NOT NULL CHECK (round_no >= 1),
  head_seat     INTEGER NOT NULL CHECK (head_seat BETWEEN 1 AND 4),
  partner_place INTEGER NOT NULL CHECK (partner_place BETWEEN 2 AND 4),  -- 2 二游（双上）/ 3 三游 / 4 末游
  recorded_by   TEXT NOT NULL REFERENCES users(id),
  created_at    INTEGER NOT NULL,
  edited_by     TEXT REFERENCES users(id),  -- 最近一次改这一局的人；没改过为空
  edited_at     INTEGER,
  PRIMARY KEY (game_id, round_no)
);
