import { createSSEParser } from "./sse";

/**
 * 蛋蛋 `POST /api/agent` 的客户端（server-design §6，#11）。
 *
 * 服务端只出草稿不写库；用户确认后照常走 `POST /api/games`。
 * 不能用时一律退回 08 表单，所以这里把各种失败都归成 AgentError(code)，
 * 页面只需要分两类：`agent_disabled` 悄悄退回，其余提示一句再给「直接填表」。
 *
 * 不走 api.ts 的 request()：那里会把 503 自动重试一次，而蛋蛋的 503
 * （没配 key、模型挂了、并发满了）重试没有意义，只会让用户多等。
 */

/** 草稿字段和 `POST /api/games` 的请求体一致；认不出的字段服务端不给。 */
export interface AgentDraft {
  starts_at?: number;
  area?: string;
  venue?: string;
  kind?: string;
  note?: string;
}

export interface AgentResult {
  draft: AgentDraft;
  valid: boolean;
  problem?: string;
}

/** 服务端的错误码，外加前端自己的 `timeout`、`aborted`。 */
export class AgentError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "AgentError";
  }
}

/** 服务端整个请求最多 20 秒（§6）；前端多留一点余量，再晚就当它挂了。 */
export const AGENT_TIMEOUT_MS = 25_000;

const FALLBACK_MESSAGE = "蛋蛋走神了，直接填表吧";

/** 没配 key 是常态而不是故障：记下来，这次打开页面里就不再显示蛋蛋入口。 */
let disabled = false;
export function agentDisabled(): boolean {
  return disabled;
}
/** 仅测试用。 */
export function resetAgentDisabled(): void {
  disabled = false;
}

export interface AskOptions {
  signal?: AbortSignal;
  onReply?: (textSoFar: string) => void;
  timeoutMs?: number;
}

export async function askAgent(text: string, opts: AskOptions = {}): Promise<AgentResult> {
  const ctrl = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    ctrl.abort();
  }, opts.timeoutMs ?? AGENT_TIMEOUT_MS);
  const onOuterAbort = () => ctrl.abort();
  if (opts.signal?.aborted) ctrl.abort();
  opts.signal?.addEventListener("abort", onOuterAbort);

  const failure = (err: unknown): AgentError => {
    if (err instanceof AgentError) return err;
    if (timedOut) return new AgentError("timeout", FALLBACK_MESSAGE);
    if (opts.signal?.aborted) return new AgentError("aborted", "已取消");
    return new AgentError("agent_unavailable", FALLBACK_MESSAGE);
  };

  try {
    const resp = await fetch("/api/agent", {
      method: "POST",
      headers: { "content-type": "application/json", accept: "text/event-stream" },
      body: JSON.stringify({ text }),
      signal: ctrl.signal,
    });

    if (!resp.ok) {
      let code = "agent_unavailable";
      let message = FALLBACK_MESSAGE;
      try {
        const body = await resp.json();
        code = body?.error?.code ?? code;
        message = body?.error?.message ?? message;
      } catch {
        // 不是 JSON：按 agent_unavailable 处理。
      }
      if (code === "agent_disabled") disabled = true;
      throw new AgentError(code, message);
    }
    if (!resp.body) throw new AgentError("agent_unavailable", FALLBACK_MESSAGE);

    let reply = "";
    let result: AgentResult | null = null;
    let streamError: AgentError | null = null;
    const parser = createSSEParser(({ event, data }) => {
      if (result || streamError) return;
      let payload: any;
      try {
        payload = JSON.parse(data);
      } catch {
        return;
      }
      if (event === "reply" && typeof payload?.text === "string") {
        reply += payload.text;
        opts.onReply?.(reply);
      } else if (event === "draft" && payload?.draft) {
        result = { draft: payload.draft, valid: payload.valid === true, problem: payload.problem || undefined };
      } else if (event === "error") {
        streamError = new AgentError(payload?.code ?? "agent_unavailable", payload?.message || FALLBACK_MESSAGE);
      }
    });

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      parser.push(decoder.decode(value, { stream: true }));
      if (result || streamError) {
        void reader.cancel().catch(() => {});
        break;
      }
    }
    parser.push(decoder.decode());
    parser.end();

    if (streamError) throw streamError;
    if (!result) throw new AgentError("agent_unavailable", FALLBACK_MESSAGE);
    return result;
  } catch (err) {
    throw failure(err);
  } finally {
    clearTimeout(timer);
    opts.signal?.removeEventListener("abort", onOuterAbort);
  }
}
