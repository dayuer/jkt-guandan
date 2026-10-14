-- 局头改了时间或地点的时刻（Unix 秒），#114。空表示从没改过。
-- 前端拿它和本机「上次看到的时间」比较，决定要不要显示「局头把时间改到 X 了」横幅。
-- 只加一个可空列，旧版本照常读写，不是 breaking。
ALTER TABLE games ADD COLUMN rescheduled_at INTEGER;
