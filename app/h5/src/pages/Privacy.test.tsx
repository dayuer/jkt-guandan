import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Privacy } from "./Privacy";

/** G5 · 隐私与数据（§14）。 */

function respond(status: number, body: unknown = null) {
  const text = body === null ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

function setup(opts: { hasIdentity: boolean; onDelete: () => Response }) {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? "GET"} ${url}`);
      return opts.onDelete();
    }),
  );

  const container = document.createElement("div");
  document.body.appendChild(container);
  const toasts: string[] = [];
  let deleted = 0;
  render(
    <Privacy
      hasIdentity={opts.hasIdentity}
      toast={(t) => toasts.push(t)}
      onBack={() => calls.push("back")}
      onDeleted={() => deleted++}
    />,
    container,
  );
  return { container, calls, toasts, getDeleted: () => deleted };
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("隐私页（§14）", () => {
  it("说清楚存了什么、存多久、怎么删", () => {
    const { container } = setup({ hasIdentity: true, onDelete: () => respond(204) });

    expect(container.textContent).toContain("我们存了什么");
    expect(container.textContent).toContain("存多久");
    // 期限要和 §14 对上：设备 180 天、埋点 1 年、备份 30 天。
    expect(container.textContent).toContain("180 天");
    expect(container.textContent).toContain("1 年");
    expect(container.textContent).toContain("30 天");
    // 注销之后座位怎么显示，进去之前就要讲明白。
    expect(container.textContent).toContain("已注销牌友");
  });

  it("注销要先确认，确认后才真的调接口", async () => {
    const { container, calls, toasts, getDeleted } = setup({
      hasIdentity: true,
      onDelete: () => respond(204),
    });

    (container.querySelector(".pad .btn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=delete-dialog]")).not.toBeNull());
    expect(calls).toHaveLength(0); // 只是打开了确认框

    const confirm = [...container.querySelectorAll("[data-testid=delete-dialog] button")].find(
      (b) => b.textContent === "注销",
    ) as HTMLButtonElement;
    confirm.click();

    await vi.waitFor(() => expect(getDeleted()).toBe(1));
    expect(calls).toEqual(["DELETE /api/me"]);
    expect(toasts.join()).toContain("已经注销");
  });

  it("确认框里可以反悔", async () => {
    const { container, calls, getDeleted } = setup({ hasIdentity: true, onDelete: () => respond(204) });

    (container.querySelector(".pad .btn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=delete-dialog]")).not.toBeNull());

    const cancel = [...container.querySelectorAll("[data-testid=delete-dialog] button")].find(
      (b) => b.textContent === "不注销",
    ) as HTMLButtonElement;
    cancel.click();

    await vi.waitFor(() => expect(container.querySelector("[data-testid=delete-dialog]")).toBeNull());
    expect(calls).toHaveLength(0);
    expect(getDeleted()).toBe(0);
  });

  it("删除失败时留在原地并提示，不假装成功", async () => {
    const { container, toasts, getDeleted } = setup({
      hasIdentity: true,
      // 用 500：不触发 503 的自动重试，一次提交就是一次请求（503 的重试在 api.test.ts 里测）。
      onDelete: () => respond(500, { error: { code: "boom", message: "" } }),
    });

    (container.querySelector(".pad .btn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=delete-dialog]")).not.toBeNull());
    ([...container.querySelectorAll("[data-testid=delete-dialog] button")].find(
      (b) => b.textContent === "注销",
    ) as HTMLButtonElement).click();

    await vi.waitFor(() => expect(container.querySelector(".error")?.textContent).toContain("没删掉"));
    expect(getDeleted()).toBe(0);
    expect(toasts).toHaveLength(0);
  });

  it("没有身份时只读说明，不显示注销按钮", () => {
    const { container, calls } = setup({ hasIdentity: false, onDelete: () => respond(204) });

    expect(container.textContent).toContain("我们存了什么");
    expect(container.querySelector(".pad .btn")).toBeNull();
    expect(container.textContent).toContain("没有可以注销的数据");
    expect(calls).toHaveLength(0);
  });
});
