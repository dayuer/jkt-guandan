import { describe, expect, it } from "vitest";
import { jumpOutLabels, markWelcomed, normaliseCode, pushGuide, shouldWelcome, withClaim, withoutClaim } from "./guide";
import { isIOS, pushSupport } from "./push";

/** 哪种环境出哪张引导屏（#10 H1–H8）。能力检测本身由 push.test.ts 覆盖，这里从 UA 一路走到屏。 */

const UA = {
  wechatIOS: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 MicroMessenger/8.0.49",
  wechatAndroid: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120.0 Mobile MicroMessenger/8.0.49",
  whatsapp: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120.0 Mobile WhatsApp/2.24",
  iphoneSafari: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 Version/17.0 Mobile Safari/604.1",
  androidChrome: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36",
};

function screenFor(userAgent: string, opts: { standalone?: boolean; push?: boolean } = {}) {
  const nav = { userAgent, maxTouchPoints: 5, serviceWorker: {} } as unknown as Navigator;
  const win = {
    navigator: nav,
    matchMedia: () => ({ matches: opts.standalone ?? false }),
    Notification: function Notification() {},
    ...(opts.push === false ? {} : { PushManager: function PushManager() {} }),
  } as unknown as Window;
  return pushGuide(pushSupport(userAgent, nav, win), isIOS(nav));
}

describe("点了「提醒我」之后出哪张引导屏", () => {
  it("iPhone 微信 → H1，菜单项写「在 Safari 中打开」", () => {
    expect(screenFor(UA.wechatIOS)).toEqual({ kind: "jump-out", app: "wechat", ios: true });
  });

  it("安卓微信 → H1", () => {
    expect(screenFor(UA.wechatAndroid)).toEqual({ kind: "jump-out", app: "wechat", ios: false });
  });

  it("WhatsApp 内置浏览器 → H1 的 WhatsApp 版", () => {
    expect(screenFor(UA.whatsapp)).toMatchObject({ kind: "jump-out", app: "whatsapp" });
  });

  it("Instagram / Facebook / LINE 内置浏览器 → H1（印尼常见入口）", () => {
    expect(screenFor("Mozilla/5.0 (iPhone) Mobile/15E148 Instagram 312.0.0")).toMatchObject({ kind: "jump-out", app: "instagram" });
    expect(screenFor("Mozilla/5.0 (iPhone) Mobile/15E148 [FBAN/FBIOS;FBAV/450.0]")).toMatchObject({ kind: "jump-out", app: "facebook" });
    expect(screenFor("Mozilla/5.0 (iPhone) Mobile/15E148 Safari Line/13.17.0")).toMatchObject({ kind: "jump-out", app: "line" });
  });

  it("H1 的菜单文案按应用区分", () => {
    expect(jumpOutLabels("wechat", true)).toEqual({ menu: "···", item: "在 Safari 中打开" });
    expect(jumpOutLabels("wechat", false).item).toBe("在浏览器中打开");
    expect(jumpOutLabels("whatsapp", false).menu).toBe("⋮");
    expect(jumpOutLabels("instagram", true).item).toBe("在外部浏览器中打开");
    expect(jumpOutLabels("facebook", false).item).toBe("在外部浏览器中打开");
    expect(jumpOutLabels("line", false)).toEqual({ menu: "⋮", item: "用默认浏览器打开" });
  });

  it("iPhone Safari 标签页 → H2 加到主屏幕", () => {
    expect(screenFor(UA.iphoneSafari, { push: false })).toEqual({ kind: "home-screen" });
  });

  it("iPhone 主屏幕打开 → 不用引导，直接订阅", () => {
    expect(screenFor(UA.iphoneSafari, { standalone: true })).toEqual({ kind: "none" });
  });

  it("安卓 Chrome → 不用引导", () => {
    expect(screenFor(UA.androidChrome)).toEqual({ kind: "none" });
  });

  it("不支持推送的浏览器 → 不出引导屏（交给 B5 的加到日历）", () => {
    expect(screenFor(UA.androidChrome, { push: false })).toEqual({ kind: "none" });
  });
});

describe("主屏幕首开（H3–H8）只出一次", () => {
  function memory(): Storage {
    const data = new Map<string, string>();
    return {
      getItem: (k: string) => data.get(k) ?? null,
      setItem: (k: string, v: string) => void data.set(k, v),
    } as unknown as Storage;
  }

  it("浏览器标签页里永远不出", () => {
    expect(shouldWelcome(false, memory())).toBe(false);
  });

  it("standalone 第一次出，记下之后不再出", () => {
    const storage = memory();
    expect(shouldWelcome(true, storage)).toBe(true);
    markWelcomed(storage);
    expect(shouldWelcome(true, storage)).toBe(false);
  });

  it("存储不可用（无痕）时照样出，不抛错", () => {
    const broken = {
      getItem: () => {
        throw new Error("SecurityError");
      },
    };
    expect(shouldWelcome(true, broken)).toBe(true);
    expect(shouldWelcome(true, null)).toBe(true);
  });
});

describe("跳出微信时地址栏上的 claim 令牌", () => {
  it("挂上令牌，保留原有路径、参数和锚点", () => {
    expect(withClaim("https://guandan.synon.ai/g/8K2QXM?from=wa#seats", "tok")).toBe(
      "/g/8K2QXM?from=wa&claim=tok#seats",
    );
  });

  it("不跳了就把令牌拿掉，其他参数不动", () => {
    expect(withoutClaim("https://guandan.synon.ai/g/8K2QXM?from=wa&claim=tok")).toBe("/g/8K2QXM?from=wa");
    expect(withoutClaim("https://guandan.synon.ai/g/8K2QXM?claim=tok")).toBe("/g/8K2QXM");
  });
});

describe("认领码输入", () => {
  it("只留数字，最多 6 位", () => {
    expect(normaliseCode("482 791")).toBe("482791");
    expect(normaliseCode("4827911")).toBe("482791");
    expect(normaliseCode("48a2")).toBe("482");
  });
});
