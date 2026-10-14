import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, createGame, subscribeRetry, updateGame } from "./api";

function respond(status: number, body: unknown, headers: Record<string, string> = {}) {
  const text = JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (k: string) => headers[k] ?? null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

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
  seats: [],
  waitlist: [],
  missing: 4,
  me: { role: "none" },
};

afterEach(() => vi.unstubAllGlobals());

describe("503 自动重试（原型 X4）", () => {
  // 写锁等待超时或过载会返回 503（§12.3）。这时按 Retry-After 等一会儿重试一次，
  // 请求完全一样——包括幂等键——所以不会开出两个局（§11.4）。
  it("按 Retry-After 等一会儿，重试一次，请求逐字相同", async () => {
    const calls: RequestInit[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init: RequestInit) => {
        calls.push(init);
        if (calls.length === 1) {
          return respond(503, { error: { code: "busy", message: "" } }, { "Retry-After": "0.001" });
        }
        return respond(200, game);
      }),
    );

    const states: boolean[] = [];
    const unsubscribe = subscribeRetry((v) => states.push(v));

    const created = await createGame(
      { starts_at: 1791986400, area: "SCBD", venue: "Pacific Place", kind: "classic" },
      "key-1",
    );
    unsubscribe();

    expect(created.game.short_code).toBe("ABC123");
    expect(calls.length).toBe(2);
    expect(calls[1].body).toBe(calls[0].body);
    expect((calls[0].headers as Record<string, string>)["Idempotency-Key"]).toBe("key-1");
    expect((calls[1].headers as Record<string, string>)["Idempotency-Key"]).toBe("key-1");
    // 页面上按钮要能显示「人有点多，正在重试…」。
    expect(states).toContain(true);
    expect(states[states.length - 1]).toBe(false);
  });

  it("只重试一次：第二次还是 503 就把错误抛出去", async () => {
    let n = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        n++;
        return respond(503, { error: { code: "busy", message: "繁忙" } }, { "Retry-After": "0.001" });
      }),
    );

    await expect(
      updateGame("ABC123", { venue: "X" }, 1),
    ).rejects.toBeInstanceOf(ApiError);
    expect(n).toBe(2);
  });

  it("GET 不重试（读接口没有副作用，交给页面自己刷新）", async () => {
    let n = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        n++;
        return respond(503, { error: { code: "overloaded", message: "过载" } }, { "Retry-After": "0.001" });
      }),
    );
    const { listGames } = await import("./api");
    await expect(listGames()).rejects.toBeInstanceOf(ApiError);
    expect(n).toBe(1);
  });
});
