/**
 * 极简的 text/event-stream 解析器（蛋蛋 `POST /api/agent`，server-design §6）。
 *
 * EventSource 只能发 GET，所以这里用 fetch + ReadableStream 自己解析。
 * 网络分片可能把一行、甚至一个 `\r\n` 劈成两半，所以按行缓冲：
 * 只处理已经收到换行的完整行，剩下的半行留到下一片。
 */
export interface SSEEvent {
  event: string;
  data: string;
}

export interface SSEParser {
  /** 喂进一段文本（任意切分）。 */
  push(chunk: string): void;
  /** 流结束：把最后一个没有空行收尾的事件也交出去。 */
  end(): void;
}

export function createSSEParser(onEvent: (e: SSEEvent) => void): SSEParser {
  let buffer = "";
  let event = "";
  let data: string[] = [];
  let hasData = false;

  const dispatch = () => {
    if (hasData) onEvent({ event: event || "message", data: data.join("\n") });
    event = "";
    data = [];
    hasData = false;
  };

  const line = (text: string) => {
    if (text === "") return dispatch();
    if (text.startsWith(":")) return; // 注释 / 心跳
    const colon = text.indexOf(":");
    const field = colon < 0 ? text : text.slice(0, colon);
    let value = colon < 0 ? "" : text.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") event = value;
    else if (field === "data") {
      data.push(value);
      hasData = true;
    }
  };

  return {
    push(chunk) {
      buffer += chunk;
      for (;;) {
        const m = /\r\n|\r|\n/.exec(buffer);
        if (!m) break;
        // 末尾单独一个 \r：可能是被劈开的 \r\n，等下一片再说。
        if (m[0] === "\r" && m.index === buffer.length - 1) break;
        line(buffer.slice(0, m.index));
        buffer = buffer.slice(m.index + m[0].length);
      }
    },
    end() {
      if (buffer !== "") {
        line(buffer.replace(/\r$/, ""));
        buffer = "";
      }
      dispatch();
    },
  };
}
