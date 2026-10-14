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

  it("关掉浏览器的自动填充条，回车键显示「完成」", () => {
    const { input } = mount();
    expect(input.getAttribute("autocomplete")).toBe("off");
    expect(input.getAttribute("enterkeyhint")).toBe("done");
  });
});
