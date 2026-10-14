import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentError, agentDisabled, askAgent, resetAgentDisabled } from "./agent";
import { draftEvent, hangingFetch, jsonError, reply, sseResponse } from "./agentTestUtil";

const DRAFT = { starts_at: 1791980000, area: "PIK", venue: "PIK Avenue", kind: "classic", note: "" };

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  resetAgentDisabled();
});

async function failure(p: Promise<unknown>): Promise<AgentError> {
  try {
    await p;
  } catch (err) {
    expect(err).toBeInstanceOf(AgentError);
    return err as AgentError;
  }
  throw new Error("没有失败");
}

describe("askAgent", () => {
  it("拼起分片的 reply，最后拿到草稿；请求按 CSRF 要求带 JSON 头", async () => {
    const stream = reply("好嘞，") + reply("周四 19:30 在 PIK") + draftEvent({ draft: DRAFT, valid: true });
    // 在多字节汉字中间切开，确认解码器按流解码。
    const bytes = new TextEncoder().encode(stream);
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => {
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          for (let i = 0; i < bytes.length; i += 7) c.enqueue(bytes.slice(i, i + 7));
          c.close();
        },
      });
      expect(init.signal).toBeTruthy();
      return new Response(body, { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);

    const seen: string[] = [];
    const result = await askAgent("周四晚上 PIK 打一局", { onReply: (t) => seen.push(t) });

    expect(result).toEqual({ draft: DRAFT, valid: true, problem: undefined });
    expect(seen.at(-1)).toBe("好嘞，周四 19:30 在 PIK");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/agent");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["content-type"]).toBe("application/json");
    expect(JSON.parse(init.body as string)).toEqual({ text: "周四晚上 PIK 打一局" });
  });

  it("valid 为假时带回 problem", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        sseResponse([draftEvent({ draft: { area: "PIK" }, valid: false, problem: "还没说在哪儿打" })]),
      ),
    );
    expect(await askAgent("打一局")).toEqual({ draft: { area: "PIK" }, valid: false, problem: "还没说在哪儿打" });
  });

  it.each(["agent_disabled", "agent_unavailable", "agent_busy"])("503 %s 原样交给页面", async (code) => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonError(503, code)));
    const err = await failure(askAgent("打一局"));
    expect(err.code).toBe(code);
    expect(agentDisabled()).toBe(code === "agent_disabled");
  });

  it("503 不自动重试（重试没有意义，只会让用户多等）", async () => {
    const fetchMock = vi.fn(async () => jsonError(503, "agent_busy"));
    vi.stubGlobal("fetch", fetchMock);
    await failure(askAgent("打一局"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("非 JSON 的错误响应按 agent_unavailable 处理", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("bad gateway", { status: 502 })));
    expect((await failure(askAgent("打一局"))).code).toBe("agent_unavailable");
  });

  it("推到一半的 error 事件", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        sseResponse([
          reply("好嘞"),
          `event: error\ndata: ${JSON.stringify({ code: "agent_unavailable", message: "蛋蛋走神了，直接填表吧" })}\n\n`,
        ]),
      ),
    );
    const err = await failure(askAgent("打一局"));
    expect(err.code).toBe("agent_unavailable");
    expect(err.message).toBe("蛋蛋走神了，直接填表吧");
  });

  it("流断了还没有 draft", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse([reply("好嘞")])));
    expect((await failure(askAgent("打一局"))).code).toBe("agent_unavailable");
  });

  it("超时：fetch 一直不返回", async () => {
    vi.stubGlobal("fetch", hangingFetch());
    expect((await failure(askAgent("打一局", { timeoutMs: 20 }))).code).toBe("timeout");
  });

  it("超时：推了一半卡住", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_u: string, init: RequestInit) => sseResponse([reply("好嘞")], init.signal, true)),
    );
    expect((await failure(askAgent("打一局", { timeoutMs: 20 }))).code).toBe("timeout");
  });

  it("调用方 abort（离开页面）", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_u: string, init: RequestInit) => sseResponse([reply("好嘞")], init.signal, true)),
    );
    const ctrl = new AbortController();
    const p = askAgent("打一局", { signal: ctrl.signal, onReply: () => ctrl.abort() });
    expect((await failure(p)).code).toBe("aborted");
  });
});
