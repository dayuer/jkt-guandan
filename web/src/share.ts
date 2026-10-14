import type { GameDetail } from "./api";
import { dayShort, timeHM } from "./format";

/**
 * 分享的四个出口（docs/h5-pwa-plan.md §5、设计稿 04 的 C1）。
 *
 * 微信里的 H5 链接渲染不出牌桌卡片，所以拆成四条路：海报（服务端渲染，见下）、
 * 复制话术、WhatsApp（靠 OG 标签出原生卡片）、系统分享。
 *
 * 这里只有纯逻辑：文案怎么拼、URL 怎么带参数、`navigator.share` 的结果怎么归一。
 * 面板本身在 components/ShareSheet.tsx。
 */

/** 站内地址。测试里传 origin，别去读真 window。 */
function origin(override?: string): string {
  if (override) return override;
  if (typeof window !== "undefined" && window.location?.origin) return window.location.origin;
  return "";
}

/** 牌局链接：`https://<站点>/g/<短码>`。 */
export function shareURL(code: string, overrideOrigin?: string): string {
  return `${origin(overrideOrigin)}/g/${code}`;
}

/**
 * 海报地址（服务端渲染的 1080×1440 竖版图）。
 *
 * 带上版本号是为了满足「海报内容和座位状态实时一致」：座位一变牌局 version 就 +1，
 * URL 跟着变，中转缓存和微信的图片缓存不会把上一版端出来。
 */
export function posterURL(code: string, version: number, overrideOrigin?: string): string {
  return `${origin(overrideOrigin)}/g/${code}/poster.png?v=${version}`;
}

/**
 * 一句话话术。缺人时是「差 N 人」，满了是「已经满了，可以排候补」——
 * 完成标志里「话术随缺几人变化」就是这一条。
 */
export function shareText(
  detail: GameDetail,
  now: number = Math.floor(Date.now() / 1000),
  overrideOrigin?: string,
): string {
  const { game } = detail;
  const when = `${dayShort(game.starts_at, now)} ${timeHM(game.starts_at)}`;
  const where = game.venue;
  const link = shareURL(game.short_code, overrideOrigin);

  switch (game.status) {
    case "cancelled":
      return `${when} ${where} 这一局取消了，下次再约 👉 ${link}`;
    case "done":
      return `${when} ${where} 这一局打完了 👉 ${link}`;
    case "full":
      return `${when} ${where} 已经满了，想打可以排候补，点这里 👉 ${link}`;
    default:
      return `${when} ${where} ${missingWords(detail.missing)}，来的点 👉 ${link}`;
  }
}

function missingWords(missing: number): string {
  if (missing <= 0) return "就等开打了";
  return `差 ${missing} 人`;
}

/** WhatsApp 的分享入口。`wa.me/?text=` 不带号码时让用户自己选群或人。 */
export function whatsappURL(text: string): string {
  return `https://wa.me/?text=${encodeURIComponent(text)}`;
}

export type SystemShareResult = "shared" | "cancelled" | "unsupported" | "failed";

/** 这台设备支不支持系统分享（PWA、Safari、安卓 Chrome 支持；微信内置浏览器不支持）。 */
export function canSystemShare(): boolean {
  return typeof navigator !== "undefined" && typeof navigator.share === "function";
}

/**
 * 走系统分享。用户取消不算失败——`navigator.share` 用 AbortError 表示取消，
 * 这里归一成 "cancelled"，页面据此不要弹「分享失败」。
 */
export async function systemShare(payload: {
  title: string;
  text: string;
  url: string;
}): Promise<SystemShareResult> {
  if (!canSystemShare()) return "unsupported";
  try {
    await navigator.share(payload);
    return "shared";
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") return "cancelled";
    return "failed";
  }
}

/**
 * 复制到剪贴板，返回是否成功。
 *
 * 微信内置浏览器里的 `navigator.clipboard` 有的版本是 undefined，有的写失败，
 * 所以退回 `document.execCommand("copy")`——这是老 API，但在这里是必要的兜底，
 * 失败时页面要提示用户手动长按选中。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 落到下面的兜底。
  }
  try {
    const el = document.createElement("textarea");
    el.value = text;
    el.setAttribute("readonly", "");
    el.style.position = "fixed";
    el.style.top = "-1000px";
    document.body.appendChild(el);
    el.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(el);
    return ok;
  } catch {
    return false;
  }
}
