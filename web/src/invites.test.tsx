import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Friend, GameDetail } from "./api";
import { inviteSummary } from "./api";
import { installFakeSocket } from "./fakeSocket";
import { CreateGame } from "./pages/CreateGame";
import { Detail } from "./pages/Detail";

/** #115 邀请牌友：08 表单的「邀请谁」、局头详情页的「邀请牌友」、被邀请的人看到「X 邀请了你」。 */

function respond(status: number, body: unknown) {
  const text = body === undefined ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

const friend = (id: string, nickname: string, n = 2): Friend => ({
  user: { id, nickname },
  together_count: n,
  last_together_at: 1791986400,
  starred: false,
});
const FRIENDS = [friend("u2", "Lily", 5), friend("u3", "阿杰", 3), friend("u4", "Kevin", 1)];

function game(over: Partial<GameDetail> = {}): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "ABC123",
      starts_at: Math.floor(Date.now() / 1000) + 86400,
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      status: "open",
      version: 1,
      capacity: 4,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }],
    waitlist: [],
    missing: 3,
    me: { role: "seat", seat_no: 1 },
    ...over,
  };
}

type Call = { method: string; url: string; body: any };

function stub(routes: (c: Call) => Response | undefined) {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const c = { method: init?.method ?? "GET", url, body: init?.body ? JSON.parse(String(init.body)) : undefined };
      calls.push(c);
      return (
        routes(c) ??
        (url === "/api/friends" ? respond(200, { friends: FRIENDS }) : respond(404, { error: { code: "not_found", message: "" } }))
      );
    }),
  );
  return calls;
}

function mount(node: preact.ComponentChild) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(node, container);
  return container;
}

const byText = (c: HTMLElement, text: string) =>
  [...c.querySelectorAll("button")].find((b) => b.textContent?.includes(text)) as HTMLButtonElement | undefined;
const pick = (c: HTMLElement, name: string) =>
  ([...c.querySelectorAll(".pick")].find((b) => b.textContent?.includes(name)) as HTMLButtonElement).click();
const tick = () => new Promise((r) => setTimeout(r, 20));

beforeEach(() => installFakeSocket());
afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("08 开局表单：邀请谁", () => {
  function setup(inviteResp?: Response) {
    const toasts: string[] = [];
    const created: string[] = [];
    const calls = stub((c) => {
      if (c.method === "POST" && c.url === "/api/games") return respond(200, game());
      if (c.url === "/api/games/ABC123/invites") return inviteResp ?? respond(200, { invited: c.body.user_ids, skipped: [] });
      return undefined;
    });
    const container = mount(
      <CreateGame
        me={{ id: "u1", nickname: "老王" }}
        requireIdentity={async () => null}
        toast={(t) => toasts.push(t)}
        onBack={() => {}}
        onCreated={(code) => created.push(code)}
      />,
    );
    const venue = container.querySelector("#venue") as HTMLInputElement;
    venue.value = "Pacific Place";
    venue.dispatchEvent(new Event("input", { bubbles: true }));
    return { container, calls, toasts, created };
  }

  async function choose(container: HTMLElement, names: string[]) {
    byText(container, "从牌友里选")!.click();
    await vi.waitFor(() => expect(container.querySelectorAll(".pick").length).toBe(3));
    for (const n of names) pick(container, n);
    await tick();
    byText(container, `就这 ${names.length} 位`)!.click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=friend-picker]")).toBeNull());
  }

  it("选了两位：表单上显示他们；开好局后 POST invites，再进详情页", async () => {
    const { container, calls, toasts, created } = setup();
    await choose(container, ["Lily", "Kevin"]);
    expect([...container.querySelectorAll(".invitees .chip")].map((c) => c.textContent)).toEqual(["Lily", "Kevin"]);

    await tick();
    (container.querySelector(".actionbar .btn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(created).toEqual(["ABC123"]));
    const inv = calls.find((c) => c.url === "/api/games/ABC123/invites")!;
    expect(inv.method).toBe("POST");
    expect(inv.body).toEqual({ user_ids: ["u2", "u4"] });
    // 先开局再邀请。
    expect(calls.findIndex((c) => c.url === "/api/games")).toBeLessThan(calls.indexOf(inv));
    expect(toasts.join()).toContain("已邀请 2 位牌友");
  });

  it("不选人就不发邀请", async () => {
    const { container, calls, created } = setup();
    await tick();
    (container.querySelector(".actionbar .btn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(created).toEqual(["ABC123"]));
    expect(calls.some((c) => c.url.endsWith("/invites"))).toBe(false);
  });

  it("邀请失败不拦着进详情页，提示可以在详情页再邀请", async () => {
    const { container, toasts, created } = setup(respond(409, { error: { code: "quota_exceeded", message: "" } }));
    await choose(container, ["Lily"]);
    await tick();
    (container.querySelector(".actionbar .btn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(created).toEqual(["ABC123"]));
    expect(toasts.join()).toContain("今天邀请太多了");
    expect(toasts.join()).toContain("可以在详情页再邀请");
  });

  it("没有牌友：面板里说清楚，可以「不邀请了」", async () => {
    const { container } = setup();
    stub((c) => (c.url === "/api/friends" ? respond(200, { friends: [] }) : undefined));
    byText(container, "从牌友里选")!.click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=picker-empty]")).not.toBeNull());
    byText(container, "不邀请了")!.click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=friend-picker]")).toBeNull());
  });
});

describe("详情页：局头邀请牌友", () => {
  function setup(d: GameDetail, me = { id: "u1", nickname: "老王" }, inviteResp?: Response) {
    const toasts: string[] = [];
    const calls = stub((c) => {
      if (c.method === "GET" && c.url === "/api/games/ABC123") return respond(200, d);
      if (c.url === "/api/games/ABC123/invites") return inviteResp ?? respond(200, { invited: ["u2"], skipped: ["u3"] });
      return undefined;
    });
    vi.stubGlobal("navigator", { userAgent: "Mozilla/5.0 (iPhone)" });
    const container = mount(
      <Detail code="ABC123" me={me} requireIdentity={async () => null} toast={(t) => toasts.push(t)} onBack={() => {}} />,
    );
    return { container, calls, toasts };
  }

  it("局头看得到「＋ 邀请牌友」，选人后 POST，并提示结果", async () => {
    const { container, calls, toasts } = setup(game());
    await vi.waitFor(() => expect(byText(container, "邀请牌友")).toBeTruthy());
    byText(container, "邀请牌友")!.click();
    await vi.waitFor(() => expect(container.querySelectorAll(".pick").length).toBe(3));
    // 一个都没选时按钮不能点。
    expect(byText(container, "选几位牌友")!.disabled).toBe(true);
    pick(container, "Lily");
    pick(container, "阿杰");
    await tick();
    byText(container, "邀请 2 位")!.click();
    await vi.waitFor(() => expect(toasts).toContain("已邀请 1 位牌友，他们会收到通知（1 位已在局里或邀请过）"));
    expect(calls.find((c) => c.url.endsWith("/invites"))!.body).toEqual({ user_ids: ["u2", "u3"] });
    await vi.waitFor(() => expect(container.querySelector("[data-testid=friend-picker]")).toBeNull());
  });

  it("已经在局里的牌友标出来，不能选", async () => {
    const d = game({
      seats: [
        { seat_no: 1, user: { id: "u1", nickname: "老王" } },
        { seat_no: 2, user: { id: "u2", nickname: "Lily" } },
      ],
      waitlist: [{ position: 1, user: { id: "u3", nickname: "阿杰" } }],
      missing: 2,
    });
    const { container } = setup(d);
    await vi.waitFor(() => expect(byText(container, "邀请牌友")).toBeTruthy());
    byText(container, "邀请牌友")!.click();
    await vi.waitFor(() => expect(container.querySelectorAll(".pick").length).toBe(3));
    const row = (n: string) => [...container.querySelectorAll(".pick")].find((b) => b.textContent?.includes(n)) as HTMLButtonElement;
    expect(row("Lily").disabled).toBe(true);
    expect(row("Lily").textContent).toContain("已在座位上");
    expect(row("阿杰").textContent).toContain("候补第 1 位");
    expect(row("Kevin").disabled).toBe(false);
    row("Lily").click();
    await tick();
    expect(row("Lily").getAttribute("aria-checked")).toBe("false");
  });

  it("not_friend：整次失败，面板留着，提示刷新", async () => {
    const { container, toasts } = setup(game(), undefined, respond(404, { error: { code: "not_friend", message: "" } }));
    await vi.waitFor(() => expect(byText(container, "邀请牌友")).toBeTruthy());
    byText(container, "邀请牌友")!.click();
    await vi.waitFor(() => expect(container.querySelectorAll(".pick").length).toBe(3));
    pick(container, "Lily");
    await tick();
    byText(container, "邀请 1 位")!.click();
    await vi.waitFor(() => expect(toasts).toContain("有人不在你的牌友里了，刷新一下再选"));
    expect(container.querySelector("[data-testid=friend-picker]")).not.toBeNull();
  });

  it("不是局头、或者局已结束：没有这个按钮", async () => {
    const guest = setup({ ...game(), me: { role: "none" } }, { id: "u9", nickname: "路人" });
    await vi.waitFor(() => expect(guest.container.textContent).toContain("Pacific Place"));
    expect(byText(guest.container, "邀请牌友")).toBeUndefined();
    document.body.innerHTML = "";

    const done = setup({ ...game(), game: { ...game().game, status: "done" } });
    await vi.waitFor(() => expect(done.container.textContent).toContain("这一局已经结束"));
    expect(byText(done.container, "邀请牌友")).toBeUndefined();
  });

  it("被邀请、还没报名：显示「老王 邀请了你」；报了名就不显示", async () => {
    const invited = setup({ ...game(), me: { role: "none", invited_by: { id: "u1", nickname: "老王" } } }, { id: "u2", nickname: "Lily" });
    await vi.waitFor(() => expect(invited.container.querySelector("[data-testid=invited-by]")?.textContent).toContain("老王 邀请了你"));
    expect(invited.container.textContent).toContain("还差 3 人，报个名占座吧");
    document.body.innerHTML = "";

    const seated = setup(
      { ...game(), me: { role: "seat", seat_no: 2, invited_by: { id: "u1", nickname: "老王" } } },
      { id: "u2", nickname: "Lily" },
    );
    await vi.waitFor(() => expect(seated.container.textContent).toContain("Pacific Place"));
    expect(seated.container.querySelector("[data-testid=invited-by]")).toBeNull();
  });
});

describe("inviteSummary", () => {
  it("全被跳过时说清楚", () => {
    expect(inviteSummary({ invited: [], skipped: ["a"] })).toBe("他们都已经在局里或邀请过了");
    expect(inviteSummary({ invited: ["a", "b"], skipped: [] })).toBe("已邀请 2 位牌友，他们会收到通知");
  });
});
