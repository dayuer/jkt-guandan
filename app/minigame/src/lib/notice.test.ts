import { describe, expect, it } from "vitest";
import { snapshotKey } from "../../../h5/src/status";
import type { GameDetail, GameSummary } from "./api";
import { detailView, dismissNotice, fallbackKey, observeNotices, summaryView } from "./notice";

const ME = "u-me", HOST = "u-host", CODE = "ABC123";
const T0 = Math.floor(Date.now() / 1000); // 快照按开局时间过期清理，用现在的时间

function memStore() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m };
}

function detail(o: {
  role?: "seat" | "waitlist" | "none"; startsAt?: number; venue?: string; host?: string;
  rescheduledAt?: number; joinedAt?: number; promotedAt?: number; becameHostAt?: number;
} = {}): GameDetail {
  return {
    game: { short_code: CODE, starts_at: o.startsAt ?? T0 + 7200, venue: o.venue ?? "Pacific Place", rescheduled_at: o.rescheduledAt },
    host: { id: o.host ?? HOST, nickname: "局头" },
    me: { role: o.role ?? "seat", joined_at: o.joinedAt, promoted_at: o.promotedAt, became_host_at: o.becameHostAt },
    seats: [], waitlist: [], missing: 0,
  } as unknown as GameDetail;
}

describe("状态提示的换设备兜底（#160）", () => {
  it("无快照 + 加入之后改过时间 → 改时间横幅（说现在的时间和地点）", () => {
    const s = memStore();
    const d = detail({ joinedAt: T0, rescheduledAt: T0 + 60, startsAt: T0 + 9000, venue: "Grand Indonesia" });
    expect(observeNotices(detailView(d), ME, s)).toEqual([
      { kind: "rescheduled", code: CODE, startsAt: T0 + 9000, venue: "Grand Indonesia", timeChanged: false, venueChanged: false },
    ]);
    // 没关掉之前不写快照：再刷新还在。
    expect(s.m.has(snapshotKey(ME))).toBe(false);
    expect(observeNotices(detailView(d), ME, s)).toHaveLength(1);
  });

  it("无快照 + 加入之后没改过 → 不提示，并写下快照", () => {
    const s = memStore();
    expect(observeNotices(detailView(detail({ joinedAt: T0 + 60, rescheduledAt: T0 })), ME, s)).toEqual([]);
    expect(observeNotices(detailView(detail({ joinedAt: T0 })), ME, s)).toEqual([]);
    expect(JSON.parse(s.m.get(snapshotKey(ME))!)).toHaveProperty(CODE);
  });

  it("无快照 + 补过位 → 「轮到你了」", () => {
    const s = memStore();
    const d = detail({ joinedAt: T0, promotedAt: T0 + 600 });
    expect(observeNotices(detailView(d), ME, s)).toEqual([{ kind: "promoted", code: CODE }]);
  });

  it("关掉之后不再出现（详情）", () => {
    const s = memStore();
    const d = detail({ joinedAt: T0, rescheduledAt: T0 + 60 });
    expect(observeNotices(detailView(d), ME, s)).toHaveLength(1);
    dismissNotice(detailView(d), ME, "rescheduled", s, T0);
    expect(observeNotices(detailView(d), ME, s)).toEqual([]);

    const p = detail({ joinedAt: T0, promotedAt: T0 + 600 });
    const s2 = memStore();
    expect(observeNotices(detailView(p), ME, s2)).toHaveLength(1);
    dismissNotice(detailView(p), ME, "promoted", s2, T0);
    expect(observeNotices(detailView(p), ME, s2)).toEqual([]);
  });

  it("两条同时出现：关掉一条，另一条还在；都关掉后不再出现", () => {
    const s = memStore();
    const d = detail({ joinedAt: T0, rescheduledAt: T0 + 60, promotedAt: T0 + 600 });
    expect(observeNotices(detailView(d), ME, s).map((n) => n.kind)).toEqual(["promoted", "rescheduled"]);
    dismissNotice(detailView(d), ME, "promoted", s, T0);
    expect(observeNotices(detailView(d), ME, s).map((n) => n.kind)).toEqual(["rescheduled"]);
    dismissNotice(detailView(d), ME, "rescheduled", s, T0);
    expect(observeNotices(detailView(d), ME, s)).toEqual([]);
    expect(JSON.parse(s.m.get(fallbackKey(ME))!)).toEqual({}); // 回到快照那条路，兜底记录清掉
  });

  it("关掉之后局头又改了一次 → 快照那条路照常提示", () => {
    const s = memStore();
    const d = detail({ joinedAt: T0, rescheduledAt: T0 + 60 });
    dismissNotice(detailView(d), ME, "rescheduled", s, T0);
    const again = detail({ joinedAt: T0, rescheduledAt: T0 + 120, startsAt: T0 + 10800 });
    expect(observeNotices(detailView(again), ME, s)).toMatchObject([{ kind: "rescheduled", timeChanged: true, venueChanged: false }]);
  });

  it("局头自己、不在局里的人、候补没补上的人：不出兜底提示", () => {
    expect(observeNotices(detailView(detail({ host: ME, joinedAt: T0, rescheduledAt: T0 + 60 })), ME, memStore())).toEqual([]);
    expect(observeNotices(detailView(detail({ role: "none", rescheduledAt: T0 + 60 })), ME, memStore())).toEqual([]);
    expect(observeNotices(detailView(detail({ role: "waitlist", joinedAt: T0 })), ME, memStore())).toEqual([]);
  });

  it("有快照时行为不变：和快照比，不看服务端字段", () => {
    const s = memStore();
    s.setItem(snapshotKey(ME), JSON.stringify({ [CODE]: { startsAt: T0 + 7200, venue: "Pacific Place", role: "seat", hostID: HOST, hostName: "局头" } }));
    // 服务端说改过（晚于加入），但快照里的时间地点和现在一样 → 不提示。
    expect(observeNotices(detailView(detail({ joinedAt: T0, rescheduledAt: T0 + 60, promotedAt: T0 + 30 })), ME, s)).toEqual([]);
  });

  it("列表项也一样：my_joined_at / my_promoted_at / rescheduled_at", () => {
    const g = {
      short_code: CODE, starts_at: T0 + 7200, venue: "Pacific Place", host: { id: HOST, nickname: "局头" },
      my_role: "seated", rescheduled_at: T0 + 60, my_joined_at: T0, my_promoted_at: T0 + 30,
    } as unknown as GameSummary;
    const s = memStore();
    expect(observeNotices(summaryView(g), ME, s).map((n) => n.kind)).toEqual(["promoted", "rescheduled"]);
    dismissNotice(summaryView(g), ME, "rescheduled", s, T0);
    dismissNotice(summaryView(g), ME, "promoted", s, T0);
    expect(observeNotices(summaryView(g), ME, s)).toEqual([]);
  });
});

// #189：K2「你现在是局头」也要能用服务端字段兜底（换设备、清了存储后照样能看到），
// 顺带处理 #185 复审意见 3：**接手**的局头也要能看到上一位局头改过的改时间横幅。
describe("局头转交的换设备兜底（#189）", () => {
  it("无快照 + 我接手当上局头 → K2 横幅", () => {
    const s = memStore();
    const d = detail({ host: ME, joinedAt: T0, becameHostAt: T0 + 300 });
    expect(observeNotices(detailView(d), ME, s)).toEqual([
      { kind: "host", code: CODE, formerHost: "" },
    ]);
    // 没关掉之前不写快照：再刷新还在。
    expect(observeNotices(detailView(d), ME, s)).toHaveLength(1);
  });

  it("开局的人本人（没有 became_host_at）→ 不弹 K2", () => {
    // 自己开的局，当上局头不是新闻；老规矩：局头自己不提示。
    expect(observeNotices(detailView(detail({ host: ME, joinedAt: T0 })), ME, memStore())).toEqual([]);
  });

  it("关掉 K2 之后不再出现", () => {
    const s = memStore();
    const v = detailView(detail({ host: ME, joinedAt: T0, becameHostAt: T0 + 300 }));
    expect(observeNotices(v, ME, s)).toHaveLength(1);
    dismissNotice(v, ME, "host", s, T0);
    expect(observeNotices(v, ME, s)).toEqual([]);
    // 快照这时写下了，之后走快照那条路，也不会又冒出来。
    expect(s.m.has(snapshotKey(ME))).toBe(true);
  });

  // #185 复审意见 3：补位上来、后来又当上局头的人，换设备后也要能看到改时间横幅。
  it("接手的局头 + 上一位局头改过时间（改在我接手之前）→ 改时间横幅", () => {
    const s = memStore();
    const d = detail({ host: ME, joinedAt: T0, becameHostAt: T0 + 300, rescheduledAt: T0 + 120 });
    expect(observeNotices(detailView(d), ME, s).map((n) => n.kind)).toEqual(["host", "rescheduled"]);
  });

  it("我接手**之后**才改的时间 → 那是我自己改的，不提示", () => {
    const s = memStore();
    const d = detail({ host: ME, joinedAt: T0, becameHostAt: T0 + 300, rescheduledAt: T0 + 600 });
    expect(observeNotices(detailView(d), ME, s).map((n) => n.kind)).toEqual(["host"]);
  });

  it("开局的人本人改的时间 → 还是不提示（老规矩不变）", () => {
    expect(observeNotices(
      detailView(detail({ host: ME, joinedAt: T0, rescheduledAt: T0 + 60 })), ME, memStore(),
    )).toEqual([]);
  });

  it("列表项也一样：my_became_host_at", () => {
    const g = {
      short_code: CODE, starts_at: T0 + 7200, venue: "Pacific Place", host: { id: ME, nickname: "我" },
      my_role: "seated", my_joined_at: T0, my_became_host_at: T0 + 300,
    } as unknown as GameSummary;
    const s = memStore();
    expect(observeNotices(summaryView(g), ME, s).map((n) => n.kind)).toEqual(["host"]);
    dismissNotice(summaryView(g), ME, "host", s, T0);
    expect(observeNotices(summaryView(g), ME, s)).toEqual([]);
  });
});
