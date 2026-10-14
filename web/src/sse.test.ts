import { describe, expect, it } from "vitest";
import { createSSEParser, type SSEEvent } from "./sse";

function parse(chunks: string[]): SSEEvent[] {
  const out: SSEEvent[] = [];
  const p = createSSEParser((e) => out.push(e));
  chunks.forEach((c) => p.push(c));
  p.end();
  return out;
}

const STREAM =
  'event: reply\ndata: {"text":"好嘞"}\n\n' +
  ': ping\n\n' +
  'event: reply\ndata: {"text":"，周四"}\n\n' +
  'event: draft\ndata: {"draft":{"area":"PIK"},"valid":true}\n\n';

const EXPECTED: SSEEvent[] = [
  { event: "reply", data: '{"text":"好嘞"}' },
  { event: "reply", data: '{"text":"，周四"}' },
  { event: "draft", data: '{"draft":{"area":"PIK"},"valid":true}' },
];

describe("createSSEParser", () => {
  it("整段一次喂进来", () => {
    expect(parse([STREAM])).toEqual(EXPECTED);
  });

  it("任意位置切开都得到同样的结果", () => {
    for (let i = 1; i < STREAM.length; i++) {
      expect(parse([STREAM.slice(0, i), STREAM.slice(i)])).toEqual(EXPECTED);
    }
  });

  it("逐字喂", () => {
    expect(parse(STREAM.split(""))).toEqual(EXPECTED);
  });

  it("CRLF，并且 \\r\\n 被劈在两片之间", () => {
    const crlf = STREAM.replace(/\n/g, "\r\n");
    for (let i = 1; i < crlf.length; i++) {
      expect(parse([crlf.slice(0, i), crlf.slice(i)])).toEqual(EXPECTED);
    }
  });

  it("多行 data 用换行拼起来；没有 event 字段时是 message", () => {
    expect(parse(["data: a\ndata: b\n\n"])).toEqual([{ event: "message", data: "a\nb" }]);
  });

  it("最后一个事件没有空行收尾，end 时也交出去", () => {
    expect(parse(['event: error\ndata: {"code":"x"}'])).toEqual([{ event: "error", data: '{"code":"x"}' }]);
  });

  it("只有 event 没有 data 的块不派发", () => {
    expect(parse(["event: reply\n\n"])).toEqual([]);
  });
});
