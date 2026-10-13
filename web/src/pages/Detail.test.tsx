import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GameDetail, GamePatch } from "../api";
import { Detail } from "./Detail";
import { installFakeSocket } from "../fakeSocket";

function detail(version: number, over: Partial<GameDetail["game"]> = {}): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "ABC123",
      starts_at: 1791986400,
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      status: "open",
      version,
      capacity: 4,
      ...over,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }],
    waitlist: [],
    missing: 3,
    me: { role: "seat", seat_no: 1 },
  };
}

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

/** 起一个详情页：GET 返回 initial，PATCH 按 patchResponses 依次返回。 */
function setup(initial: GameDetail, patchResponses: Response[]) {
  const patches: { body: GamePatch; ifMatch: string | null }[] = [];
  const toasts: string[] = [];
  let gets = 0;

  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      if (method === "GET") {
        gets++;
        return respond(200, initial);
      }
      patches.push({
        body: JSON.parse(String(init?.body ?? "{}")),
        ifMatch: (init?.headers as Record<string, string>)?.["If-Match"] ?? null,
      });
      return patchResponses[Math.min(patches.length, patchResponses.length) - 1];
    }),
  );

  const container = document.createElement("div");
  document.body.appendChild(container);
  render(
    <Detail
      code="ABC123"
      me={{ id: "u1", nickname: "老王" }}
      requireIdentity={async () => null}
      toast={(t: string) => toasts.push(t)}
      onBack={() => {}}
    />,
    container,
  );
  return { container, patches, toasts, getGets: () => gets };
}

/** 打开编辑弹层，改地点，提交。 */
async function editVenue(container: HTMLElement, venue: string) {
  const open = [...container.querySelectorAll("button")].find((b) => b.textContent?.includes("改时间")) as HTMLButtonElement;
  open.click();
  await vi.waitFor(() => expect(container.querySelector("#venue")).not.toBeNull());

  const input = container.querySelector("#venue") as HTMLInputElement;
  input.value = venue;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  // Preact 的状态更新是异步的：等它重渲染完再提交，否则读到的还是旧值。
  await new Promise((resolve) => setTimeout(resolve, 20));

  const form = container.querySelector("form.sheet") as HTMLFormElement;
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
}

beforeEach(() => {
  installFakeSocket();
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("局头改牌局的版本冲突（K4 / K5 / K6）", () => {
  // K5：冲突与本次修改无关（期间有人报名）——自动带新版本号重提一次，不打断用户。
  it("K5：字段没冲突就自动重提一次，并提示「已保存 · 期间有人报名」", async () => {
    const { container, patches, toasts } = setup(detail(1), [
      respond(409, { ...detail(2), error: { code: "version_conflict", message: "冲突" } }),
      respond(200, detail(3)),
    ]);

    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    await editVenue(container, "Grand Indonesia");
    await vi.waitFor(() => expect(patches.length).toBe(2));

    // 第一次用编辑开始时的版本号，第二次用 409 里带回来的最新版本号。
    expect(patches[0].ifMatch).toBe("1");
    expect(patches[1].ifMatch).toBe("2");
    expect(patches[1].body).toEqual({ venue: "Grand Indonesia" });
    await vi.waitFor(() => expect(toasts).toContain("已保存 · 期间有人报名"));
  });

  // K4：同一个字段被两个人改了——摆出对比，让局头选。
  it("K4：字段真冲突时弹出对比框，不擅自覆盖", async () => {
    const { container, patches, toasts } = setup(detail(1), [
      respond(409, { ...detail(2, { venue: "PIK Avenue" }), error: { code: "version_conflict", message: "冲突" } }),
      respond(200, detail(3)),
    ]);

    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    await editVenue(container, "Grand Indonesia");

    await vi.waitFor(() => expect(container.querySelector('[data-testid="conflict-dialog"]')).not.toBeNull());
    expect(container.textContent).toContain("Grand Indonesia");
    expect(container.textContent).toContain("PIK Avenue");
    // 还没提交第二次：等用户选。
    expect(patches.length).toBe(1);

    // 选「仍然改成我的」→ 带最新版本号重提。
    const keep = [...container.querySelectorAll("button")].find((b) => b.textContent === "仍然改成我的") as HTMLButtonElement;
    keep.click();
    await vi.waitFor(() => expect(patches.length).toBe(2));
    expect(patches[1].ifMatch).toBe("2");
    await vi.waitFor(() => expect(toasts).toContain("已按你的版本改好"));
  });

  // K6：编辑期间局头被转给了别人——丢掉本地修改，重新加载。
  it("K6：403 not_host 时提示并重新加载", async () => {
    const { container, toasts, getGets } = setup(detail(1), [
      respond(403, { error: { code: "not_host", message: "只有局头能做这件事" } }),
    ]);

    await vi.waitFor(() => expect(container.textContent).toContain("Pacific Place"));
    const before = getGets();
    await editVenue(container, "Grand Indonesia");

    await vi.waitFor(() => expect(toasts).toContain("你已经不是局头了"));
    await vi.waitFor(() => expect(getGets()).toBeGreaterThan(before));
  });
});
