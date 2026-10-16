import { describe, expect, it, vi } from "vitest";

// 构建期常量（build.mjs 的 define），api.ts 加载时就要用；小游戏在 node 里没有 wx。
vi.hoisted(() => {
  (globalThis as any).__API_BASE__ = "https://mini.test";
  (globalThis as any).wx = new Proxy({}, { get: () => () => undefined });
});

const { gameShareText } = await import("./share");

// WIB 10 月 14 日 12:00（= 05:00 UTC）当「现在」；这一局今晚 19:30。
const NOW = Date.UTC(2026, 9, 14, 5, 0) / 1000;
const TONIGHT = Date.UTC(2026, 9, 14, 12, 30) / 1000; // WIB 19:30
const TOMORROW = TONIGHT + 86400;

function game(over: Partial<{ short_code: string; venue: string; status: string; starts_at: number }> = {}, missing = 2) {
  return {
    game: { short_code: "8K2QXM", venue: "Pacific Place", status: "open", starts_at: TONIGHT, ...over },
    missing,
  };
}

describe("分享话术（小游戏：lib/fmt 拼时间 + API_BASE 拼链接，#253 P9）", () => {
  it("缺人时说差几人，时间是「今天 19:30」，链接指向 API_BASE 的站点", () => {
    expect(gameShareText(game(), NOW)).toBe(
      "今天 19:30 Pacific Place 差 2 人，来的点 👉 https://mini.test/g/8K2QXM",
    );
  });

  it("明天的局说「明天」，不写成日期", () => {
    expect(gameShareText(game({ starts_at: TOMORROW }), NOW)).toBe(
      "明天 19:30 Pacific Place 差 2 人，来的点 👉 https://mini.test/g/8K2QXM",
    );
  });

  it("满了不说「差 0 人」，说可以排候补", () => {
    const text = gameShareText(game({ status: "full" }, 0), NOW);
    expect(text).toBe("今天 19:30 Pacific Place 已经满了，想打可以排候补，点这里 👉 https://mini.test/g/8K2QXM");
    expect(text).not.toContain("差 0 人");
  });

  it("取消了的局不装作还能来", () => {
    expect(gameShareText(game({ status: "cancelled" }, 4), NOW)).toBe(
      "今天 19:30 Pacific Place 这一局取消了，下次再约 👉 https://mini.test/g/8K2QXM",
    );
  });
});
