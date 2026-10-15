-- 记分跟进（#168、#172，#165 复审意见，docs/server-design.md §5.6、§11.4）。
-- 只加列、加表，向前兼容：旧版本的程序不写 fingerprint（为空，按不比对处理），也不读新表。

-- 幂等键的请求指纹：同一个键配了不同的请求体时返回 422，不再当成重放静默返回 200。
-- 是请求里业务参数的 sha256（十六进制），由服务端从解析后的参数算出，和 JSON 的字段顺序、空白无关。
-- 为空表示写这一行时还不比指纹（旧版本写的、或者没有请求体的接口）。
ALTER TABLE idempotency_keys ADD COLUMN fingerprint TEXT;

-- 记分开始以后，每个座号最后一个离开的人（退出、被移出、注销都算）。
-- 过了报名截止（JoinGrace）只有他能回到这个座号，而且这个座号得还空着：分队按座号定，
-- 回原座号不会换队（#168）。有人在 JoinGrace 之内补进这个座号、后来又走了，就换成补进来的人。
-- 被移出的人照样记一行，但报名先查 game_bans，所以回不来。
CREATE TABLE score_seat_leavers (
  game_id TEXT NOT NULL REFERENCES score_matches(game_id),
  seat_no INTEGER NOT NULL CHECK (seat_no BETWEEN 1 AND 4),
  user_id TEXT NOT NULL REFERENCES users(id),
  left_at INTEGER NOT NULL,
  PRIMARY KEY (game_id, seat_no)
);
CREATE INDEX score_seat_leavers_user ON score_seat_leavers(game_id, user_id);

-- 每局当时四个座位上的人（#172）：中途换人后，换人前的局仍按当时的人显示称呼，S15 战报也按实际打过的人列。
-- 记一局时从 seats 抄下来；改一局只改结果，不换人。
ALTER TABLE score_rounds ADD COLUMN seat1_user TEXT REFERENCES users(id);
ALTER TABLE score_rounds ADD COLUMN seat2_user TEXT REFERENCES users(id);
ALTER TABLE score_rounds ADD COLUMN seat3_user TEXT REFERENCES users(id);
ALTER TABLE score_rounds ADD COLUMN seat4_user TEXT REFERENCES users(id);

-- 0008 以来已经记下的局：按现在的座位补上（尽力而为——0008 上线不久，记分中途换过人的局极少）。
-- 那个座号现在空着的留空，客户端退回用现在的座位。
UPDATE score_rounds SET
  seat1_user = (SELECT user_id FROM seats s WHERE s.game_id = score_rounds.game_id AND s.seat_no = 1),
  seat2_user = (SELECT user_id FROM seats s WHERE s.game_id = score_rounds.game_id AND s.seat_no = 2),
  seat3_user = (SELECT user_id FROM seats s WHERE s.game_id = score_rounds.game_id AND s.seat_no = 3),
  seat4_user = (SELECT user_id FROM seats s WHERE s.game_id = score_rounds.game_id AND s.seat_no = 4);
