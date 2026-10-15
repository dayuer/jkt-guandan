import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GameDetail, GamePatch } from "../api";
import { Detail } from "./Detail";
import { installFakeSocket } from "../fakeSocket";

// 埋点（#117）在这里换成间谍：既能断言漏斗事件，又不会往假后端发 /api/events。
const telemetry = vi.hoisted(() => ({ track: vi.fn() }));
vi.mock("../telemetry", () => telemetry);

function detail(version: number, over: Partial<GameDetail["game"]> = {}): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "ABC123",
      starts_at: 1791986400,
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      status: "open",
      version,
      capacity: 4,
      ...over,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }],
    waitlist: [],
    missing: 3,
    me: { role: "seat", seat_no: 1 },
  };
}

function respond(status: number, body: unknown) {
  const text = JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

// 用例结束时把挂上的详情页卸掉：只清 DOM 的话组件还活着，它排在下一帧之后的 effect
// （比如记 detail_view）会落进下一个用例（#167）。
const mounted: HTMLElement[] = [];

/** 起一个详情页：GET 返回 initial，PATCH 按 patchResponses 依次返回。 */
function setup(initial: GameDetail, patchResponses: Response[]) {
  const patches: { body: GamePatch; ifMatch: string | null }[] = [];
  const toasts: string[] = [];
  let gets = 0;

  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      if (method === "GET") {
        gets++;
        return respond(200, initial);
      }
      patches.push({
        body: JSON.parse(String(init?.body ?? "{}")),
        ifMatch: (init?.headers as Record<string, string>)?.["If-Match"] ?? null,
      });
      return patchResponses[Math.min(patches.length, patchResponses.length) - 1];
    }),
  );

  const container = document.createElement("div");
  document.body.appendChild(container);
  render(
    <Detail
      code="ABC123"
      me={{ id: "u1", nickname: "老王" }}
      requireIdentity={async () => null}
      toast={(t: string) => toasts.push(t)}
      onBack={() => {}}
    />,
    container,
  );
  mounted.push(container);
  return { container, patches, toasts, getGets: () => gets };
}

/** 打开编辑弹层，改地点，提交。 */
async function editVenue(container: HTMLElement, venue: string) {
  const open = [...container.querySelectorAll("button")].find((b) => b.textContent?.includes("改时间")) as HTMLButtonElement;
  open.click();
  await vi.waitFor(() => expect(container.querySelector("#venue")).not.toBeNull());

  const input = container.querySelector("#venue") as HTMLInputElement;
  input.value = venue;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  // Preact 的状态更新是异步的：等它重渲染完再提交，否则读到的还是旧值。
  await new Promise((resolve) => setTimeout(resolve, 20));

  const form = container.querySelector("form.sheet") as HTMLFormElement;
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
}

beforeEach(() => {
  installFakeSocket();
});

afterEach(() => {
  for (const c of mounted.splice(0)) render(null, c);
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("局头改牌局的版本冲突（K4 / K5 / K6）", () => {
  // K5：冲突与本次修改无关（期间有人报名）——自动带新版本号重提一次，不打断用户。
  it("K5：字段没冲突就自动重提一次，并提示「已保存 · 期间有人报名」", async () => {
    const { container, patches, toasts } = setup(detail(1), [
      respond(409, { ...detail(2), error: { code: "version_conflict", message: "冲突" } }),
      respond(200, detail(3)),
    ]);

    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    await editVenue(container, "Grand Indonesia");
    await vi.waitFor(() => expect(patches.length).toBe(2));

    // 第一次用编辑开始时的版本号，第二次用 409 里带回来的最新版本号。
    expect(patches[0].ifMatch).toBe("1");
    expect(patches[1].ifMatch).toBe("2");
    expect(patches[1].body).toEqual({ venue: "Grand Indonesia" });
    await vi.waitFor(() => expect(toasts).toContain("已保存 · 期间有人报名"));
  });

  // K4：同一个字段被两个人改了——摆出对比，让局头选。
  it("K4：字段真冲突时弹出对比框，不擅自覆盖", async () => {
    const { container, patches, toasts } = setup(detail(1), [
      respond(409, { ...detail(2, { venue: "PIK Avenue" }), error: { code: "version_conflict", message: "冲突" } }),
      respond(200, detail(3)),
    ]);

    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    await editVenue(container, "Grand Indonesia");

    await vi.waitFor(() => expect(container.querySelector('[data-testid="conflict-dialog"]')).not.toBeNull());
    expect(container.textContent).toContain("Grand Indonesia");
    expect(container.textContent).toContain("PIK Avenue");
    // 还没提交第二次：等用户选。
    expect(patches.length).toBe(1);

    // 选「仍然改成我的」→ 带最新版本号重提。
    const keep = [...container.querySelectorAll("button")].find((b) => b.textContent === "仍然改成我的") as HTMLButtonElement;
    keep.click();
    await vi.waitFor(() => expect(patches.length).toBe(2));
    expect(patches[1].ifMatch).toBe("2");
    await vi.waitFor(() => expect(toasts).toContain("已按你的版本改好"));
  });

  // K6：编辑期间局头被转给了别人——丢掉本地修改，重新加载。
  it("K6：403 not_host 时提示并重新加载", async () => {
    const { container, toasts, getGets } = setup(detail(1), [
      respond(403, { error: { code: "not_host", message: "只有局头能做这件事" } }),
    ]);

    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    const before = getGets();
    await editVenue(container, "Grand Indonesia");

    await vi.waitFor(() => expect(toasts).toContain("你已经不是局头了"));
    await vi.waitFor(() => expect(getGets()).toBeGreaterThan(before));
  });
});

// 分享面板（§5.4）：从详情页左下角进去，任何能看到这一局的人都能分享——
// 报了名的、路过的，都能帮局头拉人。
describe("分享入口", () => {
  it("底部有「⇪ 分享」，点开面板", async () => {
    vi.stubGlobal("navigator", { userAgent: "Mozilla/5.0 (iPhone)", clipboard: { writeText: async () => {} } });
    const { container } = setup(detail(1), []);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const share = [...container.querySelectorAll(".actionbar button")].find((b) =>
      b.textContent?.includes("分享"),
    ) as HTMLButtonElement;
    expect(share).toBeTruthy();
    share.click();

    await vi.waitFor(() => expect(container.querySelector("[data-testid=share-sheet]")).not.toBeNull());
    expect(container.querySelector(".script p")?.textContent).toContain("差 3 人");
  });

  it("已经结束的局不再显示分享——发出去也没用", async () => {
    const { container } = setup(detail(2, { status: "done" }), []);
    await vi.waitFor(() => expect(container.textContent).toContain("这一局已经结束"));

    const share = [...container.querySelectorAll(".actionbar button")].find((b) =>
      b.textContent?.includes("分享"),
    );
    expect(share).toBeUndefined();
  });
});

// 加入日历（#6）：座位上的人能在详情页加日历；刚报上名时自动问一句。
describe("加入日历", () => {
  const SAFARI = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Version/17.0 Mobile/15E148 Safari/604.1";
  const WECHAT = "Mozilla/5.0 (iPhone) Mobile/15E148 MicroMessenger/8.0.47 NetType/WIFI";

  const calButton = (c: HTMLElement) =>
    [...c.querySelectorAll("button")].find((b) => b.textContent?.includes("加入日历")) as HTMLButtonElement | undefined;

  it("浏览器里：已报名的人点开后有 .ics 和 Google Calendar 两个出口", async () => {
    vi.stubGlobal("navigator", { userAgent: SAFARI });
    const { container } = setup(detail(1), []);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    calButton(container)!.click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=calendar-sheet]")).not.toBeNull());
    expect(container.querySelector("[data-testid=calendar-ics]")?.getAttribute("href")).toBe("/g/ABC123/game.ics");
    expect(container.querySelector("[data-testid=calendar-google]")?.getAttribute("href")).toBe("/g/ABC123/google");
    expect(container.querySelector("[data-testid=calendar-wechat-hint]")).toBeNull();
  });

  it("微信里：不给下载，提示跳到浏览器", async () => {
    vi.stubGlobal("navigator", { userAgent: WECHAT });
    const { container } = setup(detail(1), []);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    calButton(container)!.click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=calendar-wechat-hint]")).not.toBeNull());
    expect(container.querySelector("[data-testid=calendar-ics]")).toBeNull();
    expect(container.querySelector("[data-testid=calendar-google]")).toBeNull();
    expect(container.textContent).toContain("在浏览器打开");
  });

  it("没报名、候补和已结束的局都不显示", async () => {
    vi.stubGlobal("navigator", { userAgent: SAFARI });
    for (const d of [
      { ...detail(1), me: { role: "none" as const } },
      { ...detail(1), me: { role: "waitlist" as const } },
      detail(1, { status: "done" }),
    ]) {
      const { container } = setup(d, []);
      await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
      expect(calButton(container)).toBeUndefined();
      document.body.innerHTML = "";
    }
  });

  it("报名成功后弹出「占上了！」，从那里点「加到日历」进日历弹层", async () => {
    vi.stubGlobal("navigator", { userAgent: SAFARI });
    const before = { ...detail(1), me: { role: "none" as const } };
    const after = { ...detail(2), me: { role: "seat" as const, seat_no: 2 } };
    const { container } = setup(before, [respond(200, after)]);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const join = [...container.querySelectorAll(".actionbar button")].find((b) =>
      b.textContent?.includes("报名占座"),
    ) as HTMLButtonElement;
    // detail_view 在 useEffect 里记，Preact 把它排在下一帧之后：先等它记上再清，
    // 否则机器一忙它会落在清空之后、混进下面的漏斗断言（#167）。
    await vi.waitFor(() => expect(telemetry.track).toHaveBeenCalledWith("detail_view", { game: "ABC123" }));
    telemetry.track.mockClear();
    join.click();
    await vi.waitFor(() => expect(container.querySelector(".sheet.joined")).not.toBeNull());
    expect(container.textContent).toContain("占上了");
    // 漏斗：点报名、报上名（#117）。
    expect(telemetry.track.mock.calls.map((c) => c[0])).toEqual(["join_click", "join_ok"]);
    expect(telemetry.track).toHaveBeenCalledWith("join_ok", { game: "ABC123" });

    const cal = [...container.querySelectorAll(".sheet.joined button")].find((b) =>
      b.textContent?.includes("加到日历"),
    ) as HTMLButtonElement;
    cal.click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=calendar-sheet]")).not.toBeNull());
    expect(container.querySelector(".sheet.joined")).toBeNull();
  });

  it("微信里报上名、点「提醒我」：盖上 H1 跳出引导，不去要推送权限（#10b 接 #70）", async () => {
    const WECHAT = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 MicroMessenger/8.0.49";
    vi.stubGlobal("navigator", { userAgent: WECHAT, maxTouchPoints: 5 });
    const requestPermission = vi.fn();
    vi.stubGlobal("Notification", { requestPermission, permission: "default" });
    const before = { ...detail(1), me: { role: "none" as const } };
    const after = { ...detail(2), me: { role: "seat" as const, seat_no: 2 } };
    const claim = respond(200, { claim_token: "tok", code: "123456", expires_at: 0 });
    const { container } = setup(before, [respond(200, after), claim]);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    ([...container.querySelectorAll(".actionbar button")].find((b) =>
      b.textContent?.includes("报名占座"),
    ) as HTMLButtonElement).click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=joined-sheet]")).not.toBeNull());

    ([...container.querySelectorAll("[data-testid=joined-sheet] button")].find((b) =>
      b.textContent?.includes("提醒我"),
    ) as HTMLButtonElement).click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=guide-jumpout]")).not.toBeNull());
    expect(requestPermission).not.toHaveBeenCalled();
    // 引导屏盖在报名成功弹层上，关掉引导还回到弹层。
    expect(container.querySelector("[data-testid=joined-sheet]")).not.toBeNull();
  });
});

describe("埋点（#117）", () => {
  beforeEach(() => {
    installFakeSocket();
    telemetry.track.mockClear();
  });

  afterEach(() => {
    document.body.innerHTML = "";
    vi.unstubAllGlobals();
  });

  it("打开详情页记一次 detail_view；重新拉数据不重复记；点分享记 share_open", async () => {
    const { container } = setup(detail(2), []);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    await vi.waitFor(() => expect(telemetry.track).toHaveBeenCalledWith("detail_view", { game: "ABC123" }));

    ([...container.querySelectorAll("button")].find((b) => b.textContent?.includes("分享")) as HTMLButtonElement).click();
    await vi.waitFor(() => expect(telemetry.track).toHaveBeenCalledWith("share_open", { game: "ABC123" }));
    expect(telemetry.track.mock.calls.filter((c) => c[0] === "detail_view")).toHaveLength(1);
  });
});
