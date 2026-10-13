/**
 * 全站共用的一条 WebSocket 连接（docs/server-design.md §5.5「客户端约定」）。
 *
 * - 各页面用 register() 登记自己关心的主题，离开页面时调用返回的函数注销；同一个主题被多处登记时按引用计数，
 *   最后一处注销才真正退订。
 * - **REST 是事实来源**：WebSocket 只说「哪里变了」。断线重连后、页面回到前台时，调用各页面的 refresh()
 *   重新拉一次 REST，不依赖补发历史消息。
 * - 断线重连：指数退避，1 秒起步、上限 30 秒，加随机抖动（部署后所有客户端不会同一刻挤进来）。
 * - visibilitychange：页面回到前台时如果连接已断，立刻重连并刷新。
 * - 降级：连续 3 次连不上，改为每 15 秒轮询一次 REST（只有登记时 poll: true 的页面，即 02 和 03），
 *   状态变成 "offline"，页面据此悄悄标一个「离线」小点；后台继续按退避重连，连上后自动恢复。
 * - game:{code} 上的消息带牌局版本号 v：收到 v 不大于本地已见过的版本时直接丢弃，乱序、重复都不会出错。
 *   feed:{area} 上的事件来自不同的局，不按版本去重（页面收到后重新拉列表）。
 */

export interface RealtimeEvent {
  topic?: string;
  type: string;
  v?: number;
  data?: unknown;
}

export type RealtimeStatus = "connecting" | "online" | "offline";

export interface Registration {
  topics: string[];
  onEvent?: (ev: RealtimeEvent) => void;
  /** 重新拉一次本页的 REST。重连成功、回到前台、轮询时调用。 */
  refresh?: () => void;
  /** 连不上时是否参与轮询降级（只有 02 首页和 03 牌局详情需要）。 */
  poll?: boolean;
}

/** 浏览器 WebSocket 里我们用到的部分，测试里换成假的。 */
export interface SocketLike {
  readyState: number;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: { code: number }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
}

export interface RealtimeDeps {
  createSocket: (url: string) => SocketLike;
  setTimeout: (fn: () => void, ms: number) => unknown;
  clearTimeout: (id: unknown) => void;
  setInterval: (fn: () => void, ms: number) => unknown;
  clearInterval: (id: unknown) => void;
  random: () => number;
  /** 页面是否在前台，以及前后台切换的通知。 */
  isVisible: () => boolean;
  onVisibilityChange: (fn: () => void) => () => void;
}

export const BACKOFF_START_MS = 1000;
export const BACKOFF_MAX_MS = 30_000;
export const FAILURES_BEFORE_POLLING = 3;
export const POLL_INTERVAL_MS = 15_000;
const OPEN = 1;
/** 服务端断开时的关闭码：身份失效（#43）。 */
export const IDENTITY_REVOKED = 4001;

/** 第 attempt 次重连（从 0 开始）的等待时间：指数退避，取 [一半, 全部] 之间的随机值。 */
export function backoffDelay(attempt: number, random: () => number): number {
  const base = Math.min(BACKOFF_MAX_MS, BACKOFF_START_MS * 2 ** attempt);
  return Math.round(base / 2 + (random() * base) / 2);
}

function defaultDeps(): RealtimeDeps {
  return {
    createSocket: (url) => new WebSocket(url) as unknown as SocketLike,
    setTimeout: (fn, ms) => window.setTimeout(fn, ms),
    clearTimeout: (id) => window.clearTimeout(id as number),
    setInterval: (fn, ms) => window.setInterval(fn, ms),
    clearInterval: (id) => window.clearInterval(id as number),
    random: Math.random,
    isVisible: () => document.visibilityState === "visible",
    onVisibilityChange: (fn) => {
      document.addEventListener("visibilitychange", fn);
      return () => document.removeEventListener("visibilitychange", fn);
    },
  };
}

function defaultURL(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export class RealtimeClient {
  private readonly deps: RealtimeDeps;
  private readonly url: string;
  private readonly regs = new Set<Registration>();
  private readonly topicRefs = new Map<string, number>();
  private readonly lastV = new Map<string, number>();
  private readonly statusListeners = new Set<(s: RealtimeStatus) => void>();
  private readonly helloListeners = new Set<(hello: { build?: string; user_id?: string }) => void>();
  private readonly revokedListeners = new Set<() => void>();

  private socket: SocketLike | null = null;
  private opened = false; // 当前这条 socket 是否真的连上过
  private everConnected = false;
  private failures = 0;
  private reconnectTimer: unknown = null;
  private pollTimer: unknown = null;
  private unlistenVisibility: (() => void) | null = null;
  private started = false;
  private _status: RealtimeStatus = "connecting";

  constructor(opts: { url?: string; deps?: Partial<RealtimeDeps> } = {}) {
    this.deps = { ...defaultDeps(), ...opts.deps };
    this.url = opts.url ?? defaultURL();
  }

  get status(): RealtimeStatus {
    return this._status;
  }

  /** 开始连接。重复调用无效。 */
  start(): void {
    if (this.started) return;
    this.started = true;
    this.unlistenVisibility = this.deps.onVisibilityChange(() => this.handleVisibility());
    this.connect();
  }

  /** 断开并停止一切重连、轮询（测试和页面卸载时用）。 */
  stop(): void {
    this.started = false;
    this.unlistenVisibility?.();
    this.unlistenVisibility = null;
    this.clearReconnect();
    this.stopPolling();
    const s = this.socket;
    this.socket = null;
    if (s) {
      s.onclose = null;
      s.close(1000, "bye");
    }
  }

  /** 登记一组主题。返回注销函数。 */
  register(reg: Registration): () => void {
    this.regs.add(reg);
    const added: string[] = [];
    for (const t of reg.topics) {
      const n = this.topicRefs.get(t) ?? 0;
      this.topicRefs.set(t, n + 1);
      if (n === 0) added.push(t);
    }
    if (added.length) this.sendOp("sub", added);
    return () => {
      if (!this.regs.delete(reg)) return;
      const removed: string[] = [];
      for (const t of reg.topics) {
        const n = (this.topicRefs.get(t) ?? 1) - 1;
        if (n <= 0) {
          this.topicRefs.delete(t);
          this.lastV.delete(t);
          removed.push(t);
        } else {
          this.topicRefs.set(t, n);
        }
      }
      if (removed.length) this.sendOp("unsub", removed);
    };
  }

  onStatus(fn: (s: RealtimeStatus) => void): () => void {
    this.statusListeners.add(fn);
    return () => this.statusListeners.delete(fn);
  }

  /** hello 消息（带服务端构建号）。构建号和页面的不一致时，页面提示「有新版本，点我刷新」（§13.4）。 */
  onHello(fn: (hello: { build?: string; user_id?: string }) => void): () => void {
    this.helloListeners.add(fn);
    return () => this.helloListeners.delete(fn);
  }

  /**
   * 服务端以 4001 断开：这台设备的身份失效了（被「退出其他设备」、切换到别的身份、被封禁，#43）。
   * 页面应重新拉一次 GET /api/me；连接会以新身份自动重连。
   */
  onIdentityRevoked(fn: () => void): () => void {
    this.revokedListeners.add(fn);
    return () => this.revokedListeners.delete(fn);
  }

  // ---- 内部 ----

  private setStatus(s: RealtimeStatus): void {
    if (s === this._status) return;
    this._status = s;
    for (const fn of this.statusListeners) fn(s);
  }

  private connect(): void {
    if (!this.started || this.socket) return;
    this.clearReconnect();
    this.opened = false;
    if (this._status !== "offline") this.setStatus("connecting");
    const s = this.deps.createSocket(this.url);
    this.socket = s;
    s.onopen = () => this.handleOpen();
    s.onmessage = (ev) => this.handleMessage(ev.data);
    s.onclose = (ev) => this.handleClose(ev.code);
    s.onerror = () => {
      // onclose 随后会来，在那里统一处理。
    };
  }

  private handleOpen(): void {
    this.opened = true;
    this.failures = 0;
    this.stopPolling();
    this.setStatus("online");
    const topics = [...this.topicRefs.keys()];
    if (topics.length) this.sendOp("sub", topics);
    // 第一次连上时页面刚拉过 REST；之后每次重连都可能漏了中间的变化，重新拉一次。
    if (this.everConnected) this.refreshAll(false);
    this.everConnected = true;
  }

  private handleMessage(raw: unknown): void {
    let ev: RealtimeEvent;
    try {
      ev = JSON.parse(String(raw)) as RealtimeEvent;
    } catch {
      return;
    }
    if (ev.type === "hello") {
      for (const fn of this.helloListeners) fn(ev as { build?: string; user_id?: string });
      return;
    }
    if (!ev.topic) return; // subscribed / error 等回执
    // v 是「这一局」的版本号，只在 game:{code} 主题上能比大小。feed:{area} 上的事件来自片区里不同的局，
    // 各有各的版本，按主题去重会把别的局的更新（比如新开的局 v=1）当成旧消息丢掉。
    if (ev.topic.startsWith("game:") && typeof ev.v === "number" && ev.v > 0) {
      const seen = this.lastV.get(ev.topic) ?? 0;
      if (ev.v <= seen) return;
      this.lastV.set(ev.topic, ev.v);
    }
    for (const reg of this.regs) {
      if (reg.topics.includes(ev.topic)) reg.onEvent?.(ev);
    }
  }

  private handleClose(code: number): void {
    this.socket = null;
    if (!this.started) return;
    if (!this.opened) {
      this.failures++;
    }
    if (this.failures >= FAILURES_BEFORE_POLLING) {
      this.setStatus("offline");
      this.startPolling();
    } else if (this._status !== "offline") {
      this.setStatus("connecting");
    }
    if (code === IDENTITY_REVOKED) {
      for (const fn of this.revokedListeners) fn();
    }
    // 1012 是服务端重启（§12.4），新进程几秒内就起来；4001 是身份失效，换身份后马上能重连。
    // 这两种不必等太久，其他情况按退避。
    const quick = (code === 1012 || code === IDENTITY_REVOKED) && this.opened;
    const delay = quick ? backoffDelay(0, this.deps.random) : backoffDelay(this.failures, this.deps.random);
    this.reconnectTimer = this.deps.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private handleVisibility(): void {
    if (!this.started || !this.deps.isVisible()) return;
    if (this.socket) return; // 连着或正在连
    // 回到前台：立刻重连，并先刷新一次（后台期间可能错过了变化）。
    this.refreshAll(false);
    this.connect();
  }

  private sendOp(op: "sub" | "unsub", topics: string[]): void {
    const s = this.socket;
    if (s && s.readyState === OPEN) s.send(JSON.stringify({ op, topics }));
    // 没连上时不用发：连上后 handleOpen 会按当前的登记重新订阅。
  }

  private refreshAll(pollOnly: boolean): void {
    for (const reg of this.regs) {
      if (pollOnly && !reg.poll) continue;
      reg.refresh?.();
    }
  }

  private startPolling(): void {
    if (this.pollTimer !== null) return;
    this.pollTimer = this.deps.setInterval(() => this.refreshAll(true), POLL_INTERVAL_MS);
  }

  private stopPolling(): void {
    if (this.pollTimer === null) return;
    this.deps.clearInterval(this.pollTimer);
    this.pollTimer = null;
  }

  private clearReconnect(): void {
    if (this.reconnectTimer === null) return;
    this.deps.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }
}

let shared: RealtimeClient | null = null;

/** 全站共用的那一个连接。第一次调用时建立。 */
export function realtime(): RealtimeClient {
  if (!shared) {
    shared = new RealtimeClient();
    shared.start();
  }
  return shared;
}
