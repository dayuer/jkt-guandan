import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  BACKOFF_MAX_MS,
  POLL_INTERVAL_MS,
  RealtimeClient,
  backoffDelay,
  type RealtimeStatus,
  type SocketLike,
} from "./realtime";

class FakeSocket implements SocketLike {
  readyState = 0;
  sent: unknown[] = [];
  closed = false;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;

  send(data: string): void {
    this.sent.push(JSON.parse(data));
  }
  close(): void {
    this.closed = true;
  }
  // 测试驱动：
  open(): void {
    this.readyState = 1;
    this.onopen?.({});
  }
  receive(msg: unknown): void {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
  drop(code = 1006): void {
    this.readyState = 3;
    this.onclose?.({ code });
  }
}

let sockets: FakeSocket[];
let visible: boolean;
let visibilityHandler: (() => void) | null;

function client(): RealtimeClient {
  return new RealtimeClient({
    url: "wss://guandan.test/ws",
    deps: {
      createSocket: () => {
        const s = new FakeSocket();
        sockets.push(s);
        return s;
      },
      setTimeout: (fn, ms) => setTimeout(fn, ms),
      clearTimeout: (id) => clearTimeout(id as ReturnType<typeof setTimeout>),
      setInterval: (fn, ms) => setInterval(fn, ms),
      clearInterval: (id) => clearInterval(id as ReturnType<typeof setInterval>),
      random: () => 1, // 抖动取上限，延迟可预测
      isVisible: () => visible,
      onVisibilityChange: (fn) => {
        visibilityHandler = fn;
        return () => {
          visibilityHandler = null;
        };
      },
    },
  });
}

const last = () => sockets[sockets.length - 1];

beforeEach(() => {
  vi.useFakeTimers();
  sockets = [];
  visible = true;
  visibilityHandler = null;
});
afterEach(() => {
  vi.useRealTimers();
});

describe("backoffDelay", () => {
  it("1 秒起步、翻倍、上限 30 秒，抖动在 [一半, 全部] 之间", () => {
    expect(backoffDelay(0, () => 1)).toBe(1000);
    expect(backoffDelay(0, () => 0)).toBe(500);
    expect(backoffDelay(3, () => 1)).toBe(8000);
    expect(backoffDelay(10, () => 1)).toBe(BACKOFF_MAX_MS);
    expect(backoffDelay(10, () => 0)).toBe(BACKOFF_MAX_MS / 2);
  });
});

describe("RealtimeClient", () => {
  it("全站只开一条连接；连上后按登记的主题订阅，引用计数归零才退订", () => {
    const rt = client();
    const a = rt.register({ topics: ["game:8K2", "feed:SCBD"] });
    rt.register({ topics: ["game:8K2"] });
    rt.start();
    expect(sockets).toHaveLength(1);
    last().open();
    expect(last().sent).toEqual([{ op: "sub", topics: ["game:8K2", "feed:SCBD"] }]);

    a(); // game:8K2 还有另一处登记，只退订 feed:SCBD
    expect(last().sent.at(-1)).toEqual({ op: "unsub", topics: ["feed:SCBD"] });
    rt.stop();
  });

  it("事件按主题分发，丢弃版本号不比本地新的（乱序、重复）", () => {
    const rt = client();
    const got: unknown[] = [];
    rt.register({ topics: ["game:8K2"], onEvent: (ev) => got.push([ev.type, ev.v]) });
    rt.register({ topics: ["feed:SCBD"], onEvent: () => got.push("feed") });
    rt.start();
    last().open();
    last().receive({ topic: "game:8K2", type: "seat.taken", v: 7 });
    last().receive({ topic: "game:8K2", type: "seat.taken", v: 7 }); // 重复
    last().receive({ topic: "game:8K2", type: "seat.left", v: 6 }); // 乱序的旧消息
    last().receive({ topic: "game:8K2", type: "game.full", v: 8 });
    last().receive({ type: "subscribed", topics: ["game:8K2"] }); // 回执不分发
    expect(got).toEqual([
      ["seat.taken", 7],
      ["game.full", 8],
    ]);
    rt.stop();
  });

  it("hello 带构建号，交给页面判断要不要提示刷新", () => {
    const rt = client();
    const hellos: unknown[] = [];
    rt.onHello((h) => hellos.push(h.build));
    rt.start();
    last().open();
    last().receive({ type: "hello", build: "abc", server_time: 1 });
    expect(hellos).toEqual(["abc"]);
    rt.stop();
  });

  it("断线后按退避重连；重连成功后重新订阅并刷新 REST", () => {
    const rt = client();
    const refresh = vi.fn();
    rt.register({ topics: ["user:u1"], refresh });
    rt.start();
    last().open();
    expect(refresh).not.toHaveBeenCalled(); // 第一次连上时页面刚拉过 REST

    last().drop();
    expect(rt.status).toBe("connecting");
    vi.advanceTimersByTime(999);
    expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);

    last().open();
    expect(last().sent).toEqual([{ op: "sub", topics: ["user:u1"] }]);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(rt.status).toBe("online");
    rt.stop();
  });

  it("连续 3 次连不上：降级为每 15 秒轮询（只轮询 poll 的页面），连上后停止轮询", () => {
    const rt = client();
    const statuses: RealtimeStatus[] = [];
    rt.onStatus((s) => statuses.push(s));
    const feedRefresh = vi.fn();
    const meRefresh = vi.fn();
    rt.register({ topics: ["feed:SCBD"], refresh: feedRefresh, poll: true });
    rt.register({ topics: ["user:u1"], refresh: meRefresh });
    rt.start();

    last().drop(); // 第 1 次失败
    vi.advanceTimersByTime(backoffDelay(1, () => 1));
    last().drop(); // 第 2 次
    vi.advanceTimersByTime(backoffDelay(2, () => 1));
    expect(rt.status).toBe("connecting");
    last().drop(); // 第 3 次
    expect(rt.status).toBe("offline");

    vi.advanceTimersByTime(POLL_INTERVAL_MS);
    expect(feedRefresh).toHaveBeenCalledTimes(1);
    expect(meRefresh).not.toHaveBeenCalled();

    // 后台继续重连；连上后恢复在线、不再轮询。
    vi.advanceTimersByTime(backoffDelay(3, () => 1));
    last().open();
    expect(rt.status).toBe("online");
    const before = feedRefresh.mock.calls.length;
    vi.advanceTimersByTime(POLL_INTERVAL_MS * 3);
    expect(feedRefresh.mock.calls.length).toBe(before);
    expect(statuses).toEqual(["offline", "online"]);
    rt.stop();
  });

  it("身份失效（4001）：通知页面重新拉身份，并很快重连", () => {
    const rt = client();
    const revoked = vi.fn();
    rt.onIdentityRevoked(revoked);
    rt.start();
    last().open();
    last().drop(4001);
    expect(revoked).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1000);
    expect(sockets).toHaveLength(2);
    rt.stop();
  });

  it("服务端重启（1012）后很快重连", () => {
    const rt = client();
    rt.start();
    last().open();
    last().drop(1012);
    vi.advanceTimersByTime(1000);
    expect(sockets).toHaveLength(2);
    rt.stop();
  });

  it("回到前台时，如果连接已断，立刻重连并刷新", () => {
    const rt = client();
    const refresh = vi.fn();
    rt.register({ topics: ["game:8K2"], refresh });
    rt.start();
    last().open();
    last().drop(); // 页面在后台被挂起，连接断了

    visible = false;
    visibilityHandler?.();
    expect(sockets).toHaveLength(1); // 后台不重连（交给退避计时器）

    visible = true;
    visibilityHandler?.();
    expect(sockets).toHaveLength(2);
    expect(refresh).toHaveBeenCalledTimes(1);

    // 原来的退避计时器已取消，不会再多开一条。
    vi.advanceTimersByTime(BACKOFF_MAX_MS);
    expect(sockets).toHaveLength(2);
    rt.stop();
  });

  it("没连上时登记的主题，连上后一并订阅", () => {
    const rt = client();
    rt.start();
    rt.register({ topics: ["game:8K2"] });
    expect(last().sent).toEqual([]);
    last().open();
    expect(last().sent).toEqual([{ op: "sub", topics: ["game:8K2"] }]);
    rt.stop();
  });
});
