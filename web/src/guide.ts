/**
 * 引导屏（#10，原型 H1–H8，设计稿 12–14）该出哪一张。
 *
 * 能力检测本身在 push.ts（10a）；这里只把检测结果翻译成「给用户看什么」，
 * 并且只放纯函数，方便在测试里把各种环境挨个过一遍。
 */

import type { PushSupport } from "./push";

export type PushGuide =
  /** H1：微信 / WhatsApp 内置浏览器，引导「在浏览器中打开」。 */
  | { kind: "jump-out"; app: "wechat" | "whatsapp"; ios: boolean }
  /** H2：iOS Safari 标签页，引导加到主屏幕。 */
  | { kind: "home-screen" }
  /** 能直接订阅，或者这台设备怎么都收不到（交给 B5 的「加到日历」）：不需要引导屏。 */
  | { kind: "none" };

/** 用户点了「提醒我」之后，要不要先出一张引导屏。 */
export function pushGuide(support: PushSupport, ios: boolean): PushGuide {
  switch (support.kind) {
    case "in-app-browser":
      return { kind: "jump-out", app: support.app, ios };
    case "ios-needs-home":
      return { kind: "home-screen" };
    default:
      return { kind: "none" };
  }
}

/**
 * 把 claim 令牌挂到当前地址上（§5.2「跳出微信前」）。
 *
 * 微信的「在浏览器中打开」用的是当前页面地址，所以先 `replaceState` 换成带令牌的地址，
 * 浏览器那边一打开，App 启动时的 `redeemClaimFromURL` 就把身份认回来了。
 */
export function withClaim(href: string, token: string): string {
  const url = new URL(href);
  url.searchParams.set("claim", token);
  return url.pathname + url.search + url.hash;
}

/** 用户不跳了：把令牌从地址栏拿掉，免得顺手「发送给朋友」时把它分享出去。 */
export function withoutClaim(href: string): string {
  const url = new URL(href);
  url.searchParams.delete("claim");
  return url.pathname + url.search + url.hash;
}

const WELCOMED_KEY = "gd_pwa_welcomed";

/**
 * 主屏幕图标第一次打开（H3–H8）：只在 standalone 里出，走完或跳过一次就不再出。
 *
 * 这个标记只决定 UI，不是凭证；iOS 主屏幕 App 的 localStorage 和 Safari 是分开的，
 * 所以 Safari 里的标记不会让这里误以为「已经欢迎过」。
 */
export function shouldWelcome(standalone: boolean, storage: Pick<Storage, "getItem"> | null = safeStorage()): boolean {
  if (!standalone) return false;
  try {
    return storage?.getItem(WELCOMED_KEY) !== "1";
  } catch {
    return true;
  }
}

export function markWelcomed(storage: Pick<Storage, "setItem"> | null = safeStorage()): void {
  try {
    storage?.setItem(WELCOMED_KEY, "1");
  } catch {
    // 存不下就下次再欢迎一次，无伤大雅。
  }
}

function safeStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

/** 认领码只收数字，最多 6 位（粘贴进来的「482 791」也认）。 */
export const CLAIM_CODE_LENGTH = 6;

export function normaliseCode(raw: string): string {
  return raw.replace(/\D/g, "").slice(0, CLAIM_CODE_LENGTH);
}
