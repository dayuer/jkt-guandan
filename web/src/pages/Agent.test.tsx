import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetAgentDisabled } from "../agent";
import { draftEvent, jsonError, reply, sseResponse } from "../agentTestUtil";
import { Agent, type FormPrefill } from "./Agent";

const DRAFT = { starts_at: 1791980000, area: "PIK", venue: "PIK Avenue", kind: "classic" };

const GAME_DETAIL = {
  game: {
    short_code: "AB12",
    version: 1,
    starts_at: DRAFT.starts_at,
    area: "PIK",
    venue: "PIK Avenue",
    kind: "classic",
    note: "",
    status: "open",
    host: { id: "u1", nickname: "老王" },
  },
  seats: [{ index: 0, user: { id: "u1", nickname: "老王" } }],
  waitlist: [],
  me: { role: "host" },
};

const flush = async (n = 10) => {
  for (let i = 0; i < n; i++) await new Promise((r) => setTimeout(r, 0));
};

function mount(fetchImpl: (url: string, init: RequestInit) => Promise<Response>) {
  const fetchMock = vi.fn(fetchImpl);
  vi.stubGlobal("fetch", fetchMock);
  const toast = vi.fn();
  const onForm = vi.fn<(p: FormPrefill) => void>();
  const onCreated = vi.fn();
  const container = document.createElement("div");
  document.body.appendChild(container);
  act(() =>
    render(
      <Agent
        me={{ id: "u1", nickname: "老王" }}
        requireIdentity={async () => null}
        toast={toast}
        onBack={() => {}}
        onForm={onForm}
        onCreated={onCreated}
      />,
      container,
    ),
  );
  return { container, fetchMock, toast, onForm, onCreated };
}

async function say(container: HTMLElement, text: string) {
  const input = container.querySelector(".composer input") as HTMLInputElement;
  input.value = text;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  await flush(1);
  (container.querySelector(".composer .send") as HTMLButtonElement).click();
  await flush();
}

function button(container: HTMLElement, label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll("button")].find((b) => b.textContent?.includes(label));
  if (!found) throw new Error(`没有按钮：${label}`);
  return found as HTMLButtonElement;
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  resetAgentDisabled();
});

describe("07 · 蛋蛋", () => {
  it("说一句 → 流式回复 → 确认卡 → 照常开局 → 分享面板", async () => {
    const { container, fetchMock, onCreated } = mount(async (url, init) => {
      if (url === "/api/agent") {
        return sseResponse([reply("好嘞，"), reply("周四 19:30 在 PIK"), draftEvent({ draft: DRAFT, valid: true })]);
      }
      expect(url).toBe("/api/games");
      expect(JSON.parse(init.body as string)).toEqual(DRAFT);
      expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBeTruthy();
      return new Response(JSON.stringify(GAME_DETAIL), { status: 201 });
    });

    await say(container, "周四晚上 PIK 打一局");

    expect(container.querySelector(".bubble.me")?.textContent).toBe("周四晚上 PIK 打一局");
    expect(container.textContent).toContain("好嘞，周四 19:30 在 PIK");
    const card = container.querySelector(".confirm-card")!;
    expect(card.textContent).toContain("PIK · PIK Avenue");
    expect(card.textContent).toContain("经典掼蛋");

    button(container, "确认，发到群里").click();
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(container.querySelector(".sheet")).toBeTruthy();
    expect(container.textContent).toContain("局开好了");
    expect(onCreated).not.toHaveBeenCalled();
  });

  it("「改一下」带着草稿去 08", async () => {
    const { container, onForm } = mount(async () => sseResponse([draftEvent({ draft: DRAFT, valid: true })]));
    await say(container, "周四晚上 PIK 打一局");
    button(container, "改一下").click();
    expect(onForm).toHaveBeenCalledWith({ draft: DRAFT, problem: undefined, said: "周四晚上 PIK 打一局" });
  });

  it("草稿不合格：显示 problem，去 08 时带上草稿", async () => {
    const draft = { area: "PIK", kind: "classic" };
    const { container, onForm } = mount(async () =>
      sseResponse([reply("好嘞"), draftEvent({ draft, valid: false, problem: "还没说在哪儿打" })]),
    );
    await say(container, "PIK 打一局");

    expect(container.querySelector(".confirm-card")).toBeNull();
    expect(container.textContent).toContain("还没说在哪儿打");
    button(container, "去表单补一下").click();
    expect(onForm).toHaveBeenCalledWith({ draft, problem: "还没说在哪儿打", said: "PIK 打一局" });
  });

  it("503 agent_disabled：不提示，直接去 08", async () => {
    const { container, toast, onForm } = mount(async () => jsonError(503, "agent_disabled"));
    await say(container, "打一局");
    expect(toast).not.toHaveBeenCalled();
    expect(onForm).toHaveBeenCalledWith({ said: "打一局" });
  });

  it.each([
    ["agent_unavailable", "蛋蛋走神了"],
    ["agent_busy", "蛋蛋有点忙"],
  ])("503 %s：提示一句，给「直接填表」并带上用户的话", async (code, toastText) => {
    const { container, toast, onForm } = mount(async () => jsonError(503, code));
    await say(container, "周四晚上打一局");

    expect(toast).toHaveBeenCalledWith(toastText);
    expect(container.textContent).toContain("蛋蛋走神了，直接填表吧");
    button(container, "直接填表").click();
    expect(onForm).toHaveBeenCalledWith({ draft: undefined, problem: undefined, said: "周四晚上打一局" });
  });

  it("推到一半出错：同样退回表单", async () => {
    const { container, toast } = mount(async () =>
      sseResponse([reply("好嘞"), `event: error\ndata: {"code":"agent_unavailable","message":"蛋蛋走神了，直接填表吧"}\n\n`]),
    );
    await say(container, "打一局");
    expect(toast).toHaveBeenCalled();
    expect(button(container, "直接填表")).toBeTruthy();
  });

  it("离开页面时掐掉还在推的流", async () => {
    let signal: AbortSignal | null | undefined;
    const { container, toast } = mount(async (_url, init) => {
      signal = init.signal;
      return sseResponse([reply("好嘞")], init.signal, true);
    });
    await say(container, "打一局");
    expect(signal?.aborted).toBe(false);

    act(() => render(null, container));
    await flush();
    expect(signal?.aborted).toBe(true);
    expect(toast).not.toHaveBeenCalled();
  });
});
