/**
 * 「加到日历」（方案 §6、docs/server-design.md §5.4）。
 *
 * 服务端提供两个链接：
 *   - /g/{code}/game.ics：系统日历（iOS Safari、安卓 Chrome 打开后直接导入）；
 *     UID 固定、SEQUENCE 是牌局版本号，局头改时间后再点一次，日历会更新原有事件。
 *   - /g/{code}/google：跳到 Google Calendar 的新建事件页。
 *
 * 微信内置浏览器下载不了 .ics，也打不开 Google 的登录页，所以在那里不显示这两个按钮，
 * 改为引导用户跳出微信（用浏览器打开）再加。
 */

export interface CalendarLinks {
  ics: string;
  google: string;
}

export type CalendarOption = ({ kind: "links" } & CalendarLinks) | { kind: "leave-wechat" };

/** 微信内置浏览器（含小程序 web-view）。 */
export function isWeChat(userAgent: string): boolean {
  return /MicroMessenger/i.test(userAgent);
}

export function calendarLinks(code: string): CalendarLinks {
  const c = encodeURIComponent(code);
  return { ics: `/g/${c}/game.ics`, google: `/g/${c}/google` };
}

/** 按当前环境决定 B3、B4 屏的「加到日历」显示什么。 */
export function calendarOption(code: string, userAgent: string = navigator.userAgent): CalendarOption {
  if (isWeChat(userAgent)) return { kind: "leave-wechat" };
  return { kind: "links", ...calendarLinks(code) };
}
