import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GameDetail, GameSummary } from "./api";
import { installFakeSocket, lastSocket } from "./fakeSocket";

/**
 * #49：02 首页和 03 详情页接上 WebSocket。
 *
 * 这里只测「接线」——订阅了哪些主题、收到消息后有没有重新拉 REST、连不上时有没有
 * 降级轮询并显示「离线」。落座动画本身由 GameTable 比对前后座位播放，也在下面覆盖。
 */

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

function detail(seats: [number, string, string][]): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "8K2QXM",
      starts_at: 1791986400,
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      status: seats.length === 4 ? "full" : "open",
      version: 1,
      capacity: 4,
    },
    host: { id: "u1", nickname: "老王" },
    seats: seats.map(([seat_no, id, nickname]) => ({ seat_no, user: { id, nickname } })),
    waitlist: [],
    missing: 4 - seats.length,
    me: { role: "none" },
  };
}

const oneSeat: [number, string, string][] = [[1, "u1", "老王"]];
const threeSeats: [number, string, string][] = [
  [1, "u1", "老王"],
  [2, "u2", "Lily"],
  [3, "u3", "阿明"],
];

/**
 * 每个用例都重新载入页面模块。
 *
 * realtime.ts 里的连接是全站共用的单例，"只建一次" 正是它的设计；测试里如果复用，
 * 第二个用例就不会再建连接，也就无从驱动消息。resetModules() 让每个用例拿到干净的
 * 一份（node_modules 里的 preact 不会被重置，所以渲染器还是同一个）。
 */
async function loadPages() {
  vi.resetModules();
  const [home, detail] = await Promise.all([import("./pages/Home"), import("./pages/Detail")]);
  return { Home: home.Home, Detail: detail.Detail };
}

async function renderDetail(initial: GameDetail) {
  let gets = 0;
  const snapshots = [initial, detail(threeSeats)];
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      const body = snapshots[Math.min(gets, snapshots.length - 1)];
      gets++;
      return respond(200, body);
    }),
  );

  const { Detail } = await loadPages();
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(
    <Detail
      code="8K2QXM"
      me={null}
      requireIdentity={async () => null}
      toast={() => {}}
      onBack={() => {}}
    />,
    container,
  );
  return { container, getGets: () => gets };
}

async function renderHome(games: GameSummary[]) {
  let lists = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      lists++;
      return respond(200, { games });
    }),
  );

  const { Home } = await loadPages();
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(<Home me={null} onOpen={() => {}} onCreate={() => {}} onMe={() => {}} toast={() => {}} />, container);
  return { container, getLists: () => lists };
}

const summary: GameSummary = {
  short_code: "8K2QXM",
  starts_at: 1791986400,
  area: "SCBD",
  venue: "Pacific Place",
  kind: "classic",
  status: "open",
  seat_list: [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }],
  seats: 1,
  missing: 3,
  host: { id: "u1", nickname: "老王" },
};

describe("详情页的实时", () => {
  beforeEach(() => {
    installFakeSocket();
  });
  afterEach(() => {
    document.body.innerHTML = "";
    vi.unstubAllGlobals();
  });

  it("进页面就订阅这一局的主题，连上后把主题发过去", async () => {
    const { container } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const socket = lastSocket();
    socket.open();
    await vi.waitFor(() => expect([...socket.topics]).toEqual(["game:8K2QXM"]));
  });

  it("收到 seat.taken 就重新拉一次详情，新坐下的那位播落座动画", async () => {
    const { container, getGets } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    expect(getGets()).toBe(1);

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:8K2QXM", type: "seat.taken", v: 2, data: { seat_no: 2 } });

    await vi.waitFor(() => expect(getGets()).toBe(2));
    await vi.waitFor(() => expect(container.textContent).toContain("Lily"));

    // 新出现的两个座位（Lily、阿明）带 drop，原来就在的老王不带。
    await vi.waitFor(() => expect(container.querySelectorAll(".disc.drop").length).toBe(2));
    const wang = [...container.querySelectorAll(".disc")].find((d) => d.textContent === "老");
    expect(wang?.className).not.toContain("drop");
  });

  it("不比本地新的版本号直接丢掉，不重新拉", async () => {
    const { container, getGets } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:8K2QXM", type: "seat.taken", v: 5 });
    await vi.waitFor(() => expect(getGets()).toBe(2));

    // 乱序/重复的旧消息不该再触发一次请求。
    socket.receive({ topic: "game:8K2QXM", type: "seat.taken", v: 5 });
    socket.receive({ topic: "game:8K2QXM", type: "game.updated", v: 2 });
    await new Promise((r) => setTimeout(r, 20));
    expect(getGets()).toBe(2);
  });

  it("presence（在看人数）不是牌局变化，不重新拉详情；seat.taken 照样拉", async () => {
    const { container, getGets } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    expect(getGets()).toBe(1);

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 3 } });
    socket.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 4 } });
    await new Promise((r) => setTimeout(r, 20));
    expect(getGets()).toBe(1);

    // 真正的变化照样重新拉。
    socket.receive({ topic: "game:8K2QXM", type: "seat.taken", v: 2 });
    await vi.waitFor(() => expect(getGets()).toBe(2));
  });

  it("连不上三次就降级轮询，并显示「离线 · 15 秒刷新」", async () => {
    vi.useFakeTimers();
    try {
      const { container } = await renderDetail(detail(oneSeat));
      await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
      expect(container.textContent).not.toContain("离线");

      // 重连是退避的：每掉一次线，把定时器推过去让下一次连接建起来，再掉。
      for (let i = 0; i < 3; i++) {
        lastSocket().drop();
        await vi.advanceTimersByTimeAsync(31_000);
      }

      await vi.waitFor(() => expect(container.textContent).toContain("离线 · 15 秒刷新"));
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("首页的实时", () => {
  beforeEach(() => {
    installFakeSocket();
  });
  afterEach(() => {
    document.body.innerHTML = "";
    vi.unstubAllGlobals();
  });

  it("订阅当前片区的主题", async () => {
    const { container } = await renderHome([summary]);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const socket = lastSocket();
    socket.open();
    await vi.waitFor(() => expect([...socket.topics]).toEqual(
      // 默认是「全部」，服务端按片区发消息，所以逐个片区订阅。
      ["feed:SCBD", "feed:PIK", "feed:Central Park", "feed:Kemang", "feed:Kuningan",
       "feed:Senayan", "feed:Kelapa Gading", "feed:BSD", "feed:Other"],
    ));
  });

  it("选了具体片区就只订那一个", async () => {
    const { container } = await renderHome([summary]);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const pik = [...container.querySelectorAll(".areachips button")].find(
      (b) => b.textContent === "PIK",
    ) as HTMLButtonElement;
    pik.click();

    const socket = lastSocket();
    socket.open();
    await vi.waitFor(() => expect([...socket.topics]).toEqual(["feed:PIK"]));
  });

  it("feed 上有新局就重新拉列表", async () => {
    const { container, getLists } = await renderHome([summary]);
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    expect(getLists()).toBe(1);

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "feed:SCBD", type: "feed.game_added", v: 1 });

    await vi.waitFor(() => expect(getLists()).toBe(2));
  });
});

describe("useRealtime 的 presence", () => {
  beforeEach(() => {
    installFakeSocket();
  });
  afterEach(() => {
    document.body.innerHTML = "";
    vi.unstubAllGlobals();
  });

  it("presence 只交给 onPresence，不交给 onEvent", async () => {
    vi.resetModules();
    const { useRealtime } = await import("./useRealtime");
    const events: string[] = [];
    const viewers: [string, number][] = [];
    function Probe() {
      useRealtime({
        topics: ["game:8K2QXM"],
        onEvent: (ev) => events.push(ev.type),
        onPresence: (topic, n) => viewers.push([topic, n]),
      });
      return null;
    }
    const container = document.createElement("div");
    document.body.appendChild(container);
    render(<Probe />, container);

    const socket = await vi.waitFor(() => lastSocket());
    socket.open();
    socket.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 3 } });
    socket.receive({ topic: "game:8K2QXM", type: "seat.taken", v: 2 });
    await vi.waitFor(() => expect(events).toEqual(["seat.taken"]));
    expect(viewers).toEqual([["game:8K2QXM", 3]]);
  });
});

// #46 前端：原型 R1 的「👀 N 人正在看」。
describe("详情页的「N 人正在看」", () => {
  beforeEach(() => {
    installFakeSocket();
  });
  afterEach(() => {
    document.body.innerHTML = "";
    vi.unstubAllGlobals();
  });

  it("多于 1 人在看时显示，回落到 1 人（只剩自己）就藏起来", async () => {
    const { container } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    const pill = () => container.querySelector("[data-testid=viewers]");
    expect(pill()).toBeNull();

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 3 } });
    await vi.waitFor(() => expect(pill()?.textContent).toBe("👀 3 人正在看"));

    socket.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 1 } });
    await vi.waitFor(() => expect(pill()).toBeNull());
  });

  // #71 复审：断线后不再有 presence，人数会停在旧值，所以连接不是 online 时藏起来。
  it("掉线后胶囊藏起来，重连后等新的 presence 再出现", async () => {
    const { container } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    const pill = () => container.querySelector("[data-testid=viewers]");

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 3 } });
    await vi.waitFor(() => expect(pill()?.textContent).toBe("👀 3 人正在看"));

    socket.drop();
    await vi.waitFor(() => expect(pill()).toBeNull());

    // 重连上之后，旧的 3 不会自己冒出来，要等服务端再发。
    const again = await vi.waitFor(() => {
      const s = lastSocket();
      expect(s).not.toBe(socket);
      return s;
    }, { timeout: 5000 });
    again.open();
    await new Promise((r) => setTimeout(r, 20));
    expect(pill()).toBeNull();
    again.receive({ topic: "game:8K2QXM", type: "presence", data: { viewers: 2 } });
    await vi.waitFor(() => expect(pill()?.textContent).toBe("👀 2 人正在看"));
  });

  it("别的局的 presence 不算到这一局头上", async () => {
    const { container } = await renderDetail(detail(oneSeat));
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    const socket = lastSocket();
    socket.open();
    socket.receive({ topic: "game:OTHER1", type: "presence", data: { viewers: 5 } });
    await new Promise((r) => setTimeout(r, 20));
    expect(container.querySelector("[data-testid=viewers]")).toBeNull();
  });
});
