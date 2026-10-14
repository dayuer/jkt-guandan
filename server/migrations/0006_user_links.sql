-- 微信身份关联（#84，docs/server-design.md §4、§5.2）。
--
-- user_id（ULID）仍是唯一主键；unionid、openid 只是指向它的关联，不当作 user_id 用，也不返回给客户端。
--   provider = 'wx_unionid'          subject = unionid
--   provider = 'wx_openid:<appid>'   subject = openid
-- 先按 wx_unionid 找，找不到再按 wx_openid:<appid> 找；unionid 后来才出现时补写。
-- 不存 session_key。注销（DELETE /api/me）时这个用户的关联全部删除（§14）。
-- 只新建一张表，向前兼容：旧版本的程序不读它。
CREATE TABLE user_links (
  provider   TEXT NOT NULL,
  subject    TEXT NOT NULL,
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (provider, subject),
  UNIQUE (provider, user_id)  -- 一个用户在同一个 provider 下只有一个关联
);
CREATE INDEX user_links_user_id_idx ON user_links(user_id);
