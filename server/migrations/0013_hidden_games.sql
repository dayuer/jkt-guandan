-- 「我的牌局」历史里删掉一局（#211 2b）：删除只是从**我自己的**列表里拿掉，
-- 不删牌局本身，不动同局别人的列表、计分记录和牌友关系。
--
-- 所以单独一张表记「谁把哪一局从自己的历史里藏了」。注销时按 user_id 一起删（§14）。
-- 隐藏过一次就一直在（用户说删了不能恢复），再删一次是幂等的。
CREATE TABLE hidden_games (
  user_id   TEXT NOT NULL REFERENCES users(id),
  game_id   TEXT NOT NULL REFERENCES games(id),
  hidden_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, game_id)
);

-- 历史列表要按「我藏过哪些」过滤，按 user_id 查。
CREATE INDEX hidden_games_user_id_idx ON hidden_games(user_id);
