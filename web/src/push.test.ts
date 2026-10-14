import { afterEach, describe, expect, it, vi } from "vitest";
import {
  inAppBrowser,
  isIOS,
  isStandalone,
  pushSupport,
  subscribeToPush,
  unsubscribeFromPush,
  urlBase64ToUint8Array,
} from "./push";

/**
 * 能力检测是这个 issue 的完成标志里第一条：不支持推送的机型不能显示「提醒我」。
 * 各个平台的差别（iOS 必须先加到主屏幕、微信里没有 SW）都要在这里说清楚。
 */

const UA = {
  wechat: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 MicroMessenger/8.0.49",
  whatsapp: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 WhatsApp/2.24",
  iphoneSafari: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 Version/17.0 Mobile Safari/604.1",
  androidChrome: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36",
  desktopMac: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
  oldAndroid: "Mozilla/5.0 (Linux; Android 8.0; HUAWEI) AppleWebKit/537.36 Chrome/60.0.0.0 Mobile Safari/537.36",
};

/** 造一个带指定能力的 navigator / window。 */
function env(opts: { push?: boolean; standalone?: boolean; touch?: number } = {}) {
  const nav = {
    userAgent: UA.desktopMac,
    maxTouchPoints: opts.touch ?? 0,
    serviceWorker: {},
  } as unknown as Navigator;
  const win = {
    navigator: nav,
    matchMedia: () => ({ matches: opts.standalone ?? false }),
    PushManager: opts.push === false ? undefined : function PushManager() {},
    Notification: function Notification() {},
  } as unknown as Window;
  if (opts.push === false) {
    delete (win as unknown as Record<string, unknown>).PushManager;
  }
  return { nav, win };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("能力检测（完成标志第 1 条）", () => {
  it("安卓 Chrome / 桌面：现在就能订阅", () => {
    expect(pushSupport(UA.androidChrome, env().nav, env().win).kind).toBe("ready");
    expect(pushSupport(UA.desktopMac, env().nav, env().win).kind).toBe("ready");
  });

  it("微信里：先引导跳出（不能显示「提醒我」）", () => {
    const s = pushSupport(UA.wechat, env().nav, env().win);
    expect(s).toEqual({ kind: "in-app-browser", app: "wechat" });
  });

  it("WhatsApp 内置浏览器：一样要跳出去", () => {
    const s = pushSupport(UA.whatsapp, env().nav, env().win);
    expect(s).toEqual({ kind: "in-app-browser", app: "whatsapp" });
  });

  it("iOS Safari 标签页里要先加到主屏幕", () => {
    const { nav, win } = env();
    expect(pushSupport(UA.iphoneSafari, { ...nav, userAgent: UA.iphoneSafari } as Navigator, win).kind).toBe(
      "ios-needs-home",
    );
  });

  it("iOS 主屏幕上打开（standalone）就能订阅", () => {
    const { nav, win } = env({ standalone: true });
    expect(
      pushSupport(UA.iphoneSafari, { ...nav, userAgent: UA.iphoneSafari } as Navigator, win).kind,
    ).toBe("ready");
  });

  it("浏览器本身没有 PushManager：不支持，不装作支持", () => {
    const { nav, win } = env({ push: false });
    expect(pushSupport(UA.oldAndroid, { ...nav, userAgent: UA.oldAndroid } as Navigator, win).kind).toBe(
      "unsupported",
    );
  });

  it("iPadOS 13 起 UA 写的是 Macintosh，靠触屏点数认出来", () => {
    const { nav, win } = env({ touch: 5 });
    const ipad = { ...nav, userAgent: UA.desktopMac, maxTouchPoints: 5 } as Navigator;
    expect(isIOS(ipad)).toBe(true);
    // 桌面 Mac（没有触屏）不该被误判成 iPad。
    expect(isIOS({ ...nav, userAgent: UA.desktopMac, maxTouchPoints: 0 } as Navigator)).toBe(false);
    expect(pushSupport(UA.desktopMac, ipad, win).kind).toBe("ios-needs-home");
  });
});

describe("平台判断的小工具", () => {
  it("inAppBrowser 区分微信和 WhatsApp，普通浏览器返回空", () => {
    expect(inAppBrowser(UA.wechat)).toBe("wechat");
    expect(inAppBrowser(UA.whatsapp)).toBe("whatsapp");
    expect(
      inAppBrowser("Mozilla/5.0 (iPhone) Mobile/15E148 Instagram 312.0.0 (iPhone14,5; iOS 17_0; FBAN/FBIOS)"),
    ).toBe("instagram");
    expect(inAppBrowser("Mozilla/5.0 (iPhone) Mobile/15E148 [FBAN/FBIOS;FBAV/450.0;FBBV/1]")).toBe("facebook");
    expect(inAppBrowser("Mozilla/5.0 (Linux; Android 14) Chrome/120 Mobile Safari/537.36 [FB_IAB/FB4A;FBAV/450.0;]")).toBe(
      "facebook",
    );
    expect(inAppBrowser("Mozilla/5.0 (iPhone) Mobile/15E148 Safari Line/13.17.0")).toBe("line");
    expect(inAppBrowser("Mozilla/5.0 (X11; Linux) Online/1.0 Chrome/120")).toBe("");
    expect(inAppBrowser(UA.iphoneSafari)).toBe("");
  });

  it("isStandalone 同时认 display-mode 和 iOS 的 navigator.standalone", () => {
    expect(isStandalone({ matchMedia: () => ({ matches: true }) } as unknown as Window)).toBe(true);
    expect(
      isStandalone({
        matchMedia: () => ({ matches: false }),
        navigator: { standalone: true },
      } as unknown as Window),
    ).toBe(true);
  });
});

describe("订阅", () => {
  /** 一套假的浏览器推送 API。 */
  function fakeBrowser(opts: { permission?: NotificationPermission } = {}) {
    const subscription = {
      endpoint: "https://fcm.googleapis.com/fcm/send/abc",
      toJSON: () => ({
        endpoint: "https://fcm.googleapis.com/fcm/send/abc",
        keys: { p256dh: "p", auth: "a" },
      }),
      unsubscribe: vi.fn(async () => true),
    };
    const subscribe = vi.fn(async () => subscription);
    const registration = {
      pushManager: { subscribe, getSubscription: vi.fn(async () => subscription) },
    };

    const requestPermission = vi.fn(async () => opts.permission ?? "granted");
    vi.stubGlobal("navigator", { serviceWorker: { ready: Promise.resolve(registration) } });
    vi.stubGlobal("window", { PushManager: function () {}, Notification: { requestPermission } });
    vi.stubGlobal("Notification", { requestPermission, permission: opts.permission ?? "granted" });

    return { subscribe, requestPermission, subscription };
  }

  function stubFetch(key = "BKEY", status = 204) {
    const calls: { method: string; url: string; body: unknown }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ method: init?.method ?? "GET", url, body: init?.body ? JSON.parse(String(init.body)) : null });
        if (url === "/api/push/key") {
          return {
            ok: true,
            status: 200,
            headers: { get: () => null },
            text: async () => JSON.stringify({ key }),
            json: async () => ({ key }),
          };
        }
        return {
          ok: status >= 200 && status < 300,
          status,
          headers: { get: () => null },
          text: async () => "",
          json: async () => null,
        };
      }),
    );
    return calls;
  }

  it("要权限 → 拿公钥 → 订阅 → 告诉服务端", async () => {
    const { subscribe, requestPermission } = fakeBrowser();
    const calls = stubFetch();

    expect(await subscribeToPush()).toEqual({ kind: "ok" });
    expect(requestPermission).toHaveBeenCalled();
    expect(subscribe).toHaveBeenCalledWith(
      expect.objectContaining({ userVisibleOnly: true, applicationServerKey: expect.any(Uint8Array) }),
    );
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual(["GET /api/push/key", "POST /api/push/subscribe"]);
    expect(calls[1].body).toEqual({
      endpoint: "https://fcm.googleapis.com/fcm/send/abc",
      keys: { p256dh: "p", auth: "a" },
    });
  });

  it("用户没点允许：返回 denied，不去订阅，也不打服务端", async () => {
    const { subscribe } = fakeBrowser({ permission: "denied" });
    const calls = stubFetch();

    expect(await subscribeToPush()).toEqual({ kind: "denied" });
    expect(subscribe).not.toHaveBeenCalled();
    expect(calls).toHaveLength(0);
  });

  it("服务端不收（比如没有身份 401）：如实返回 failed", async () => {
    fakeBrowser();
    stubFetch("BKEY", 401);

    const result = await subscribeToPush();
    expect(result.kind).toBe("failed");
  });

  it("退订：先告诉服务端，再 unsubscribe", async () => {
    const { subscription } = fakeBrowser();
    const calls = stubFetch();

    expect(await unsubscribeFromPush()).toBe(true);
    expect(calls[0]).toEqual({
      method: "DELETE",
      url: "/api/push/subscribe",
      body: { endpoint: "https://fcm.googleapis.com/fcm/send/abc" },
    });
    expect(subscription.unsubscribe).toHaveBeenCalled();
  });

  it("服务端删失败也照样退订——本地退掉才是用户要的", async () => {
    const { subscription } = fakeBrowser();
    stubFetch("BKEY", 500);

    expect(await unsubscribeFromPush()).toBe(true);
    expect(subscription.unsubscribe).toHaveBeenCalled();
  });
});

describe("VAPID 公钥解码", () => {
  it("base64url 的 - 和 _ 要换回来，缺的 = 要补上", () => {
    // "hello" 的 base64url 是 aGVsbG8（无填充）。
    expect([...urlBase64ToUint8Array("aGVsbG8")]).toEqual([...new TextEncoder().encode("hello")]);
    // 带上 - 和 _ 的那几个字节。
    expect([...urlBase64ToUint8Array("-_8")]).toEqual([251, 255]);
  });
});
