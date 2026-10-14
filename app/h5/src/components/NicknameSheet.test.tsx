import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NicknameSheet } from "./NicknameSheet";

const ok = () =>
  new Response(JSON.stringify({ user: { id: "u1", nickname: "阿明" } }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });

function mount(respond: () => Response = ok) {
  const fetchMock = vi.fn(async () => respond());
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

describe("称呼命中敏感词（#147）", () => {
  const sensitive = () =>
    new Response(
      JSON.stringify({
        error: { code: "sensitive_word", message: "请勿涉及金钱：这里只约纯娱乐局", field: "nickname" },
      }),
      { status: 422, headers: { "content-type": "application/json" } },
    );

  async function submit(container: HTMLElement) {
    await act(async () => {
      (container.querySelector("form") as HTMLFormElement).requestSubmit();
    });
  }

  it("提示显示在输入框下面，输入框标红，已经写的字不动，不算成功", async () => {
    const { container, input, onDone } = mount(sensitive);
    await type(input, "judi 50rb");
    await submit(container);
    await vi.waitFor(() => expect(container.querySelector(".errmsg")?.textContent).toContain("请勿涉及金钱"));
    expect(input.classList.contains("err")).toBe(true);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.nextElementSibling?.classList.contains("errmsg")).toBe(true);
    expect(input.value).toBe("judi 50rb");
    expect(onDone).not.toHaveBeenCalled();
  });

  it("开始改称呼，提示就收起来", async () => {
    const { container, input } = mount(sensitive);
    await type(input, "judi");
    await submit(container);
    await vi.waitFor(() => expect(container.querySelector(".errmsg")).not.toBeNull());
    await type(input, "阿明");
    expect(container.querySelector(".errmsg")).toBeNull();
    expect(input.classList.contains("err")).toBe(false);
  });
});
