import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { detectEnv, flush, reportError, resetTelemetry, track } from "./telemetry";

const UA = {
  wechat: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 MicroMessenger/8.0.49",
  whatsapp: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 WhatsApp/2.24",
  instagram: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) AppleWebKit/605.1.15 Instagram 300.0",
  chrome: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36",
};

const win = (standalone: boolean) =>
  ({ matchMedia: () => ({ matches: standalone }), navigator: {} }) as unknown as Window;

describe("detectEnv", () => {
  it("微信、WhatsApp 原样；其他内置浏览器归 browser；主屏幕打开是 pwa", () => {
    expect(detectEnv(UA.wechat, win(false))).toBe("wechat");
    expect(detectEnv(UA.whatsapp, win(false))).toBe("whatsapp");
    expect(detectEnv(UA.instagram, win(false))).toBe("browser");
    expect(detectEnv(UA.chrome, win(false))).toBe("browser");
    expect(detectEnv(UA.chrome, win(true))).toBe("pwa");
  });
});

describe("track / flush", () => {
  let calls: { url: string; init: RequestInit }[];

  beforeEach(() => {
    resetTelemetry();
    calls = [];
    vi.useFakeTimers();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        calls.push({ url, init });
        return new Response(JSON.stringify({ accepted: 1, dropped: 0 }), { status: 200 });
      }),
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    resetTelemetry();
  });

  const bodies = () => calls.map((c) => JSON.parse(String(c.init.body)));

  it("攒 5 秒后一批发出，带 env 和 JSON 头", () => {
    track("detail_view", { game: "8K2QXM" });
    track("join_click", { game: "8K2QXM" });
    expect(calls).toHaveLength(0);

    vi.advanceTimersByTime(5000);
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("/api/events");
    expect((calls[0].init.headers as Record<string, string>)["content-type"]).toBe("application/json");
    expect(bodies()[0]).toEqual({
      env: "browser",
      events: [
        { name: "detail_view", props: { game: "8K2QXM" } },
        { name: "join_click", props: { game: "8K2QXM" } },
      ],
    });
  });

  it("满 20 条立刻发，一批不超过 20", () => {
    for (let i = 0; i < 45; i++) track("share_open");
    flush();
    expect(bodies().map((b) => b.events.length)).toEqual([20, 20, 5]);
  });

  it("收起页面时用 keepalive", () => {
    track("push_prompt");
    flush(true);
    expect(calls[0].init.keepalive).toBe(true);
  });

  it("fetch 不认 keepalive 而同步抛错时，退回 sendBeacon", () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => {
        throw new TypeError("keepalive unsupported");
      }),
    );
    const sendBeacon = vi.fn(() => true);
    vi.stubGlobal("navigator", { userAgent: UA.chrome, sendBeacon });
    track("jumpout");
    flush(true);
    expect(sendBeacon).toHaveBeenCalledTimes(1);
    expect((sendBeacon.mock.calls[0] as unknown[])[0]).toBe("/api/events");
  });

  it("发送失败不抛、不重试", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("offline"))));
    track("join_ok");
    expect(() => flush()).not.toThrow();
    await vi.runAllTimersAsync();
  });

  it("空队列不发", () => {
    flush(true);
    expect(calls).toHaveLength(0);
  });
});

describe("reportError", () => {
  let calls: { url: string; body: Record<string, string> }[];

  beforeEach(() => {
    resetTelemetry();
    calls = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        calls.push({ url, body: JSON.parse(String(init.body)) });
        return new Response(null, { status: 204 });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    resetTelemetry();
  });

  it("截断 message / stack，url 只留路径（不带 ?claim=）", () => {
    window.history.replaceState(null, "", "/g/ABC123?claim=secret#x");
    reportError("x".repeat(500), "s".repeat(5000));
    expect(calls[0].url).toBe("/api/client-errors");
    expect(calls[0].body.message).toHaveLength(300);
    expect(calls[0].body.stack).toHaveLength(2000);
    expect(calls[0].body.url).toBe("/g/ABC123");
    expect(JSON.stringify(calls[0].body)).not.toContain("secret");
    window.history.replaceState(null, "", "/");
  });

  it("同一条只报一次，一次打开最多 3 条", () => {
    reportError("a");
    reportError("a");
    reportError("b");
    reportError("c");
    reportError("d");
    expect(calls.map((c) => c.body.message)).toEqual(["a", "b", "c"]);
  });
});
