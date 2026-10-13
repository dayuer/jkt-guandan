-- 封禁（docs/server-design.md §13.6 的 guandan admin ban）。
-- 非空表示被封禁的时间；身份中间件（#3）看到它就对之后的请求一律返回 403。
ALTER TABLE users ADD COLUMN banned_at INTEGER;
ALTER TABLE devices ADD COLUMN banned_at INTEGER;
