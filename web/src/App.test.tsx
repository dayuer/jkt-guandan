import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

// 每个用例用一个全新的容器：Preact 会把渲染状态挂在父节点上，
// 复用 document.body 再手动清空会让它 diff 到已经脱落的旧节点。
function renderApp(): HTMLElement {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(<App />, container);
  return container;
}

function healthText(container: HTMLElement): string | null {
  return container.querySelector('[data-testid="health"]')?.textContent ?? null;
}

describe("App", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    vi.unstubAllGlobals();
  });

  it("渲染标题和副标题", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    const container = renderApp();
    expect(container.textContent).toContain("随时掼掼蛋");
    expect(container.textContent).toContain("雅加达");
  });

  it("健康检查通过后显示已连上", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true })));
    const container = renderApp();
    await vi.waitFor(() => {
      expect(healthText(container)).toContain("已连上");
    });
  });

  it("健康检查失败后显示连不上", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: false })));
    const container = renderApp();
    await vi.waitFor(() => {
      expect(healthText(container)).toContain("连不上");
    });
  });

  it("请求抛错后也显示连不上", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("网络断了");
      }),
    );
    const container = renderApp();
    await vi.waitFor(() => {
      expect(healthText(container)).toContain("连不上");
    });
  });
});
