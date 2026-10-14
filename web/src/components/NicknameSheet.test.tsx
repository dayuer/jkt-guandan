import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NicknameSheet } from "./NicknameSheet";

function mount() {
  const fetchMock = vi.fn(
    async () =>
      new Response(JSON.stringify({ user: { id: "u1", nickname: "阿明" } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
  );
  vi.stubGlobal("fetch", fetchMock);
  const onDone = vi.fn();
  const container = document.createElement("div");
  document.body.appendChild(container);
  act(() => render(<NicknameSheet onDone={onDone} onCancel={() => {}} />, container));
  const input = container.querySelector("input") as HTMLInputElement;
  return { container, input, fetchMock, onDone };
}

async function type(input: HTMLInputElement, value: string) {
  input.value = value;
  await act(async () => {
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("称呼弹层（#130 第 3 条）", () => {
  it("输入框在 form 里，按回车就提交", async () => {
    const { input, fetchMock, onDone } = mount();
    expect(input.form).not.toBeNull();
    await type(input, "阿明");
    await act(async () => {
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    await vi.waitFor(() => expect(onDone).toHaveBeenCalledWith("u1", "阿明"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("输入法还在拼字时的回车只是上屏，不提交", async () => {
    const { input, fetchMock } = mount();
    await type(input, "a'm");
    await act(async () => {
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true, cancelable: true }),
      );
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("没有 autofocus：弹层是异步挂上的，程序化 focus 在 iOS / 微信 / XWeb 里只给红框不弹键盘（P0）", () => {
    const { input } = mount();
    expect(input.hasAttribute("autofocus")).toBe(false);
    expect(document.activeElement).not.toBe(input);
  });

  it("安卓 WebView 的「有焦点没键盘」：焦点不是点出来的，按下时先 blur，让这次点击重新聚焦（P0）", () => {
    const { input } = mount();
    input.focus(); // 模拟 WebView 自己给的焦点
    expect(document.activeElement).toBe(input);
    input.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    expect(document.activeElement).not.toBe(input);
  });

  it("输入框的属性：称呼自动填充、文字键盘、回车键显示「完成」", () => {
    const { input } = mount();
    expect(input.getAttribute("autocomplete")).toBe("nickname");
    expect(input.getAttribute("inputmode")).toBe("text");
    expect(input.getAttribute("enterkeyhint")).toBe("done");
  });
});
