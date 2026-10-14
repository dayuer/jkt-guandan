import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { installFakeSocket } from "./fakeSocket";

/** 假的 fetch 响应：api.ts 用的是 text() + JSON.parse。 */
function respond(status: number, body: unknown) {
  const text = JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

function stubFetch(route: (url: string, init?: RequestInit) => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => route(url, init)),
  );
}

function visit(path: string) {
  window.history.replaceState(null, "", path);
}

function renderApp(): HTMLElement {
  // 每个用例用一个新的容器：Preact 把渲染状态挂在父节点上，
  // 复用 document.body 再手动清空会让它 diff 到已经脱落的旧节点。
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(<App />, container);
  return container;
}

const anonymous = (url: string) => {
  if (url.startsWith("/api/me")) return respond(401, { error: { code: "unauthorized", message: "还没有身份" } });
  if (url.startsWith("/api/games")) return respond(200, { games: [] });
  return respond(404, { error: { code: "not_found", message: "" } });
};

describe("App", () => {
  beforeEach(() => {
  installFakeSocket();
});

afterEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    vi.unstubAllGlobals();
    visit("/");
  });

  // #60：从分享链接 /g/X 落地后切回首页，标题要换回应用名，而不是停在那一局。
  it("路由切换时重设 document.title", async () => {
    const game = {
      game: {
        id: "g1",
        short_code: "ABC123",
        starts_at: 1791986400,
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
      me: { role: "none" },
    };
    stubFetch((url) => (url.startsWith("/api/games/ABC123") ? respond(200, game) : anonymous(url)));
    document.title = "掼蛋局 · 服务端注入的标题";
    visit("/g/ABC123");

    const container = renderApp();
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    await vi.waitFor(() => expect(document.title).toMatch(/^掼蛋局 · .* · Pacific Place$/));

    window.history.pushState(null, "", "/");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await vi.waitFor(() => expect(document.title).toBe("随时掼掼蛋"));
  });

  it("首页把牌局列出来，带上座位圆点和「差 N 人」", async () => {
    stubFetch((url) => {
      if (url.startsWith("/api/games")) {
        return respond(200, {
          games: [
            {
              short_code: "8K2QXM",
              starts_at: 1791986400,
              area: "SCBD",
              venue: "Pacific Place",
              kind: "classic",
              status: "open",
              seat_list: [
                { seat_no: 1, user: { id: "u1", nickname: "老王" } },
                { seat_no: 2, user: { id: "u2", nickname: "Lily" } },
                { seat_no: 3, user: { id: "u3", nickname: "阿明" } },
              ],
              seats: 3,
              missing: 1,
              host: { id: "u1", nickname: "老王" },
            },
          ],
        });
      }
      return anonymous(url);
    });

    const container = renderApp();
    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));

    expect(container.textContent).toContain("差 1 人");
    expect(container.textContent).toContain("1 桌在等人");
    // 座位圆点显示称呼首字；空位是虚线的。
    expect(container.textContent).toContain("老");
    expect(container.textContent).toContain("L");
    expect(container.querySelectorAll(".dots i.blank").length).toBe(1);
  });

  it("满员的局显示「满 · 4/4」", async () => {
    stubFetch((url) => {
      if (url.startsWith("/api/games")) {
        return respond(200, {
          games: [
            {
              short_code: "AAAAAA",
              starts_at: 1791986400,
              area: "PIK",
              venue: "PIK Avenue",
              kind: "classic",
              status: "full",
              seat_list: [1, 2, 3, 4].map((n) => ({ seat_no: n, user: { id: `u${n}`, nickname: `牌友${n}` } })),
              seats: 4,
              missing: 0,
              host: { id: "u1", nickname: "老王" },
            },
          ],
        });
      }
      return anonymous(url);
    });

    const container = renderApp();
    await vi.waitFor(() => expect(container.textContent).toContain("PIK Avenue"));
    expect(container.textContent).toContain("满 · 4/4");
  });

  // 自动兑换是一条 login CSRF 的入口，所以旁边必须有一个立刻退出的出口（#36）。
  it("带 claim 的链接自动兑换后，显示「已认回身份」和「不是我」", async () => {
    visit("/?claim=abc123");
    stubFetch((url) => {
      if (url === "/api/claim/redeem") return respond(200, { user: { id: "u1", nickname: "老王" } });
      if (url.startsWith("/api/me")) {
        return respond(200, { user: { id: "u1", nickname: "老王" }, device_id: "d1", devices: [] });
      }
      if (url.startsWith("/api/games")) return respond(200, { games: [] });
      return respond(404, { error: { code: "not_found", message: "" } });
    });

    const container = renderApp();
    await vi.waitFor(() => expect(container.textContent).toContain("已认回身份"));
    expect(container.textContent).toContain("老王");
    expect(container.textContent).toContain("不是我");
  });

  // 切换不可逆，确认屏要说清楚原身份会从这台设备上退出（#36 第 2 条）。
  it("已有身份时兑换别人的链接：先出确认屏，并说明原身份会退出", async () => {
    visit("/?claim=abc123");
    stubFetch((url) => {
      if (url === "/api/claim/redeem") {
        return respond(409, { error: { code: "claim_conflict", message: "" }, other_nickname: "小李" });
      }
      if (url.startsWith("/api/me")) {
        return respond(200, { user: { id: "u2", nickname: "我自己" }, device_id: "d2", devices: [] });
      }
      if (url.startsWith("/api/games")) return respond(200, { games: [] });
      return respond(404, { error: { code: "not_found", message: "" } });
    });

    const container = renderApp();
    await vi.waitFor(() => expect(container.textContent).toContain("这个链接属于 小李"));

    expect(container.textContent).toContain("保持当前");
    expect(container.textContent).toContain("切换");
    // 文案要点明原身份的下场。
    expect(container.textContent).toContain("只能通过认领链接或认领码找回");
  });
});
