import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CreateGame } from "./CreateGame";

function respond(status: number, body: unknown) {
  const text = JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

/** 起一个总是失败的接口，把每次请求带的幂等键记下来。 */
function setup(keys: (string | undefined)[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init: RequestInit) => {
      keys.push((init.headers as Record<string, string>)["Idempotency-Key"]);
      // 用 500：不触发 503 的自动重试，一次提交就是一次请求（503 的重试在 api.test.ts 里测）。
      return respond(500, { error: { code: "boom", message: "失败" } });
    }),
  );

  const container = document.createElement("div");
  document.body.appendChild(container);
  render(
    <CreateGame
      me={{ id: "u1", nickname: "老王" }}
      requireIdentity={async () => null}
      toast={() => {}}
      onBack={() => {}}
      onCreated={() => {}}
    />,
    container,
  );
  return container;
}

function fill(container: HTMLElement, value: string) {
  const input = container.querySelector("#venue") as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

function clickSubmit(container: HTMLElement) {
  (container.querySelector(".actionbar .btn") as HTMLButtonElement).click();
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("开局表单的幂等键（§11.4）", () => {
  // 512 那次其实可能已经在服务端成功了，只是响应没回来。用户再点一次时，
  // 如果换了新键，服务端就会再开一局。
  it("失败后再点一次，两次用的是同一个键", async () => {
    const keys: (string | undefined)[] = [];
    const container = setup(keys);
    fill(container, "Pacific Place");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(1));
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(2));

    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBe(keys[0]);
  });

  it("表单内容变了就换一个新键（那是另一次提交了）", async () => {
    const keys: (string | undefined)[] = [];
    const container = setup(keys);
    fill(container, "Pacific Place");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(1));

    fill(container, "PIK Avenue");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));
    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(2));

    expect(keys[1]).not.toBe(keys[0]);
  });
});
