/**
 * 时间显示。
 *
 * 服务端存的是 UTC 的 Unix 秒，「今天 / 明天 / 周末」也在服务端按 Asia/Jakarta 算（§13.2），
 * 客户端只负责把时间戳渲染成 WIB 的文本。雅加达是 UTC+7，没有夏令时。
 */

const WIB = "Asia/Jakarta";

const hm = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
  timeZone: WIB,
});

const md = new Intl.DateTimeFormat("zh-CN", {
  month: "numeric",
  day: "numeric",
  timeZone: WIB,
});

const wd = new Intl.DateTimeFormat("zh-CN", { weekday: "short", timeZone: WIB });

/** 「19:30」 */
export function timeHM(unixSeconds: number): string {
  return hm.format(new Date(unixSeconds * 1000));
}

/** 「周四」 */
export function weekday(unixSeconds: number): string {
  return wd.format(new Date(unixSeconds * 1000));
}

/** 「10月18日」 */
export function monthDay(unixSeconds: number): string {
  return md.format(new Date(unixSeconds * 1000));
}

/** WIB 的日期键，用来判断是不是同一天。 */
function dayKey(unixSeconds: number): string {
  const parts = new Intl.DateTimeFormat("en-CA", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    timeZone: WIB,
  }).format(new Date(unixSeconds * 1000));
  return parts;
}

/**
 * 详情页顶部那行字：「今天 · 周四」「明天 · 周五」「10月18日 · 周日」。
 * now 由调用方给（服务端的 server_time 或者本地时间都行），方便测试。
 */
export function dayLabel(unixSeconds: number, now: number = Math.floor(Date.now() / 1000)): string {
  const target = dayKey(unixSeconds);
  const today = dayKey(now);
  const tomorrow = dayKey(now + 24 * 3600);

  if (target === today) return `今天 · ${weekday(unixSeconds)}`;
  if (target === tomorrow) return `明天 · ${weekday(unixSeconds)}`;
  return `${monthDay(unixSeconds)} · ${weekday(unixSeconds)}`;
}

/** 「差 2 人」/「已满」 */
export function missingLabel(missing: number): string {
  return missing > 0 ? `差 ${missing} 人` : "已满";
}
