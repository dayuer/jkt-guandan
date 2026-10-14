import type { GameDetail } from "./api";

/**
 * 页面标题（#60）。
 *
 * 服务端给 `/g/{code}` 注入了这一局的 `<title>`（internal/share/og.go 的 OGFor），
 * 但 SPA 内部跳转不会再走服务端，所以路由切换时由前端重设：
 * 详情页用和服务端 OG 一样的格式，其他页面用应用名。
 */

export const APP_NAME = "随时掼掼蛋";

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const relative: Record<number, string> = { 0: "今天 ", 1: "明天 ", 2: "后天 " };

const wib = new Intl.DateTimeFormat("en-US", {
  year: "numeric",
  month: "numeric",
  day: "numeric",
  weekday: "short",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "Asia/Jakarta",
});

/** WIB 下的年月日、星期和时分。 */
function wibParts(unixSeconds: number) {
  const parts = wib.formatToParts(new Date(unixSeconds * 1000));
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? "";
  return {
    y: Number(get("year")),
    m: Number(get("month")),
    d: Number(get("day")),
    wd: Math.max(["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(get("weekday")), 0),
    hm: `${get("hour").padStart(2, "0")}:${get("minute").padStart(2, "0")}`,
  };
}

/**
 * 「掼蛋局 · 今天 10/13 周二 19:30 · Pacific Place」；取消的局前面加「【已取消】」。
 * 和服务端 dayLabel 一致：今天 / 明天 / 后天带前缀，再往后只有日期；月、日不补零。
 */
export function gameTitle(detail: GameDetail, now: number = Math.floor(Date.now() / 1000)): string {
  const { game } = detail;
  const t = wibParts(game.starts_at);
  const n = wibParts(now);
  const days = Math.round((Date.UTC(t.y, t.m - 1, t.d) - Date.UTC(n.y, n.m - 1, n.d)) / 86400000);
  const day = `${relative[days] ?? ""}${t.m}/${t.d} ${weekdays[t.wd]}`;
  const title = `掼蛋局 · ${day} ${t.hm} · ${game.venue}`;
  return game.status === "cancelled" ? `【已取消】${title}` : title;
}
