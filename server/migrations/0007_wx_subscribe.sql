-- 小游戏订阅消息的剩余次数（#137，docs/server-design.md §5.3「订阅消息通道」）。
--
-- 一次性订阅：用户在 wx.requestSubscribeMessage 里每同意一次某个模板，就能给他发一条这个模板的消息。
-- 客户端把同意的模板上报（POST /api/wx/subscribe）时 remaining + 1，发送成功一次 - 1，为 0 时不发。
-- openid 不存在这里，发送时从 user_links（wx_openid:<appid>）取。注销（DELETE /api/me、
-- guandan admin user delete）时这个用户的记录全部删除（§14）。
-- 只新建一张表，向前兼容：旧版本的程序不读它。
CREATE TABLE wx_subscribe_quota (
  user_id     TEXT NOT NULL REFERENCES users(id),
  template_id TEXT NOT NULL,
  remaining   INTEGER NOT NULL DEFAULT 0 CHECK (remaining >= 0),
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (user_id, template_id)
);
