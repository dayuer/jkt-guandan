import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * 引导屏 H1–H8（#10，设计稿 12–14）。
 *
 * push.ts 的能力检测和订阅换成可控的假实现：这里要测的是「什么环境出哪一屏、每一步都能跳过」，
 * 不是浏览器的推送 API。
 */

const push = vi.hoisted(() => ({
  support: { kind: "ready" } as { kind: string; app?: string },
  ios: false,
  standalone: false,
  permission: "default" as string,
  subscribe: vi.fn(async () => ({ kind: "ok" }) as { kind: string; reason?: string }),
}));

vi.mock("../push", () => ({
  pushSupport: () => push.support,
  isIOS: () => push.ios,
  isStandalone: () => push.standalone,
  notificationPermission: () => push.permission,
  subscribeToPush: () => push.subscribe(),
  registerServiceWorker: async () => {},
}));

import { App } from "../App";
import { FirstLaunch } from "./FirstLaunch";
import { HomeScreenGuide } from "./HomeScreenGuide";
import { JumpOutGuide } from "./JumpOutGuide";
import { PushGuide, needsPushGuide } from "./PushGuide";

function respond(status: number, body: unknown): Response {
  const text = JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

interface Call {
  method: string;
  url: string;
  body: any;
}

/** 按「方法 路径」回包；没配的一律 401（相当于没有身份）。 */
function stubFetch(routes: Record<string, Response | (() => Response)>): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const path = url.split("?")[0];
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : null });
      const hit = routes[`${method} ${path}`];
      if (hit) return typeof hit === "function" ? hit() : hit;
      return respond(401, { error: { code: "unauthenticated", message: "" } });
    }),
  );
  return calls;
}

function mount(node: preact.VNode) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(node, container);
  return container;
}

function button(container: HTMLElement, text: string): HTMLButtonElement {
  const found = [...container.querySelectorAll("button")].find((b) => b.textContent?.includes(text));
  if (!found) throw new Error(`没有「${text}」按钮：${container.textContent}`);
  return found as HTMLButtonElement;
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 20));

beforeEach(() => {
  push.support = { kind: "ready" };
  push.ios = false;
  push.standalone = false;
  push.permission = "default";
  push.subscribe.mockReset();
  push.subscribe.mockResolvedValue({ kind: "ok" });
  localStorage.clear();
  window.history.replaceState(null, "", "/g/8K2QXM");
});

afterEach(() => {
  render(null, document.body);
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("H1 · 跳出微信", () => {
  const ticket = respond(200, { claim_token: "tok123", code: "482791", expires_at: 0 });

  it("有身份：签发 claim 令牌并挂到地址栏，「在浏览器中打开」就能带走身份", async () => {
    const calls = stubFetch({ "POST /api/claim": ticket });
    const c = mount(<JumpOutGuide app="wechat" ios={false} signedIn onCalendar={() => {}} onClose={() => {}} />);

    await vi.waitFor(() => expect(window.location.search).toBe("?claim=tok123"));
    expect(calls.map((x) => `${x.method} ${x.url}`)).toEqual(["POST /api/claim"]);
    expect(window.location.pathname).toBe("/g/8K2QXM");
    expect(c.textContent).toContain("打开后自动认出你");
    expect(c.textContent).toContain("点右上角「···」");
    expect(c.textContent).toContain("在浏览器中打开");
  });

  it("iPhone 微信的菜单项叫「在 Safari 中打开」", () => {
    stubFetch({});
    const c = mount(<JumpOutGuide app="wechat" ios signedIn={false} onCalendar={() => {}} onClose={() => {}} />);
    expect(c.textContent).toContain("选「在 Safari 中打开」");
  });

  it("WhatsApp 版：菜单是右上角「⋮」", () => {
    stubFetch({});
    const c = mount(<JumpOutGuide app="whatsapp" ios={false} signedIn={false} onCalendar={() => {}} onClose={() => {}} />);
    expect(c.textContent).toContain("点右上角「⋮」");
  });

  it("没有身份：不签发令牌（没有可带走的）", async () => {
    const calls = stubFetch({});
    mount(<JumpOutGuide app="wechat" ios={false} signedIn={false} onCalendar={() => {}} onClose={() => {}} />);
    await tick();
    expect(calls).toEqual([]);
  });

  it("「算了，加日历就行」：先把令牌从地址栏拿掉，再去加日历", async () => {
    stubFetch({ "POST /api/claim": ticket });
    const onCalendar = vi.fn();
    const c = mount(<JumpOutGuide app="wechat" ios={false} signedIn onCalendar={onCalendar} onClose={() => {}} />);
    await vi.waitFor(() => expect(window.location.search).toContain("claim="));

    button(c, "算了，加日历就行").click();
    expect(window.location.search).toBe("");
    expect(onCalendar).toHaveBeenCalledOnce();
  });

  it("被父组件直接卸载（点遮罩、路由跳走）也把令牌从地址栏拿掉", async () => {
    stubFetch({ "POST /api/claim": ticket });
    const c = mount(<JumpOutGuide app="wechat" ios={false} signedIn onCalendar={() => {}} onClose={() => {}} />);
    await vi.waitFor(() => expect(window.location.search).toContain("claim="));

    render(null, c);
    await vi.waitFor(() => expect(window.location.search).toBe(""));
    expect(window.location.pathname).toBe("/g/8K2QXM");
  });

  it("「先不用」也能直接关掉", () => {
    stubFetch({});
    const onClose = vi.fn();
    const c = mount(<JumpOutGuide app="wechat" ios={false} signedIn={false} onCalendar={() => {}} onClose={onClose} />);
    button(c, "先不用").click();
    expect(onClose).toHaveBeenCalledOnce();
  });
});

describe("H2 · iOS 加到主屏幕", () => {
  it("有身份：显示 6 位认领码，给主屏幕那边输入用", async () => {
    stubFetch({ "POST /api/claim": respond(200, { claim_token: "t", code: "482791", expires_at: 0 }) });
    const c = mount(<HomeScreenGuide signedIn onCalendar={() => {}} onClose={() => {}} />);

    await vi.waitFor(() => expect(c.querySelector("[data-testid=claim-code]")).not.toBeNull());
    const digits = [...c.querySelectorAll(".claimcard .digits span")].map((s) => s.textContent);
    expect(digits).toEqual(["4", "8", "2", "7", "9", "1"]);
    expect(c.textContent).toContain("iPhone 要多一步，才能收到提醒");
    expect(c.textContent).toContain("添加到主屏幕");
  });

  it("签发失败（限流）：照样教怎么装，只是不显示认领码", async () => {
    stubFetch({ "POST /api/claim": respond(429, { error: { code: "rate_limited", message: "" } }) });
    const c = mount(<HomeScreenGuide signedIn onCalendar={() => {}} onClose={() => {}} />);
    await tick();
    expect(c.querySelector("[data-testid=claim-code]")).toBeNull();
    expect(c.textContent).toContain("点底部的分享按钮");
  });

  it("「不装了，加到日历」", () => {
    stubFetch({});
    const onCalendar = vi.fn();
    const c = mount(<HomeScreenGuide signedIn={false} onCalendar={onCalendar} onClose={() => {}} />);
    button(c, "不装了，加到日历").click();
    expect(onCalendar).toHaveBeenCalledOnce();
  });
});

describe("提醒我 → 哪一屏（给 10b 的入口）", () => {
  const cases: [string, typeof push.support, boolean, string | null][] = [
    ["微信", { kind: "in-app-browser", app: "wechat" }, true, "guide-jumpout"],
    ["WhatsApp", { kind: "in-app-browser", app: "whatsapp" }, false, "guide-jumpout"],
    ["iOS Safari 标签页", { kind: "ios-needs-home" }, true, "guide-homescreen"],
    ["能直接订阅", { kind: "ready" }, false, null],
    ["不支持推送", { kind: "unsupported" }, false, null],
  ];

  for (const [name, support, ios, testid] of cases) {
    it(`${name} → ${testid ?? "不出引导屏"}`, () => {
      stubFetch({});
      push.support = support;
      push.ios = ios;
      const c = mount(<PushGuide signedIn={false} onCalendar={() => {}} onClose={() => {}} />);
      expect(needsPushGuide()).toBe(testid !== null);
      if (testid) expect(c.querySelector(`[data-testid=${testid}]`)).not.toBeNull();
      else expect(c.innerHTML).toBe("");
    });
  }
});

describe("H3–H8 · 主屏幕首开", () => {
  const me = { id: "u1", nickname: "阿杰" };

  it("H3：cookie 跟过来了，问「你是 阿杰 吗？」；是我 → H5 打开提醒", async () => {
    stubFetch({});
    const onDone = vi.fn();
    const c = mount(<FirstLaunch me={me} onIdentity={() => {}} onDone={onDone} />);

    expect(c.textContent).toContain("欢迎回来");
    expect(c.textContent).toContain("你是 阿杰 吗？");
    button(c, "是我").click();
    await vi.waitFor(() => expect(c.querySelector("[data-testid=firstrun-push]")).not.toBeNull());
    expect(c.textContent).toContain("最后一步：打开提醒");
  });

  it("H3 → 不是：退出这台设备，改成输认领码（H4）", async () => {
    const calls = stubFetch({
      "GET /api/me": respond(200, { user: me, device_id: "d1", devices: [] }),
      "DELETE /api/me/devices/d1": respond(204, null),
    });
    const onIdentity = vi.fn();
    const c = mount(<FirstLaunch me={me} onIdentity={onIdentity} onDone={() => {}} />);

    button(c, "不是").click();
    await vi.waitFor(() => expect(c.querySelector("[data-testid=firstrun-code]")).not.toBeNull());
    expect(calls.some((x) => x.method === "DELETE" && x.url === "/api/me/devices/d1")).toBe(true);
    expect(onIdentity).toHaveBeenCalledWith(null);
  });

  it("H4：没认出来 → 输满 6 位自动兑换，认回身份后进 H5", async () => {
    const calls = stubFetch({ "POST /api/claim/redeem": respond(200, { user: me }) });
    const onIdentity = vi.fn();
    const c = mount(<FirstLaunch me={null} onIdentity={onIdentity} onDone={() => {}} />);

    expect(c.textContent).toContain("输入认领码");
    const input = c.querySelector("input[aria-label=认领码]") as HTMLInputElement;
    input.value = "482 791";
    input.dispatchEvent(new Event("input", { bubbles: true }));

    await vi.waitFor(() => expect(c.querySelector("[data-testid=firstrun-push]")).not.toBeNull());
    expect(calls.find((x) => x.url === "/api/claim/redeem")?.body).toEqual({ code: "482791" });
    expect(onIdentity).toHaveBeenCalledWith(me);
    expect(localStorage.getItem("gd_has_identity")).toBe("1");
  });

  it("H4：码不对 → 清空重输，给出原因", async () => {
    stubFetch({ "POST /api/claim/redeem": respond(400, { error: { code: "claim_invalid", message: "兑换失败" } }) });
    const c = mount(<FirstLaunch me={null} onIdentity={() => {}} onDone={() => {}} />);

    const input = c.querySelector("input[aria-label=认领码]") as HTMLInputElement;
    input.value = "000000";
    input.dispatchEvent(new Event("input", { bubbles: true }));

    await vi.waitFor(() => expect(c.querySelector("[role=alert]")?.textContent).toContain("认领码不对或已过期"));
    expect(c.querySelector("[data-testid=firstrun-code]")).not.toBeNull();
  });

  it("H4 能跳过：「先不用」直接进牌局，并且不再出现", () => {
    stubFetch({});
    const onDone = vi.fn();
    const c = mount(<FirstLaunch me={null} onIdentity={() => {}} onDone={onDone} />);
    button(c, "先不用").click();
    expect(onDone).toHaveBeenCalledOnce();
    expect(localStorage.getItem("gd_pwa_welcomed")).toBe("1");
  });

  it("H5 → H7：允许 → 「搞定，到点叫你」→ 回到牌局", async () => {
    stubFetch({});
    const onDone = vi.fn();
    const c = mount(<FirstLaunch me={me} onIdentity={() => {}} onDone={onDone} />);
    button(c, "是我").click();
    await vi.waitFor(() => button(c, "打开提醒"));

    button(c, "打开提醒").click();
    await vi.waitFor(() => expect(c.querySelector("[data-testid=firstrun-granted]")).not.toBeNull());
    expect(c.textContent).toContain("搞定，到点叫你");
    expect(c.querySelector(".bell.solid")).not.toBeNull();

    button(c, "回到牌局").click();
    expect(onDone).toHaveBeenCalledOnce();
  });

  it("H5 → H8：不允许 → 教怎么去设置里打开，给「改用日历」", async () => {
    push.subscribe.mockResolvedValue({ kind: "denied" });
    stubFetch({});
    const c = mount(<FirstLaunch me={me} onIdentity={() => {}} onDone={() => {}} />);
    button(c, "是我").click();
    await vi.waitFor(() => button(c, "打开提醒"));
    button(c, "打开提醒").click();

    await vi.waitFor(() => expect(c.querySelector("[data-testid=firstrun-denied]")).not.toBeNull());
    expect(c.textContent).toContain("设置 › 通知 › 掼蛋");
    expect(button(c, "改用日历")).toBeTruthy();
  });

  it("H5 能跳过：「以后再说」", async () => {
    stubFetch({});
    const onDone = vi.fn();
    const c = mount(<FirstLaunch me={me} onIdentity={() => {}} onDone={onDone} />);
    button(c, "是我").click();
    await vi.waitFor(() => button(c, "以后再说"));
    button(c, "以后再说").click();
    expect(onDone).toHaveBeenCalledOnce();
    expect(push.subscribe).not.toHaveBeenCalled();
  });

  it("这台设备收不到推送（或已经允许过）：认完人就结束，不问提醒", async () => {
    push.support = { kind: "unsupported" };
    stubFetch({});
    const onDone = vi.fn();
    const c = mount(<FirstLaunch me={me} onIdentity={() => {}} onDone={onDone} />);
    button(c, "是我").click();
    expect(onDone).toHaveBeenCalledOnce();

    push.support = { kind: "ready" };
    push.permission = "granted";
    const c2 = mount(<FirstLaunch me={me} onIdentity={() => {}} onDone={onDone} />);
    button(c2, "是我").click();
    expect(onDone).toHaveBeenCalledTimes(2);
  });
});

describe("首开页的布局", () => {
  it(".firstrun 铺满 #app 这一列（flex 列里只写 margin auto 会缩到内容宽度）", () => {
    const css = readFileSync(resolve(process.cwd(), "src/guides.css"), "utf8");
    const rule = /\.firstrun\s*\{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(rule).toMatch(/(^|;|\s)width:\s*100%/);
  });
});

describe("App：什么时候出首开引导", () => {
  const games = respond(200, { games: [] });

  it("主屏幕图标打开、第一次 → 先出 H3", async () => {
    push.standalone = true;
    stubFetch({
      "GET /api/me": respond(200, { user: { id: "u1", nickname: "阿杰" }, device_id: "d1", devices: [] }),
      "GET /api/games": games,
    });
    window.history.replaceState(null, "", "/");
    const c = mount(<App />);
    await vi.waitFor(() => expect(c.querySelector("[data-testid=firstrun-who]")).not.toBeNull());
  });

  it("主屏幕打开、已经欢迎过 → 直接进牌局", async () => {
    push.standalone = true;
    localStorage.setItem("gd_pwa_welcomed", "1");
    stubFetch({ "GET /api/games": games });
    window.history.replaceState(null, "", "/");
    const c = mount(<App />);
    await tick();
    await tick();
    expect(c.querySelector("[data-testid^=firstrun]")).toBeNull();
  });

  it("微信里重载带 ?claim= 的页面：不自己兑换，令牌留给跳出去的浏览器", async () => {
    push.support = { kind: "in-app-browser", app: "wechat" };
    const calls = stubFetch({ "GET /api/games": games });
    window.history.replaceState(null, "", "/g/8K2QXM?claim=tok123");
    mount(<App />);
    await vi.waitFor(() => expect(calls.some((x) => x.url === "/api/me")).toBe(true));
    await tick();

    expect(calls.some((x) => x.url === "/api/claim/redeem")).toBe(false);
    expect(window.location.search).toBe("?claim=tok123");
  });

  it("普通浏览器打开带 ?claim= 的地址：照常兑换（对照）", async () => {
    const calls = stubFetch({
      "POST /api/claim/redeem": respond(200, { user: { id: "u1", nickname: "阿杰" } }),
      "GET /api/games": games,
    });
    window.history.replaceState(null, "", "/?claim=tok123");
    mount(<App />);
    await vi.waitFor(() => expect(calls.some((x) => x.url === "/api/claim/redeem")).toBe(true));
  });

  it("浏览器标签页里不出", async () => {
    push.standalone = false;
    stubFetch({ "GET /api/games": games });
    window.history.replaceState(null, "", "/");
    const c = mount(<App />);
    await tick();
    await tick();
    expect(c.querySelector("[data-testid^=firstrun]")).toBeNull();
  });
});
