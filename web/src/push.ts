/**
 * 网页推送的客户端（docs/h5-pwa-plan.md §6、docs/server-design.md §5.3）。
 *
 * 能收到提醒的条件，各个平台不一样，能力检测必须分清楚，不然后面会一路踩坑：
 *
 *   - **Android Chrome / 桌面浏览器**：Service Worker + PushManager + 用户点「允许」就够了。
 *   - **iOS Safari 16.4+**：只有**加到主屏幕**、从主屏幕图标打开（standalone）才有
 *     `PushManager`；在 Safari 标签页里 `window.PushManager` 是 undefined，
 *     所以那边不能显示「提醒我」，只能引导加主屏幕（原型 H2）。
 *   - **微信 / WhatsApp 内置浏览器**：没有 Service Worker（或者有也不给推送），
 *     点「提醒我」只能引导跳出到浏览器（原型 H1）。
 *
 * 判断结果就是 B4 / B5 两种样子：支持 → 「提醒我」，不支持 → 「加到日历」+「用 WhatsApp 提醒我」。
 */

import { pushSubscribe, pushUnsubscribe, pushKey } from "./api";

export type PushSupport =
  /** 现在就能订阅。 */
  | { kind: "ready" }
  /** iOS Safari 标签页：要先加到主屏幕。 */
  | { kind: "ios-needs-home" }
  /** 微信 / WhatsApp 内置浏览器：得换浏览器打开。 */
  | { kind: "in-app-browser"; app: "wechat" | "whatsapp" }
  /** 这台浏览器就是不支持（老安卓、鸿蒙自带浏览器、无痕模式……）。 */
  | { kind: "unsupported" };

/** 是不是在应用内置浏览器里（微信、WhatsApp）。 */
export function inAppBrowser(userAgent: string): "wechat" | "whatsapp" | "" {
  if (/micromessenger/i.test(userAgent)) return "wechat";
  if (/whatsapp/i.test(userAgent)) return "whatsapp";
  return "";
}

/** iOS 设备（iPad 从 iPadOS 13 起 UA 也写 Macintosh，所以要连触屏一起看）。 */
export function isIOS(nav: Navigator = navigator): boolean {
  const ua = nav.userAgent ?? "";
  if (/iphone|ipod/i.test(ua)) return true;
  if (/ipad/i.test(ua)) return true;
  // iPadOS 13+ 的桌面 UA：Mac + 有触屏。
  return /macintosh/i.test(ua) && (nav.maxTouchPoints ?? 0) > 1;
}

/** 已经加到主屏幕、正从图标打开（standalone）。 */
export function isStandalone(win: Window = window): boolean {
  if (typeof win.matchMedia === "function" && win.matchMedia("(display-mode: standalone)").matches) {
    return true;
  }
  // iOS Safari 里的私有字段。
  return (win.navigator as unknown as { standalone?: boolean }).standalone === true;
}

/**
 * 这台设备现在能不能订阅推送。参数都可以传，方便测试。
 */
export function pushSupport(
  userAgent: string = navigator.userAgent,
  nav: Navigator = navigator,
  win: Window = window,
): PushSupport {
  const app = inAppBrowser(userAgent);
  if (app) return { kind: "in-app-browser", app };

  // iOS 上「有没有 PushManager」这一条就是「有没有加到主屏幕」：
  // Safari 标签页里没有，主屏幕打开的独立窗口里才有。
  if (isIOS(nav) && !isStandalone(win)) return { kind: "ios-needs-home" };

  if (!("serviceWorker" in nav) || !("PushManager" in win) || !("Notification" in win)) {
    return { kind: "unsupported" };
  }
  return { kind: "ready" };
}

/** 现在的通知权限。 */
export function notificationPermission(): NotificationPermission | "unsupported" {
  if (!("Notification" in window)) return "unsupported";
  return Notification.permission;
}

export type SubscribeResult =
  | { kind: "ok" }
  /** 用户没点允许（或者之前拒绝过）。 */
  | { kind: "denied" }
  /** 这台设备支持不了，或者拿不到公钥/订阅失败。 */
  | { kind: "failed"; reason: string };

/**
 * 走一遍订阅：要权限 → 拿 VAPID 公钥 → 订阅 → 告诉服务端。
 *
 * 服务端会把订阅挂到当前身份上（没有身份 401），并记一条 push_enabled 埋点，
 * 「提醒开启率」就是用它算的（§8）。
 */
export async function subscribeToPush(): Promise<SubscribeResult> {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
    return { kind: "failed", reason: "这台设备不支持网页推送" };
  }

  let permission: NotificationPermission;
  try {
    permission = await Notification.requestPermission();
  } catch {
    return { kind: "failed", reason: "系统权限框打不开" };
  }
  if (permission !== "granted") return { kind: "denied" };

  try {
    const registration = await serviceWorker();
    const key = await pushKey();
    const subscription = await registration.pushManager.subscribe({
      // 浏览器要求：必须承诺每条推送都显示给用户，不能偷偷静默推送。
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(key),
    });
    await pushSubscribe(subscription.toJSON());
    return { kind: "ok" };
  } catch (err) {
    return { kind: "failed", reason: err instanceof Error ? err.message : String(err) };
  }
}

/** 关掉提醒：先告诉服务端（免得它还往这个 endpoint 发），再退订。 */
export async function unsubscribeFromPush(): Promise<boolean> {
  try {
    const registration = await serviceWorker();
    const subscription = await registration.pushManager.getSubscription();
    if (!subscription) return true;
    await pushUnsubscribe(subscription.endpoint).catch(() => {
      // 服务端已经删过了也当成功。
    });
    return await subscription.unsubscribe();
  } catch {
    return false;
  }
}

/** 当前有没有订阅。 */
export async function hasPushSubscription(): Promise<boolean> {
  try {
    const registration = await serviceWorker();
    return (await registration.pushManager.getSubscription()) !== null;
  } catch {
    return false;
  }
}

/** 等 Service Worker 就绪最多这么久。 */
export const SW_READY_TIMEOUT_MS = 5000;

/**
 * 等 Service Worker 就绪（第一次要等它装上）。
 *
 * `ready` 在注册失败时永远不会 resolve（Firefox 隐私模式、存储被禁时 `serviceWorker`
 * 对象在，但 `register` 会抛错），不加超时的话「提醒我」会一直转圈（#63 审查建议 1）。
 */
export async function serviceWorker(timeoutMs: number = SW_READY_TIMEOUT_MS): Promise<ServiceWorkerRegistration> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => reject(new Error("Service Worker 没有就绪")), timeoutMs);
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

/**
 * 注册 Service Worker。页面启动时调一次就够了；失败只记日志——
 * 没有推送这个站照样能用，不该因为注册不上白屏。
 */
export async function registerServiceWorker(): Promise<void> {
  if (!("serviceWorker" in navigator)) return;
  try {
    await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  } catch (err) {
    console.warn("Service Worker 注册失败，推送不可用", err);
  }
}

/**
 * VAPID 公钥是 base64url，`applicationServerKey` 要 Uint8Array。
 * 少一个 `=` 都要自己补回来，浏览器不会帮你。
 */
export function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
  const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4);
  const normalised = padded.replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(normalised);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
