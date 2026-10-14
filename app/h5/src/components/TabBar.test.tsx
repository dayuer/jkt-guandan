import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TabBar, type TabKey } from "./TabBar";

/** 底部 Tab 栏（#107）：五个 tab 都能点，当前的会高亮。 */

function setup(active: TabKey) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const tabs: TabKey[] = [];
  render(<TabBar active={active} onTab={(t) => tabs.push(t)} />, container);
  return { container, tabs };
}

const buttonFor = (container: HTMLElement, label: string) =>
  [...container.querySelectorAll(".tab")].find((b) => b.textContent?.includes(label)) as HTMLButtonElement;

afterEach(() => {
  document.body.innerHTML = "";
  document.documentElement.style.removeProperty("--toast-lift");
  document.documentElement.style.removeProperty("--tabbar-height");
  vi.restoreAllMocks();
});

describe("TabBar", () => {
  it("五个 tab 都在，顺序是设计稿 A2 的顺序", () => {
    const { container } = setup("games");
    const labels = [...container.querySelectorAll(".tab")].map((b) => b.textContent?.trim());
    expect(labels).toEqual(["♠牌局", "♣学牌", "蛋约局", "♦牌友", "♥我的"]);
  });

  it("当前那个高亮，别的没有", () => {
    const { container } = setup("me");
    const on = [...container.querySelectorAll(".tab.on")].map((b) => b.textContent?.trim());
    expect(on).toEqual(["♥我的"]);
    expect(buttonFor(container, "我的").getAttribute("aria-current")).toBe("page");
    expect(buttonFor(container, "牌局").getAttribute("aria-current")).toBeNull();
  });

  it("每个 tab 点了都会把 key 报到上层（导航由 App 决定）", () => {
    const { container, tabs } = setup("games");
    for (const label of ["学牌", "约局", "牌友", "我的", "牌局"]) {
      buttonFor(container, label).click();
    }
    expect(tabs).toEqual(["learn", "create", "friends", "me", "games"]);
  });

  it("报自己的高度（--tabbar-height），页面据此留白", async () => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
      (() => ({ height: 76, width: 375, top: 0, left: 0, right: 375, bottom: 76, x: 0, y: 0, toJSON: () => ({}) })) as unknown as () => DOMRect,
    );
    setup("games");
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--tabbar-height")).toBe("76px"),
    );
    // toast 的抬升由页面自己的底栏（操作栏 / 蛋蛋输入框）报，Tab 栏不掺和——
    // 蛋蛋那一页两条底栏都要让开，加法写在 CSS 里更清楚。
    expect(document.documentElement.style.getPropertyValue("--toast-lift")).toBe("");
  });

  it("卸载时把高度还回去，别让下一个页面顶着一个旧值", async () => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
      (() => ({ height: 76, width: 375, top: 0, left: 0, right: 375, bottom: 76, x: 0, y: 0, toJSON: () => ({}) })) as unknown as () => DOMRect,
    );
    const { container } = setup("games");
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--tabbar-height")).toBe("76px"),
    );
    render(null, container);
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--tabbar-height")).toBe(""),
    );
  });
});
