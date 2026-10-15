-- 候补补位上来的座位，记下他原来排候补的时间（Unix 秒），#160。直接报名坐下的座位是空。
--
-- 详情和列表按当前身份给出「我加入这一局的时间」和「补位的时间」，让换了设备、本机快照没了的客户端
-- 也能判断改时间横幅、「轮到你了」弹层要不要出：
--   加入时间 = COALESCE(waitlisted_at, created_at)（候补里的人用 waitlist.created_at）；
--   补位时间 = waitlisted_at 不为空时的 created_at（补位那一刻插入的座位）。
-- seats.created_at 的含义不变（坐下的时间，转交局头按它排）。只加一个可空列，旧版本照常读写，不是 breaking。
ALTER TABLE seats ADD COLUMN waitlisted_at INTEGER;
