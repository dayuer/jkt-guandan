import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GameDetail, GameSummary } from "./api";
import { installFakeSocket, lastSocket } from "./fakeSocket";
import { Detail } from "./pages/Detail";
import { Home } from "./pages/Home";
import { timeHM } from "./format";
import { acknowledge, fromDetail, fromSummary, noticesFor, observe, snapshotKey } from "./status";

/** #114：改时间横幅（A3 / R1）、首页标签（W4）、候补转正（W3）、局头转交（K2）。 */

const ME = { id: "u2", nickname: "阿明" };
const T0 = 1791986400; // 某天 19:30 WIB
const T1 = T0 + 30 * 60; // 改到 20:00

function detail(over: { starts_at?: number; venue?: string; role?: "seat" | "waitlist" | "none"; hostID?: string; version?: number } = {}): GameDetail {
  const hostID = over.hostID ?? "u1";
  const role = over.role ?? "seat";
  return {
    game: {
      id: "g1",
      short_code: "ABC123",
      starts_at: over.starts_at ?? T0,
      area: "SCBD",
      venue: over.venue ?? "Pacific Place",
      kind: "classic",
      status: "open",
      version: over.version ?? 1,
      capacity: 4,
    },
    host: { id: hostID, nickname: hostID === "u1" ? "老王" : "阿明" },
    seats:
      hostID === "u1"
        ? [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }, ...(role === "seat" ? [{ seat_no: 2, user: ME }] : [])]
        : [{ seat_no: 2, user: ME }],
    waitlist: role === "waitlist" ? [{ position: 1, user: ME }] : [],
    missing: 2,
    me: role === "seat" ? { role, seat_no: 2 } : role === "waitlist" ? { role, position: 1 } : { role },
  };
}

function summary(over: Partial<GameSummary> = {}): GameSummary {
  return {
    short_code: "ABC123",
    starts_at: T0,
    area: "SCBD",
    venue: "Pacific Place",
    kind: "classic",
    status: "open",
    seat_list: [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }],
    seats: 1,
    missing: 3,
    host: { id: "u1", nickname: "老王" },
    ...over,
  };
}

function respond(status: number, body: unknown) {
  const text = JSON.stringify(body);
  return { ok: status >= 200 && status < 300, status, headers: { get: () => null }, text: async () => text, json: async () => body } as unknown as Response;
}

function button(c: HTMLElement, text: string): HTMLButtonElement {
  const b = [...c.querySelectorAll("button")].find((x) => x.textContent?.includes(text));
  if (!b) throw new Error(`没有「${text}」：${c.textContent}`);
  return b as HTMLButtonElement;
}

function mountDetail(responses: () => GameDetail, extra: (url: string, init?: RequestInit) => Response | undefined = () => undefined) {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? "GET"} ${url}`);
      return extra(url, init) ?? respond(200, responses());
    }),
  );
  const c = document.createElement("div");
  document.body.appendChild(c);
  render(<Detail code="ABC123" me={ME} requireIdentity={async () => ME} toast={() => {}} onBack={() => {}} />, c);
  return { c, calls };
}

beforeEach(() => installFakeSocket());

afterEach(() => {
  document.body.innerHTML = "";
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("快照比较", () => {
  it("第一次看到这一局不提示，只记下来", () => {
    expect(observe(fromDetail(detail()), ME.id)).toEqual([]);
    expect(noticesFor(fromDetail(detail()), ME.id)).toEqual([]);
  });

  it("局头改了时间 → 改时间提示；关掉之后不再提示", () => {
    observe(fromDetail(detail()), ME.id);
    const moved = fromDetail(detail({ starts_at: T1 }));
    expect(observe(moved, ME.id)).toEqual([
      { kind: "rescheduled", code: "ABC123", startsAt: T1, venue: "Pacific Place", timeChanged: true, venueChanged: false },
    ]);
    // 没关掉：再看一次还在。
    expect(noticesFor(moved, ME.id)).toHaveLength(1);
    acknowledge(moved, ME.id, ["rescheduled"]);
    expect(noticesFor(moved, ME.id)).toEqual([]);
  });

  it("我是局头时，改时间的就是我，不提示", () => {
    observe(fromDetail(detail({ hostID: ME.id })), ME.id);
    expect(observe(fromDetail(detail({ hostID: ME.id, starts_at: T1 })), ME.id)).toEqual([]);
  });

  it("先被改了时间、后来又成了局头：两条提示都在", () => {
    observe(fromDetail(detail()), ME.id);
    const now = fromDetail(detail({ hostID: ME.id, starts_at: T1 }));
    expect(noticesFor(now, ME.id).map((n) => n.kind).sort()).toEqual(["host", "rescheduled"]);
  });

  it("换了身份（认领 / 「不是我」）：A 的快照永远不会给 B 出提示", () => {
    const B = "u9";
    // A 在这一局里排候补、看过旧时间。
    observe(fromDetail(detail({ role: "waitlist" })), ME.id);
    // 设备换成 B 之后，B 坐着、时间也变了、B 还成了局头：对 B 来说这都是第一次看到。
    const forB = { ...fromDetail(detail({ role: "seat", starts_at: T1 })), hostID: B };
    expect(noticesFor(forB, B)).toEqual([]);
    expect(observe(forB, B)).toEqual([]);
    // A 的快照原样留着，B 有了自己的一份。
    expect(noticesFor(fromDetail(detail({ role: "seat" })), ME.id).map((n) => n.kind)).toEqual(["promoted"]);
    expect(localStorage.getItem(snapshotKey(B))).toContain("ABC123");
  });

  it("不在局里（退出了）就删掉快照，之后不提示", () => {
    observe(fromDetail(detail()), ME.id);
    observe(fromDetail(detail({ role: "none" })), ME.id);
    expect(noticesFor(fromDetail(detail({ starts_at: T1 })), ME.id)).toEqual([]);
  });

  it("候补 → 坐上 = 补位；自己报名坐上（之前不在）不算", () => {
    observe(fromDetail(detail({ role: "waitlist" })), ME.id);
    expect(observe(fromDetail(detail({ role: "seat" })), ME.id).map((n) => n.kind)).toEqual(["promoted"]);

    localStorage.clear();
    observe(fromDetail(detail({ role: "none" })), ME.id);
    expect(observe(fromDetail(detail({ role: "seat" })), ME.id)).toEqual([]);
  });

  it("坐着 → 成了局头 = 局头转交，带上原局头的称呼", () => {
    observe(fromDetail(detail()), ME.id);
    expect(observe(fromDetail(detail({ hostID: ME.id })), ME.id)).toEqual([{ kind: "host", code: "ABC123", formerHost: "老王" }]);
  });

  it("关掉一种提示只跟上那一种字段，另一种还在", () => {
    observe(fromDetail(detail({ role: "waitlist" })), ME.id);
    const now = fromDetail(detail({ role: "seat", starts_at: T1 }));
    expect(noticesFor(now, ME.id).map((n) => n.kind).sort()).toEqual(["promoted", "rescheduled"]);
    acknowledge(now, ME.id, ["promoted"]);
    expect(noticesFor(now, ME.id).map((n) => n.kind)).toEqual(["rescheduled"]);
  });

  it("列表项：my_role 换成详情的写法；没给 my_role 时不判断角色", () => {
    expect(fromSummary(summary({ my_role: "seated" })).role).toBe("seat");
    expect(fromSummary(summary({ my_role: "waitlist" })).role).toBe("waitlist");
    expect(fromSummary(summary()).role).toBeUndefined();
  });

  it("开局一天之后的快照自动清掉", () => {
    observe(fromDetail(detail()), ME.id);
    acknowledge(fromDetail(detail({ starts_at: T0 + 1000 })), ME.id, undefined, localStorage, T0 + 3 * 86400);
    expect(localStorage.getItem(snapshotKey(ME.id))).toBe("{}");
  });
});

describe("详情页", () => {
  it("R1：实时事件带来新时间 → 出改时间横幅，关掉后刷新也不再出", async () => {
    let current = detail();
    const { c } = mountDetail(() => current);
    await vi.waitFor(() => expect(c.textContent).toContain("Pacific Place"));
    expect(c.querySelector("[data-testid=notice-rescheduled]")).toBeNull();

    current = detail({ starts_at: T1, version: 2 });
    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:ABC123", type: "game.updated", v: 2 });

    await vi.waitFor(() => expect(c.querySelector("[data-testid=notice-rescheduled]")?.textContent).toContain("局头把时间改到"));
    expect(c.querySelector("[data-testid=notice-rescheduled]")?.textContent).toContain(timeHM(T1));

    button(c, "×").click();
    await vi.waitFor(() => expect(c.querySelector("[data-testid=notice-rescheduled]")).toBeNull());
    expect(noticesFor(fromDetail(current), ME.id)).toEqual([]);
  });

  it("W3：上次是候补、这次打开已经坐上 → 弹「你补上了」；「好的，到时见」关掉", async () => {
    observe(fromDetail(detail({ role: "waitlist" })), ME.id);
    const { c } = mountDetail(() => detail({ role: "seat" }));
    await vi.waitFor(() => expect(c.querySelector("[data-testid=promoted-sheet]")).not.toBeNull());
    expect(c.textContent).toContain("轮到你了，已经给你占上座");

    button(c, "好的，到时见").click();
    await vi.waitFor(() => expect(c.querySelector("[data-testid=promoted-sheet]")).toBeNull());
    expect(noticesFor(fromDetail(detail({ role: "seat" })), ME.id)).toEqual([]);
  });

  it("W3：「去不了了，让给下一位」= 退出", async () => {
    observe(fromDetail(detail({ role: "waitlist" })), ME.id);
    const { c, calls } = mountDetail(
      () => detail({ role: "seat" }),
      (_url, init) => (init?.method === "DELETE" ? respond(200, detail({ role: "none" })) : undefined),
    );
    await vi.waitFor(() => button(c, "去不了了"));
    button(c, "去不了了").click();
    await vi.waitFor(() => expect(calls).toContain("DELETE /api/games/ABC123/seat"));
    // 退出之后不在局里了，快照也跟着删掉（等它落定，免得写到下一个用例的存储里）。
    await vi.waitFor(() => expect(localStorage.getItem(snapshotKey(ME.id))).toBe("{}"));
  });

  it("K2：原局头退出、我成了局头 → 深色横幅「老王退出了，你现在是局头」", async () => {
    observe(fromDetail(detail()), ME.id);
    const { c } = mountDetail(() => detail({ hostID: ME.id }));
    await vi.waitFor(() => expect(c.textContent).toContain("Pacific Place"));
    await vi.waitFor(() => expect(c.querySelector("[data-testid=notice-host]")).not.toBeNull());
    const banner = c.querySelector("[data-testid=notice-host]")!;
    expect(banner.classList.contains("ink")).toBe(true);
    expect(banner.textContent).toContain("老王退出了，");
    expect(banner.textContent).toContain("你现在是局头");
  });
});

describe("首页", () => {
  function mountHome(games: GameSummary[]) {
    vi.stubGlobal("fetch", vi.fn(async () => respond(200, { games })));
    const c = document.createElement("div");
    document.body.appendChild(c);
    const opened: string[] = [];
    render(<Home me={ME} onOpen={(code) => opened.push(code)} onCreate={() => {}} toast={() => {}} />, c);
    return { c, opened };
  }

  it("W4：已报名、候补第 N 位；没给 my_role 的照旧显示差几人", async () => {
    const { c } = mountHome([
      summary({ short_code: "A1", my_role: "seated" }),
      summary({ short_code: "A2", my_role: "waitlist", waitlist_pos: 2 }),
      summary({ short_code: "A3" }),
    ]);
    await vi.waitFor(() => expect(c.querySelectorAll(".gamecard")).toHaveLength(3));
    const tags = [...c.querySelectorAll(".gamecard .need")].map((t) => [t.className, t.textContent]);
    expect(tags).toEqual([
      ["need mine", "已报名"],
      ["need waiting", "候补第 2 位"],
      ["need", "差 3 人"],
    ]);
  });

  it("A3：我报的局被改了时间 → 首页横幅点名是哪一局，点了进详情", async () => {
    observe(fromSummary(summary({ my_role: "seated" })), ME.id);
    const { c, opened } = mountHome([summary({ my_role: "seated", starts_at: T1 })]);
    await vi.waitFor(() => expect(c.querySelector("[data-testid=notice-rescheduled]")).not.toBeNull());
    expect(c.querySelector("[data-testid=notice-rescheduled]")?.textContent).toContain("局头把 Pacific Place 那局改到");

    button(c, "局头把 Pacific Place").click();
    expect(opened).toEqual(["ABC123"]);
  });

  it("W3：下次打开首页时发现已经补上 → 弹层；「加到日历」带去详情页", async () => {
    observe(fromSummary(summary({ my_role: "waitlist", waitlist_pos: 1 })), ME.id);
    const { c, opened } = mountHome([summary({ my_role: "seated" })]);
    await vi.waitFor(() => expect(c.querySelector("[data-testid=promoted-sheet]")).not.toBeNull());
    button(c, "加到日历").click();
    expect(opened).toEqual(["ABC123"]);
    await vi.waitFor(() => expect(c.querySelector("[data-testid=promoted-sheet]")).toBeNull());
  });
});
