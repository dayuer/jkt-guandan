import { afterEach, describe, expect, it, vi } from "vitest";
import type { GameDetail } from "./api";
import {
  canSystemShare,
  copyText,
  posterURL,
  shareText,
  shareURL,
  systemShare,
  systemShareText,
  whatsappURL,
} from "./share";

const ORIGIN = "https://guandan.id";
/** 2026-10-14 12:00 UTC = 雅加达 19:00。 */
const NOW = Math.floor(Date.UTC(2026, 9, 14, 12, 0, 0) / 1000);

function detail(over: Partial<GameDetail["game"]> = {}, seats = 2): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "8K2QXM",
      // 当天 WIB 19:30。
      starts_at: Math.floor(Date.UTC(2026, 9, 14, 12, 30, 0) / 1000),
      area: "SCBD",
      venue: "Pacific Place · 鼎泰丰包间",
      kind: "classic",
      status: "open",
      version: 3,
      capacity: 4,
      ...over,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [],
    waitlist: [],
    missing: 4 - seats,
    me: { role: "seat", seat_no: 1 },
  };
}

describe("话术（§5、设计稿 04）", () => {
  it("缺人时说差几人，并带上链接", () => {
    const text = shareText(detail(), NOW, ORIGIN);
    expect(text).toBe("今天 19:30 Pacific Place · 鼎泰丰包间 差 2 人，来的点 👉 https://guandan.id/g/8K2QXM");
  });

  it("差的人数变了，话术跟着变（完成标志里的这一条）", () => {
    expect(shareText(detail({}, 1), NOW, ORIGIN)).toContain("差 3 人");
    expect(shareText(detail({}, 3), NOW, ORIGIN)).toContain("差 1 人");
  });

  it("满了以后不说「差 0 人」，而是说可以排候补", () => {
    const text = shareText(detail({ status: "full" }, 4), NOW, ORIGIN);
    expect(text).toContain("已经满了");
    expect(text).toContain("排候补");
    expect(text).not.toContain("差 0 人");
  });

  it("明天和更远的日子用不同的说法", () => {
    const tomorrow = detail({ starts_at: Math.floor(Date.UTC(2026, 9, 15, 12, 30, 0) / 1000) });
    expect(shareText(tomorrow, NOW, ORIGIN)).toContain("明天 19:30");

    const later = detail({ starts_at: Math.floor(Date.UTC(2026, 9, 20, 12, 30, 0) / 1000) });
    expect(shareText(later, NOW, ORIGIN)).toContain("10月20日 19:30");
    expect(shareText(later, NOW, ORIGIN)).not.toContain("明天");
  });

  it("已取消和已结束的局不装作还能来", () => {
    expect(shareText(detail({ status: "cancelled" }, 2), NOW, ORIGIN)).toContain("取消了");
    expect(shareText(detail({ status: "done" }, 4), NOW, ORIGIN)).toContain("打完了");
    // 这两种情况下不该出现「来的点」。
    expect(shareText(detail({ status: "done" }, 4), NOW, ORIGIN)).not.toContain("来的点");
  });
});

describe("系统分享的正文", () => {
  it("不带链接——链接走 url 字段，正文里再写一遍目标 App 会显示两次", () => {
    const text = systemShareText(detail(), NOW);
    expect(text).toBe("今天 19:30 Pacific Place · 鼎泰丰包间 差 2 人");
    expect(text).not.toContain("http");
    expect(text).not.toContain("👉");
  });

  it("各种状态下都不带链接，而且句子完整", () => {
    expect(systemShareText(detail({ status: "full" }, 4), NOW)).toContain("已经满了");
    expect(systemShareText(detail({ status: "full" }, 4), NOW)).not.toContain("点这里");
    expect(systemShareText(detail({ status: "cancelled" }, 2), NOW)).toContain("取消了");
    expect(systemShareText(detail({ status: "done" }, 4), NOW)).toContain("打完了");
  });
});

describe("链接", () => {
  it("牌局链接用站点 origin 拼，不用相对路径——要发到群里", () => {
    expect(shareURL("8K2QXM", ORIGIN)).toBe("https://guandan.id/g/8K2QXM");
  });

  it("海报带牌局版本号：座位一变版本就变，缓存不会回上一版", () => {
    expect(posterURL("8K2QXM", 3, ORIGIN)).toBe("https://guandan.id/g/8K2QXM/poster.png?v=3");
    expect(posterURL("8K2QXM", 4, ORIGIN)).not.toBe(posterURL("8K2QXM", 3, ORIGIN));
  });

  it("WhatsApp 链接把整句话编码进去", () => {
    const url = whatsappURL(shareText(detail(), NOW, ORIGIN));
    expect(url.startsWith("https://wa.me/?text=")).toBe(true);
    expect(url).toContain(encodeURIComponent("差 2 人"));
    // 「👉」和链接里的斜杠都必须编码，否则 WhatsApp 只吃掉前半句。
    expect(url).toContain("%F0%9F%91%89");
    expect(url).toContain(encodeURIComponent("https://guandan.id/g/8K2QXM"));
  });
});

describe("系统分享", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    // @ts-expect-error 清掉测试里塞进去的 navigator.share
    delete navigator.share;
  });

  it("不支持时如实说 unsupported", async () => {
    expect(canSystemShare()).toBe(false);
    expect(await systemShare({ title: "t", text: "x", url: "u" })).toBe("unsupported");
  });

  it("用户取消不算失败", async () => {
    const abort = Object.assign(new Error("cancelled"), { name: "AbortError" });
    navigator.share = vi.fn(async () => {
      throw abort;
    });
    expect(canSystemShare()).toBe(true);
    expect(await systemShare({ title: "t", text: "x", url: "u" })).toBe("cancelled");
  });

  it("真出错才算 failed", async () => {
    navigator.share = vi.fn(async () => {
      throw new Error("boom");
    });
    expect(await systemShare({ title: "t", text: "x", url: "u" })).toBe("failed");
  });
});

describe("复制", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("优先用 clipboard API", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { clipboard: { writeText } });

    expect(await copyText("今天 19:30")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("今天 19:30");
  });

  it("微信里没有 clipboard 时退回 execCommand", async () => {
    vi.stubGlobal("navigator", {});
    const exec = vi.fn(() => true);
    // jsdom 默认没有 execCommand，这里补上。
    (document as unknown as { execCommand: unknown }).execCommand = exec;

    expect(await copyText("今天 19:30")).toBe(true);
    expect(exec).toHaveBeenCalledWith("copy");
  });

  it("两条路都不行时返回 false，让页面提示用户手动选", async () => {
    vi.stubGlobal("navigator", {});
    (document as unknown as { execCommand: unknown }).execCommand = vi.fn(() => false);

    expect(await copyText("x")).toBe(false);
  });
});
