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

// month 用 long 才是「10月17日」；numeric 在 zh-CN 下会变成「10/17」。
const md = new Intl.DateTimeFormat("zh-CN", {
  month: "long",
  day: "numeric",
  timeZone: WIB,
});

// 设计稿的写法是「10/13」，不是「10月13日」。
const slash = new Intl.DateTimeFormat("en-US", {
  month: "2-digit",
  day: "2-digit",
  timeZone: WIB,
});

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

/** WIB 下的「周几」，用数组而不是 Intl——设计稿要的是「周二」这种紧凑写法。 */
function weekdayCompact(unixSeconds: number): string {
  const name = new Intl.DateTimeFormat("en-US", { weekday: "short", timeZone: WIB }).format(
    new Date(unixSeconds * 1000),
  );
  const map: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };
  return weekdays[map[name] ?? 0];
}

const wd = new Intl.DateTimeFormat("zh-CN", { weekday: "short", timeZone: WIB });

/** 「19:30」 */
export function timeHM(unixSeconds: number): string {
  return hm.format(new Date(unixSeconds * 1000));
}

/** 「周四」 */
export function weekday(unixSeconds: number): string {
  return wd.format(new Date(unixSeconds * 1000));
}

/** 「10/13」，和设计稿一致。 */
export function slashDate(unixSeconds: number): string {
  return slash.format(new Date(unixSeconds * 1000));
}

/** 「周二」 */
export function weekday2(unixSeconds: number): string {
  return weekdayCompact(unixSeconds);
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

  const stamp = `${slashDate(unixSeconds)} ${weekday2(unixSeconds)}`;
  if (target === today) return `今天 ${stamp}`;
  if (target === tomorrow) return `明天 ${stamp}`;
  return stamp;
}

/**
 * 「今天」/「明天」/「10月18日」——话术里用，不带星期和具体日期。
 * （首页卡片那行小字另有写法，它要的是 10/17 这种紧凑日期。）
 */
export function dayShort(unixSeconds: number, now: number = Math.floor(Date.now() / 1000)): string {
  const target = dayKey(unixSeconds);
  if (target === dayKey(now)) return "今天";
  if (target === dayKey(now + 24 * 3600)) return "明天";
  return monthDay(unixSeconds);
}

// 这两个不碰时间，两端一样，#253 起住在 app/shared；这里转出去，format 的使用方不用动。
export { kindLabel, missingLabel } from "../../shared/src/labels";
