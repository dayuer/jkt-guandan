// 「我的」页的刷新时序（auditor-230 的两条）：只测数据状态，不画画布。
import { beforeEach, describe, expect, it, vi } from "vitest";

// 构建期常量（build.mjs 的 define），api.ts 加载时就要用；home.ts → realtime.ts 加载时会注册 wx.onShow 等，
// 这里给一个什么都不做的 wx（只测数据状态，不碰真的微信接口）。
vi.hoisted(() => {
  (globalThis as any).__API_BASE__ = "http://test.invalid";
  (globalThis as any).wx = new Proxy({}, { get: () => () => undefined });
});

const api = vi.hoisted(() => ({
  listMyGames: vi.fn(),
  listMyHistory: vi.fn(),
  user: { id: "me", nickname: "我" } as { id: string; nickname: string } | null,
}));
vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  listMyGames: api.listMyGames,
  listMyHistory: api.listMyHistory,
}));
vi.mock("../lib/session", () => ({
  currentUser: () => api.user,
  signOutHere: () => {},
  updateNickname: () => {},
}));
vi.mock("./sheets", () => ({ linkSheet: vi.fn(), requireIdentity: vi.fn() }));

import { ApiError, type HistoryGame } from "../lib/api";
import { Me } from "./me";

const game = (code: string): HistoryGame => ({
  short_code: code, starts_at: 0, area: "SCBD", venue: "PP", kind: "classic", status: "done",
  seat_list: [], seats: 4, missing: 0, host: { id: "h", nickname: "局头" },
});
const flush = () => new Promise((r) => setTimeout(r, 0));
/** 手动放行的 Promise：模拟请求还在路上。 */
function deferred<T>() {
  let resolve!: (v: T) => void, reject!: (e: unknown) => void;
  const p = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { p, resolve, reject };
}

const app = { redraw: vi.fn(), toast: vi.fn() } as any;
const state = (m: Me) => m as any;

beforeEach(() => {
  api.user = { id: "me", nickname: "我" };
  api.listMyGames.mockReset().mockResolvedValue([]);
  api.listMyHistory.mockReset();
});

describe("Me 刷新", () => {
  it("凭证失效（401）时历史和翻页状态一起清掉，不留「看更早的」", async () => {
    const m = new Me(app, {} as any);
    api.listMyHistory.mockResolvedValueOnce({ games: [game("A")], has_more: true });
    m.onEnter(); await flush();
    expect(state(m).hasMore).toBe(true);

    api.listMyGames.mockRejectedValueOnce(new ApiError(401, "unauthorized", "还没有身份"));
    api.listMyHistory.mockRejectedValueOnce(new ApiError(401, "unauthorized", "还没有身份"));
    m.onResume(); await flush();
    expect(state(m).history).toEqual([]);
    expect(state(m).hasMore).toBe(false);
    expect(state(m).cursor).toBeUndefined();
  });

  it("「看更早的」读到一半页面刷新了：旧的下一页丢掉，不拼到新列表后面", async () => {
    const m = new Me(app, {} as any);
    api.listMyHistory.mockResolvedValueOnce({ games: [game("A")], has_more: true });
    m.onEnter(); await flush();

    const slow = deferred<{ games: HistoryGame[]; has_more: boolean }>();
    api.listMyHistory.mockReturnValueOnce(slow.p);
    const loading = state(m).more();

    api.listMyHistory.mockResolvedValueOnce({ games: [game("N")], has_more: false });
    m.onResume(); await flush();

    slow.resolve({ games: [game("OLD")], has_more: true });
    await loading;
    expect(state(m).history.map((g: HistoryGame) => g.short_code)).toEqual(["N"]);
    expect(state(m).hasMore).toBe(false);
    expect(state(m).loadingMore).toBe(false);
  });

  it("刷新还没回来时点「看更早的」：刷新先回、这一页后回，不能拼出漏掉一段的列表", async () => {
    const m = new Me(app, {} as any);
    // 已经翻了两页：A B | C D，游标在 D
    api.listMyHistory.mockResolvedValueOnce({ games: [game("A"), game("B")], has_more: true });
    m.onEnter(); await flush();
    api.listMyHistory.mockResolvedValueOnce({ games: [game("C"), game("D")], has_more: true });
    await state(m).more();
    expect(state(m).history.map((g: HistoryGame) => g.short_code)).toEqual(["A", "B", "C", "D"]);

    // 回到页面触发刷新（还没回来），这时点「看更早的」，还拿着旧游标 D
    const refreshed = deferred<{ games: HistoryGame[]; has_more: boolean }>();
    const older = deferred<{ games: HistoryGame[]; has_more: boolean }>();
    api.listMyHistory.mockReturnValueOnce(refreshed.p).mockReturnValueOnce(older.p);
    m.onResume();
    const loading = state(m).more();
    expect(api.listMyHistory).toHaveBeenLastCalledWith(game("D"));

    refreshed.resolve({ games: [game("A"), game("B")], has_more: true }); await flush();
    older.resolve({ games: [game("E"), game("F")], has_more: false }); await loading;
    expect(state(m).history.map((g: HistoryGame) => g.short_code)).toEqual(["A", "B"]);
    expect(state(m).hasMore).toBe(true);
  });

  it("两次刷新交错：先发的后回来，不能盖掉后发的结果", async () => {
    const m = new Me(app, {} as any);
    const first = deferred<{ games: HistoryGame[]; has_more: boolean }>();
    api.listMyHistory.mockReturnValueOnce(first.p).mockResolvedValueOnce({ games: [game("NEW")], has_more: false });
    m.onEnter();
    m.onResume(); await flush();
    first.resolve({ games: [game("OLD")], has_more: true }); await flush();
    expect(state(m).history.map((g: HistoryGame) => g.short_code)).toEqual(["NEW"]);
    expect(state(m).hasMore).toBe(false);
  });

  it("旧的一页回来时不能把新一轮「看更早的」的加载状态复位", async () => {
    const m = new Me(app, {} as any);
    api.listMyHistory.mockResolvedValueOnce({ games: [game("A")], has_more: true });
    m.onEnter(); await flush();

    const oldPage = deferred<{ games: HistoryGame[]; has_more: boolean }>();
    api.listMyHistory.mockReturnValueOnce(oldPage.p);
    const oldLoading = state(m).more();

    api.listMyHistory.mockResolvedValueOnce({ games: [game("N")], has_more: true });
    m.onResume(); await flush();

    const newPage = deferred<{ games: HistoryGame[]; has_more: boolean }>();
    api.listMyHistory.mockReturnValueOnce(newPage.p);
    const newLoading = state(m).more();
    expect(state(m).loadingMore).toBe(true);

    oldPage.resolve({ games: [game("OLD")], has_more: true }); await oldLoading;
    expect(state(m).loadingMore).toBe(true); // 新一轮还在读，不能被旧的复位（复位了用户能再点一次，发重复请求）

    newPage.resolve({ games: [game("O")], has_more: false }); await newLoading;
    expect(state(m).loadingMore).toBe(false);
    expect(state(m).history.map((g: HistoryGame) => g.short_code)).toEqual(["N", "O"]);
  });
});
