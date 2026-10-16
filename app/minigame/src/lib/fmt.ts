// 时间显示，和 app/h5/src/format.ts 导出同一组函数（打包时 H5 模块里的 ./format 会被换成这里）。
// 不用 Intl 时区：安卓微信的 JS 引擎不保证带完整的 ICU。雅加达是 UTC+7，没有夏令时，直接加 7 小时。

const OFFSET = 7 * 3600;
const WEEK = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const pad = (n: number) => String(n).padStart(2, "0");

function wib(unixSeconds: number): Date {
  return new Date((unixSeconds + OFFSET) * 1000);
}

function dayKey(unixSeconds: number): string {
  const d = wib(unixSeconds);
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
}

const nowSec = () => Math.floor(Date.now() / 1000);

/** 「19:30」 */
export function timeHM(unixSeconds: number): string {
  const d = wib(unixSeconds);
  return `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
}

/** 「周四」 */
export function weekday(unixSeconds: number): string {
  return WEEK[wib(unixSeconds).getUTCDay()];
}

export const weekday2 = weekday;

/** 「10/13」 */
export function slashDate(unixSeconds: number): string {
  const d = wib(unixSeconds);
  return `${pad(d.getUTCMonth() + 1)}/${pad(d.getUTCDate())}`;
}

/** 「10月18日」 */
export function monthDay(unixSeconds: number): string {
  const d = wib(unixSeconds);
  return `${d.getUTCMonth() + 1}月${d.getUTCDate()}日`;
}

// 不碰时间，两端一样，#253 起住在 app/shared；这里转出去，fmt 的使用方不用动。
export { kindLabel, missingLabel } from "../../../shared/src/labels";

/** 详情页顶部：「今天 10/13 周二」 */
export function dayLabel(unixSeconds: number, now: number = nowSec()): string {
  const target = dayKey(unixSeconds);
  const stamp = `${slashDate(unixSeconds)} ${weekday(unixSeconds)}`;
  if (target === dayKey(now)) return `今天 ${stamp}`;
  if (target === dayKey(now + 86400)) return `明天 ${stamp}`;
  return stamp;
}

/** 话术里用：「今天」/「明天」/「10月18日」 */
export function dayShort(unixSeconds: number, now: number = nowSec()): string {
  const target = dayKey(unixSeconds);
  if (target === dayKey(now)) return "今天";
  if (target === dayKey(now + 86400)) return "明天";
  return monthDay(unixSeconds);
}

/** 首页卡片那行小字：「今天」/「明天」/「10/17」 */
export function cardDay(unixSeconds: number, now: number = nowSec()): string {
  const target = dayKey(unixSeconds);
  if (target === dayKey(now)) return "今天";
  if (target === dayKey(now + 86400)) return "明天";
  return slashDate(unixSeconds);
}

/** 雅加达某天某时的 Unix 秒。dayOffset 相对今天（WIB）。 */
export function wibAt(dayOffset: number, hour: number, minute: number, now: number = nowSec()): number {
  const d = wib(now);
  const midnight = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()) / 1000 - OFFSET;
  return midnight + dayOffset * 86400 + hour * 3600 + minute * 60;
}

/** 某个时间戳是 WIB 的第几天（相对今天）和几点几分，编辑时间时用。 */
export function wibParts(unixSeconds: number, now: number = nowSec()): { dayOffset: number; hour: number; minute: number } {
  const t = wib(unixSeconds);
  const midnightTarget = Date.UTC(t.getUTCFullYear(), t.getUTCMonth(), t.getUTCDate()) / 1000;
  const n = wib(now);
  const midnightNow = Date.UTC(n.getUTCFullYear(), n.getUTCMonth(), n.getUTCDate()) / 1000;
  return { dayOffset: Math.round((midnightTarget - midnightNow) / 86400), hour: t.getUTCHours(), minute: t.getUTCMinutes() };
}

/** 「3 分钟前」「昨天」…… 设备列表用 */
export function lastSeenLabel(unixSeconds: number, now: number = nowSec()): string {
  const minutes = Math.floor((now - unixSeconds) / 60);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days === 1) return "昨天";
  if (days < 7) return `${days} 天前`;
  return `${slashDate(unixSeconds)} ${timeHM(unixSeconds)}`;
}
