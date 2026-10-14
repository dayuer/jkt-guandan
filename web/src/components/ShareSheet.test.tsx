import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { GameDetail } from "../api";
import { ShareSheet } from "./ShareSheet";

/** C1 · 分享面板（设计稿 04、docs/h5-pwa-plan.md §5）。 */

function detail(over: Partial<GameDetail["game"]> = {}, missing = 2): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "8K2QXM",
      starts_at: Math.floor(Date.UTC(2026, 9, 14, 12, 30, 0) / 1000),
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      status: "open",
      version: 3,
      capacity: 4,
      ...over,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [],
    waitlist: [],
    missing,
    me: { role: "seat", seat_no: 1 },
  };
}

function setup(d: GameDetail = detail(), userAgent = "Mozilla/5.0 (iPhone)") {
  vi.stubGlobal("navigator", {
    userAgent,
    clipboard: { writeText: vi.fn(async () => {}) },
  });

  const container = document.createElement("div");
  document.body.appendChild(container);
  const toasts: string[] = [];
  let closed = 0;
  render(<ShareSheet detail={d} toast={(t) => toasts.push(t)} onClose={() => closed++} />, container);
  return { container, toasts, getClosed: () => closed };
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("分享面板（04 / C1）", () => {
  it("海报带上牌局版本号：座位一变，图就跟着变", () => {
    const { container } = setup(detail({ version: 7 }));
    const img = container.querySelector("img.poster") as HTMLImageElement;

    // 只断言尾巴：origin 在 jsdom 里是 http://localhost:3000。
    expect(img.getAttribute("src")).toContain("/g/8K2QXM/poster.png?v=7");
  });

  it("话术预览和四个出口都在", () => {
    const { container } = setup();

    expect(container.querySelector(".script p")?.textContent).toContain("差 2 人");
    expect(container.querySelector(".script p")?.textContent).toContain("/g/8K2QXM");

    // 图标是 span，文字和它拼在一起，所以用「包含」而不是「等于」。
    const outlets = [...container.querySelectorAll(".outlet")].map((b) => b.textContent ?? "");
    expect(outlets.some((t) => t.includes("保存海报"))).toBe(true);
    expect(outlets.some((t) => t.includes("复制话术"))).toBe(true);
    expect(outlets.some((t) => t.includes("WhatsApp"))).toBe(true);
  });

  it("复制话术之后按钮变成「已复制 ✓」", async () => {
    const { container, toasts } = setup();

    const copy = [...container.querySelectorAll(".script button")][0] as HTMLButtonElement;
    copy.click();

    await vi.waitFor(() => expect(copy.textContent).toBe("已复制 ✓"));
    expect(toasts.join()).toContain("话术已复制");
  });

  it("WhatsApp 出口是 wa.me 链接，整句话都编码进去了", () => {
    const { container } = setup();
    const wa = [...container.querySelectorAll("a.outlet")].find((a) =>
      a.textContent?.includes("WhatsApp"),
    ) as HTMLAnchorElement;

    expect(wa.getAttribute("href")).toContain("https://wa.me/?text=");
    expect(wa.getAttribute("href")).toContain(encodeURIComponent("差 2 人"));
    // 外链要新开窗口，别把当前页顶掉。
    expect(wa.getAttribute("target")).toBe("_blank");
  });

  it("微信里点「保存海报」是提示长按，不是下载", () => {
    const { container, toasts } = setup(detail(), "Mozilla/5.0 MicroMessenger/8.0");
    const save = [...container.querySelectorAll(".outlet")].find((b) =>
      b.textContent?.includes("保存海报"),
    ) as HTMLButtonElement;

    save.click();
    expect(toasts.join()).toContain("长按上面的海报");
  });

  it("系统分享不支持时，不显示「更多」那一个出口", () => {
    const { container } = setup();
    const outlets = [...container.querySelectorAll(".outlet")].map((b) => b.textContent ?? "");
    expect(outlets.some((t) => t.includes("更多"))).toBe(false);
  });

  it("海报加载失败时换成占位，并且禁用「保存海报」——但话术照样能复制", async () => {
    const { container } = setup();
    const img = container.querySelector("img.poster") as HTMLImageElement;
    img.dispatchEvent(new Event("error"));

    await vi.waitFor(() => expect(container.querySelector(".poster.placeholder")).not.toBeNull());
    const save = [...container.querySelectorAll(".outlet")].find((b) =>
      b.textContent?.includes("保存海报"),
    ) as HTMLButtonElement;
    expect(save.disabled).toBe(true);

    const copy = [...container.querySelectorAll(".script button")][0] as HTMLButtonElement;
    expect(copy.disabled).toBe(false);
  });

  it("「取消」关掉面板", () => {
    const { container, getClosed } = setup();
    (container.querySelector(".textlink") as HTMLButtonElement).click();
    expect(getClosed()).toBe(1);
  });
});
