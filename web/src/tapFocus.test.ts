import { afterEach, describe, expect, it } from "vitest";
import { tapFocusHandlers } from "./tapFocus";

function setup() {
  let t = 10_000;
  const h = tapFocusHandlers(() => t);
  const input = document.createElement("input");
  input.addEventListener("pointerdown", h.onPointerDown);
  input.addEventListener("touchstart", h.onTouchStart);
  input.addEventListener("focus", h.onFocus);
  input.addEventListener("blur", h.onBlur);
  document.body.appendChild(input);
  return { input, advance: (ms: number) => (t += ms) };
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("tapFocusHandlers（P0：安卓 WebView 点输入框不弹键盘）", () => {
  it("焦点不是手势给的（程序化 focus）：按下时先 blur，让原生聚焦重新来一遍", () => {
    const { input, advance } = setup();
    input.focus(); // 「有焦点、没键盘」
    advance(5000);
    input.dispatchEvent(new Event("pointerdown"));
    expect(document.activeElement).not.toBe(input);
  });

  it("焦点是用户点出来的：再点（挪光标）不 blur，键盘不会收起再弹", () => {
    const { input, advance } = setup();
    input.dispatchEvent(new Event("pointerdown"));
    input.focus(); // 这次点击的原生聚焦
    advance(3000);
    input.dispatchEvent(new Event("pointerdown"));
    expect(document.activeElement).toBe(input);
  });

  it("还没有焦点：什么都不做", () => {
    const { input } = setup();
    input.dispatchEvent(new Event("pointerdown"));
    expect(document.activeElement).not.toBe(input);
  });

  it("pointerdown 和 touchstart 都来时只 blur 一次，不会把手势给的焦点也弄掉", () => {
    const { input, advance } = setup();
    input.focus();
    advance(5000);
    input.dispatchEvent(new Event("pointerdown")); // blur
    input.focus(); // 原生聚焦
    input.dispatchEvent(new Event("touchstart"));
    expect(document.activeElement).toBe(input);
  });
});
