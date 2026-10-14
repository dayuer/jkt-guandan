import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useBarHeight } from "./useBarHeight";

/**
 * #75：底部固定条的高度要**量出来**。
 *
 * 详情页的操作栏在窄屏上会换行（局头有四颗按钮），`.body.has-actionbar` 里写死的高度
 * 只够一行，下面那 49px 的内容就被压在操作栏底下了。
 */

/** 每个用例用一个新的容器：Preact 把渲染状态挂在父节点上，复用 document.body
    再手动清空会让它 diff 到已经脱落的旧节点（App.test.tsx 里也踩过这个坑）。 */
function renderBar(): HTMLElement {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(<Bar />, container);
  return container;
}

function Bar() {
  return (
    <div class="actionbar" ref={useBarHeight<HTMLDivElement>()}>
      占位
    </div>
  );
}

afterEach(() => {
  document.body.innerHTML = "";
  document.documentElement.style.removeProperty("--bar-height");
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("量底部条的高度", () => {
  it("挂上之后把量到的高度写到 <html> 的 --bar-height 上", async () => {
    // jsdom 不做排版，getBoundingClientRect 永远是 0——这里直接替换掉它，
    // 模拟「操作栏换行之后是 139px」这件事。
    const rect = () => ({ height: 139, width: 375, top: 0, left: 0, right: 375, bottom: 139, x: 0, y: 0, toJSON: () => ({}) });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(rect as unknown as () => DOMRect);

    renderBar();

    // Preact 的 effect 是异步跑的，等它一下。
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--bar-height")).toBe("139px"),
    );
  });

  it("高度变了（换行 / 不换行）会跟着更新", async () => {
    let height = 90;
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
      (() => ({ height, width: 375, top: 0, left: 0, right: 375, bottom: height, x: 0, y: 0, toJSON: () => ({}) })) as unknown as () => DOMRect,
    );

    // 用假的 ResizeObserver：测试里自己控制「什么时候变高了」。
    const observers: (() => void)[] = [];
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private cb: () => void) {
          observers.push(() => this.cb());
        }
        observe() {}
        disconnect() {}
      },
    );

    renderBar();
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--bar-height")).toBe("90px"),
    );

    // 局头那条操作栏换行了。
    height = 139;
    observers.forEach((fire) => fire());

    expect(document.documentElement.style.getPropertyValue("--bar-height")).toBe("139px");
  });

  it("没有 ResizeObserver 的老浏览器：至少按首屏量一次，并在窗口变化时更新", async () => {
    vi.stubGlobal("ResizeObserver", undefined);
    // jsdom 不做排版，得自己给高度（和上面两个用例一样）。
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
      (() => ({ height: 100, width: 375, top: 0, left: 0, right: 375, bottom: 100, x: 0, y: 0, toJSON: () => ({}) })) as unknown as () => DOMRect,
    );
    const listeners: string[] = [];
    vi.spyOn(window, "addEventListener").mockImplementation((type: string) => {
      listeners.push(type);
    });

    renderBar();
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--bar-height")).toBe("100px"),
    );
    expect(listeners).toContain("resize");
  });

  it("高度是 0（还没排版）时不动 CSS 变量，别把兜底值冲掉", async () => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
      (() => ({ height: 0, width: 0, top: 0, left: 0, right: 0, bottom: 0, x: 0, y: 0, toJSON: () => ({}) })) as unknown as () => DOMRect,
    );
    document.documentElement.style.setProperty("--bar-height", "90px");

    renderBar();

    await vi.waitFor(() => expect(document.querySelector(".actionbar")).not.toBeNull());
    await new Promise((r) => setTimeout(r, 10));
    expect(document.documentElement.style.getPropertyValue("--bar-height")).toBe("90px");
  });
});

describe("元素挂得晚也要量到", () => {
  it("先渲染别的分支、操作栏随后才出现时，仍然会写 --bar-height", async () => {
    // 详情页第一次渲染是「正在打开…」，那一支没有操作栏——用对象 ref 的话这里永远是 null。
    const late = () => (
      <div class="loading">
        正在打开…
      </div>
    );
    const rect = () => ({ height: 155, width: 375, top: 0, left: 0, right: 375, bottom: 155, x: 0, y: 0, toJSON: () => ({}) });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(rect as unknown as () => DOMRect);

    const container = document.createElement("div");
    document.body.appendChild(container);
    render(late(), container);
    expect(document.documentElement.style.getPropertyValue("--bar-height")).not.toBe("155px");

    // 数据回来了，操作栏出现。
    render(<Bar />, container);

    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--bar-height")).toBe("155px"),
    );
  });
});

describe("toast 的抬升（#94 follow-up）", () => {
  it("量到底栏高度时，顺手把「高度 + 间隙」写到 --toast-lift 上", async () => {
    const rect = () => ({ height: 155, width: 375, top: 0, left: 0, right: 375, bottom: 155, x: 0, y: 0, toJSON: () => ({}) });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(rect as unknown as () => DOMRect);

    renderBar();

    // 155 的底栏（局头两行操作栏）→ toast 抬 175，正好在它上面。
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--toast-lift")).toBe("175px"),
    );
  });

  it("底栏卸载后把抬升还给兜底值，别让下一个页面顶着一个旧高度", async () => {
    const rect = () => ({ height: 91, width: 375, top: 0, left: 0, right: 375, bottom: 91, x: 0, y: 0, toJSON: () => ({}) });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(rect as unknown as () => DOMRect);

    const container = renderBar();
    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--toast-lift")).toBe("111px"),
    );

    render(null, container);

    await vi.waitFor(() =>
      expect(document.documentElement.style.getPropertyValue("--toast-lift")).toBe(""),
    );
  });
});

describe("样式里的兜底值", () => {
  it(".body.has-actionbar 的留白读的是 --bar-height，不是写死的高度", async () => {
    const { readFileSync } = await import("node:fs");
    const { resolve } = await import("node:path");
    const css = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8");
    const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
    const rules: { selector: string; body: string }[] = [];
    let i = 0;
    while (i < stripped.length) {
      const open = stripped.indexOf("{", i);
      if (open < 0) break;
      const selector = stripped.slice(i, open).trim();
      let depth = 1;
      let j = open + 1;
      while (j < stripped.length && depth > 0) {
        if (stripped[j] === "{") depth++;
        else if (stripped[j] === "}") depth--;
        j++;
      }
      rules.push({ selector, body: stripped.slice(open + 1, j - 1) });
      i = j;
    }

    // 操作栏那种会换行的条：留白必须走变量，量出来的值才能生效。
    expect(css).toMatch(/\.body\.has-bar,\s*\.body\.has-actionbar\s*\{[^}]*var\(--bar-height/);
    // 而 90px 只是 :root 上的兜底（首屏、JS 还没跑到时用）。
    expect(css).toMatch(/:root\s*\{[^}]*--bar-height:\s*90px/);
    // toast 也必须读变量：写死 96px 在局头两行操作栏（155px）下会盖住上面一行。
    const toast = rules.find((r) => r.selector === ".toast");
    expect(toast).toBeDefined();
    expect(toast!.body).toMatch(/bottom:\s*calc\(var\(--toast-lift/);
    expect(toast!.body).not.toMatch(/bottom:\s*calc\(96px/);
  });
});
