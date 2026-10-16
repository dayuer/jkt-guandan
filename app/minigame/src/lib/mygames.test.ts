import { describe, expect, it } from "vitest";
import type { GameSummary, HistoryGame } from "./api";
import { afterHide, historyStatus, roleBadge, scoreLine, upcomingStatus } from "./mygames";

const game = (o: Partial<GameSummary> = {}): GameSummary => ({
  short_code: "ABC123", starts_at: 0, area: "SCBD", venue: "Pacific Place", kind: "classic",
  status: "open", seat_list: [], seats: 2, missing: 2, host: { id: "host", nickname: "局头" },
  my_role: "none", ...o,
});

describe("roleBadge", () => {
  it("我开的优先于已报名（局头也坐在座位上）", () => {
    expect(roleBadge(game({ host: { id: "me", nickname: "我" }, my_role: "seated" }), "me")?.label).toBe("我开的");
  });
  it("已报名、候补第几位", () => {
    expect(roleBadge(game({ my_role: "seated" }), "me")?.label).toBe("已报名");
    expect(roleBadge(game({ my_role: "waitlist", waitlist_pos: 2 }), "me")?.label).toBe("候补第 2 位");
    expect(roleBadge(game({ my_role: "waitlist" }), "me")?.label).toBe("候补中");
  });
  it("和我无关、没身份时不显示", () => {
    expect(roleBadge(game(), "me")).toBeNull();
    expect(roleBadge(game({ host: { id: "", nickname: "" } }), undefined)).toBeNull();
  });
});

describe("upcomingStatus", () => {
  it("差几人带上已坐几个", () => {
    expect(upcomingStatus(game({ seats: 3, missing: 1 }))).toEqual({ label: "差 1 人 · 3/4", tone: "red" });
    expect(upcomingStatus(game({ seats: 1, missing: 3 })).label).toBe("差 3 人 · 1/4");
  });
  it("坐满就是已成局", () => {
    expect(upcomingStatus(game({ status: "full", seats: 4, missing: 0 })).label).toBe("已成局 · 4/4");
  });
});

describe("historyStatus", () => {
  it("取消、打完、开了局还没收尾", () => {
    expect(historyStatus(game({ status: "cancelled" })).label).toBe("已取消");
    expect(historyStatus(game({ status: "done" })).label).toBe("已打完");
    expect(historyStatus(game({ status: "full" })).label).toBe("已开局");
  });
});

describe("scoreLine", () => {
  const brief = { kind: "classic", rounds: 6, red_level: 11, black_level: 9, red_double_ups: 1, black_double_ups: 0, red_wins: 4, black_wins: 2 };
  const h = (score?: HistoryGame["score"]): HistoryGame => ({ ...game(), score });
  it("没记分就不显示", () => {
    expect(scoreLine(h())).toBe("");
    expect(scoreLine(h({ ...brief, rounds: 0 }))).toBe("");
  });
  it("升级局显示两队的级", () => {
    expect(scoreLine(h(brief))).toBe("红 J : 黑 9 · 6 局");
  });
  it("过了 A 说哪队赢了", () => {
    expect(scoreLine(h({ ...brief, winner: "black" }))).toBe("黑方过 A · 6 局");
  });
  it("不升级局说各赢几局", () => {
    expect(scoreLine(h({ ...brief, kind: "no-level" }))).toBe("红 4 : 黑 2 · 6 局");
  });
});

describe("afterHide", () => {
  const h = (code: string): HistoryGame => ({ ...game({ short_code: code }) });
  it("拿掉删的那一条，别的不动", () => {
    expect(afterHide([h("A"), h("B")], "A", true)).toEqual({ list: [h("B")], loadMore: false });
  });
  it("删光了、还有更早的：接着读下一页", () => {
    expect(afterHide([h("A")], "A", true)).toEqual({ list: [], loadMore: true });
  });
  it("删光了、没有更早的：不用读", () => {
    expect(afterHide([h("A")], "A", false)).toEqual({ list: [], loadMore: false });
  });
});
