/** 测试用：伪造 `POST /api/agent` 的响应。 */
import { vi } from "vitest";

const enc = new TextEncoder();

/** 把一串 SSE 文本按给定的片推出来；`hang` 为真时推完不收尾（模拟卡住的模型）。 */
export function sseResponse(chunks: string[], signal?: AbortSignal | null, hang = false): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      chunks.forEach((c) => controller.enqueue(enc.encode(c)));
      if (!hang) controller.close();
      signal?.addEventListener("abort", () => {
        try {
          controller.error(new DOMException("aborted", "AbortError"));
        } catch {
          // 已经关了
        }
      });
    },
  });
  return new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } });
}

export function jsonError(status: number, code: string, message = "x"): Response {
  return new Response(JSON.stringify({ error: { code, message } }), {
    status,
    headers: { "content-type": "application/json" },
  });
}

export const reply = (text: string) => `event: reply\ndata: ${JSON.stringify({ text })}\n\n`;
export const draftEvent = (payload: unknown) => `event: draft\ndata: ${JSON.stringify(payload)}\n\n`;

/** fetch 一直不返回，直到被 abort。 */
export function hangingFetch() {
  return vi.fn(
    (_url: string, init: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      }),
  );
}
