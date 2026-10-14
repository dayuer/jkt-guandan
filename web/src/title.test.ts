import { describe, expect, it } from "vitest";
import type { GameDetail } from "./api";
import { gameTitle } from "./title";

// 2026-10-13 周二 19:30 WIB = 12:30 UTC
const STARTS = Date.UTC(2026, 9, 13, 12, 30) / 1000;
const NOW = Date.UTC(2026, 9, 13, 3, 0) / 1000; // 当天上午 10:00 WIB

function detail(startsAt: number, status = "open"): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "ABC123",
      starts_at: startsAt,
      area: "SCBD",
      venue: "Pacific Place · 鼎泰丰包间",
      kind: "classic",
      status,
      version: 1,
      capacity: 4,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [],
    waitlist: [],
    missing: 4,
    me: { role: "none" },
  } as GameDetail;
}

describe("gameTitle（和服务端 OGFor 一致）", () => {
  it("今天的局", () => {
    // 服务端 og_test 的期望值就是这一行。
    expect(gameTitle(detail(STARTS), NOW)).toBe("掼蛋局 · 今天 10/13 周二 19:30 · Pacific Place · 鼎泰丰包间");
  });

  it("明天、后天带前缀，再往后只有日期", () => {
    expect(gameTitle(detail(STARTS + 86400), NOW)).toBe("掼蛋局 · 明天 10/14 周三 19:30 · Pacific Place · 鼎泰丰包间");
    expect(gameTitle(detail(STARTS + 2 * 86400), NOW)).toContain("后天 10/15 周四");
    expect(gameTitle(detail(STARTS + 3 * 86400), NOW)).toContain("掼蛋局 · 10/16 周五 19:30");
  });

  it("月、日不补零，按雅加达时间算日子", () => {
    // 2027-01-05 00:30 WIB = 01-04 17:30 UTC：UTC 还是 4 号，雅加达已经是 5 号。
    const t = Date.UTC(2027, 0, 4, 17, 30) / 1000;
    expect(gameTitle(detail(t), NOW)).toBe("掼蛋局 · 1/5 周二 00:30 · Pacific Place · 鼎泰丰包间");
  });

  it("取消的局加「【已取消】」", () => {
    expect(gameTitle(detail(STARTS, "cancelled"), NOW)).toMatch(/^【已取消】掼蛋局 · 今天/);
  });
});
