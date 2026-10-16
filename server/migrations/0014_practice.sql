-- 练习局（#88 第 1 件）：一个人和三个 AI 打的一局牌。
--
-- 为什么不塞进 games：练习局没有座位、候补、报名、邀请、分享，也没有短码 —— 它是**单人**的、
-- 只属于一个人的。塞进 games 会把 seats / waitlist / feed / invites 那一整套拖进来
-- （games.Kinds 里那个 teaching 标签是**约局**的标签，不是这个，别复用）。
--
-- state 存的是 rules.Game 的 JSON：**初始局面 + 动作序列**（server/internal/rules/play.go 的
-- wireGame）。读回时把动作重放一遍，并核对「轮到谁」「谁出完了」对不对 —— 于是不存在
-- 「当前手牌和动作对不上」的自相矛盾存档，坏存档读的一刻就报错，而且**不用新写代码**。
-- 有意**不存「当前局面」快照**：两份数据迟早会打架，重放永远自洽。
CREATE TABLE practice_games (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  level      INTEGER NOT NULL,                              -- 这一副打几（rules.Rank 的整数）
  seat       INTEGER NOT NULL CHECK (seat BETWEEN 1 AND 4), -- 我坐几号座，其余三家是 AI
  state      TEXT NOT NULL,                                 -- rules.Game 的 JSON（见上）
  revision   INTEGER NOT NULL DEFAULT 1,                    -- 乐观锁：出牌带 expected_revision
  created_at INTEGER NOT NULL,
  ended_at   INTEGER                                        -- 收局时间；NULL 表示还开着
);

-- 「一人同时只有一局」（#88 设计第二节）：建新局会把上一局收掉。
-- 用**部分唯一索引**在库里钉住，而不是只在代码里判断 —— 两个请求同时建局也不会漏出两局。
-- （收掉的局 ended_at 不再是 NULL，所以不占这个唯一性：复盘还要取它。）
CREATE UNIQUE INDEX practice_games_one_open_per_user
  ON practice_games(user_id) WHERE ended_at IS NULL;
