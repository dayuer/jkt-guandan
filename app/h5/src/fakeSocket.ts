import { vi } from "vitest";

/**
 * 测试用的假 WebSocket（只有测试会 import 它）。
 *
 * realtime.ts 里的连接是全站共用的单例，页面一渲染就会去连 /ws。测试里换成它有两个原因：
 * 一是不要真的去连一个不存在的服务（jsdom 的 WebSocket 会一直重连、还会定时轮询），
 * 二是要把「收到某条消息」「连不上掉线」这两件事变成测试能手动驱动的动作。
 */
export class FakeSocket {
  /** 这个用例里创建过的连接，按创建顺序。 */
  static created: FakeSocket[] = [];

  readyState = 0;
  /** 客户端发过来的订阅指令，原样记下来给断言看。 */
  sent: { op: string; topics: string[] }[] = [];

  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;

  constructor(public readonly url: string) {
    FakeSocket.created.push(this);
  }

  send(data: string): void {
    this.sent.push(JSON.parse(data));
  }

  close(): void {
    this.readyState = 3;
  }

  // ── 测试驱动用 ──────────────────────────────────────────────

  /** 连上（realtime.ts 会在这时把已登记的主题发过来）。 */
  open(): void {
    this.readyState = 1;
    this.onopen?.({});
  }

  /** 服务端推来一条消息。 */
  receive(msg: unknown): void {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }

  /** 掉线。code 默认 1006（异常断开），1012 / 4001 有专门的处理。 */
  drop(code = 1006): void {
    this.readyState = 3;
    this.onclose?.({ code });
  }

  /** 现在实际订阅着的主题。 */
  get topics(): Set<string> {
    const live = new Set<string>();
    for (const msg of this.sent) {
      for (const t of msg.topics) {
        if (msg.op === "sub") live.add(t);
        else live.delete(t);
      }
    }
    return live;
  }
}

/** 把全局 WebSocket 换成 FakeSocket，并清空上一条测试留下的连接。 */
export function installFakeSocket(): void {
  FakeSocket.created = [];
  vi.stubGlobal("WebSocket", FakeSocket);
}

/** 最近创建的那条连接——单例模式下，页面用的就是它。 */
export function lastSocket(): FakeSocket {
  const s = FakeSocket.created[FakeSocket.created.length - 1];
  if (!s) throw new Error("还没有创建任何 WebSocket");
  return s;
}
