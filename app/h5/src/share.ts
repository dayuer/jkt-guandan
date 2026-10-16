/**
 * H5 的分享出口（docs/h5-pwa-plan.md §5、设计稿 04 的 C1）。
 *
 * 微信里的 H5 链接渲染不出牌桌卡片，所以拆成四条路：海报（服务端渲染）、复制话术、
 * WhatsApp（靠 OG 标签出原生卡片）、系统分享。
 *
 * #253 之后**纯逻辑（话术怎么拼、URL 怎么带参数）搬去了 `app/shared/src/share.ts`**，
 * 好处是小游戏不用再反向 import H5。这个文件留两样东西：
 *
 *   1. 平台相关的：`navigator.share` 的系统分享、复制到剪贴板的兜底（H5 专有）；
 *   2. 一层薄封装：把共享层的 `shareText`/`systemShareText` 按 **H5 的时间写法**绑好再导出 ——
 *      时间怎么显示是各端自己的事（H5 用 `./format`，小游戏用 `src/lib/fmt.ts`，因为安卓微信
 *      不一定有 `Intl`），共享层只接收拼好的 `when` 字符串。这样 H5 这边的调用点一行都不用动。
 *
 * 面板本身在 components/ShareSheet.tsx。
 */

import { dayShort, timeHM } from "./format";
import {
  posterURL as sharedPosterURL,
  shareText as sharedShareText,
  shareURL as sharedShareURL,
  systemShareText as sharedSystemShareText,
  type ShareLike,
} from "../../shared/src/share";

export { whatsappURL, type ShareLike } from "../../shared/src/share";

/** H5 这边默认用当前站点（小游戏传自己的 `API_BASE`）—— 共享层不读 window。 */
function pageOrigin(): string {
  return window.location.origin;
}

/** 牌局链接。 */
export function shareURL(code: string, overrideOrigin: string = pageOrigin()): string {
  return sharedShareURL(code, overrideOrigin);
}

/** 海报地址。 */
export function posterURL(code: string, version: number, overrideOrigin: string = pageOrigin()): string {
  return sharedPosterURL(code, version, overrideOrigin);
}

/** H5 这边话术还要用开始时间（共享层只要拼好的文本，不认时间字段）。 */
export interface H5ShareInput extends ShareLike {
  game: { short_code: string; venue: string; status: string; starts_at: number };
}

/** 把这一局的开始时间按 H5 的写法拼成「今天 19:30」。 */
function whenText(detail: H5ShareInput, now: number): string {
  return `${dayShort(detail.game.starts_at, now)} ${timeHM(detail.game.starts_at)}`;
}

/** 一句话话术（H5 的时间写法）。 */
export function shareText(
  detail: H5ShareInput,
  now: number = Math.floor(Date.now() / 1000),
  overrideOrigin: string = pageOrigin(),
): string {
  return sharedShareText(detail, whenText(detail, now), overrideOrigin);
}

/** 系统分享用的正文：**不带链接**（链接交给 `url` 字段，不然对方那边会看到两遍）。 */
export function systemShareText(
  detail: H5ShareInput,
  now: number = Math.floor(Date.now() / 1000),
): string {
  return sharedSystemShareText(detail, whenText(detail, now));
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
