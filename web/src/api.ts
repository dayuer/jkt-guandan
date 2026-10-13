/**
 * 后端接口的统一封装（docs/server-design.md §6、§10.5、§11.4）。
 *
 * 三条约定必须在这里统一保证，散到各个页面里迟早会漏：
 *
 *  1. **会改数据的请求一律带 `Content-Type: application/json`**——CSRF 中间件这么要求，
 *     而且**没有请求体也要带**（`POST /api/claim`、`DELETE /api/games/{code}/seat` 都是这种）。
 *  2. **每次「提交」都生成一个新的 `Idempotency-Key`**（§11.4）：微信里双击、
 *     或者超时之后用户再点一次，都很常见，而开局本身不幂等。
 *  3. **修改牌局带 `If-Match: <version>`**（§11.3），版本不一致时服务端返回 409 和最新数据。
 */

/** ApiError 保留服务端的错误码，页面据此决定怎么提示。 */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    /** 409 的响应体会带上最新的牌局，见 updateGame。 */
    readonly body: any = null,
  ) {
    super(message);
    this.name = "ApiError";
  }

  /** 牌局被别人改过了（§11.3）。 */
  get isVersionConflict(): boolean {
    return this.status === 409 && this.code === "version_conflict";
  }

  /** 这一局已经结束或取消。 */
  get isClosed(): boolean {
    return this.status === 409 && this.code === "game_closed";
  }

  /**
   * 内容是敏感词时，服务端告诉我们命中的是哪个字段（venue / note），
   * 用来把提示放到对应的输入框下面（原型 X1）。其他错误返回空串。
   */
  get field(): string {
    const field = this.body?.error?.field;
    return typeof field === "string" ? field : "";
  }
}

interface RequestOptions {
  body?: unknown;
  headers?: Record<string, string>;
}

/**
 * 过载/写锁超时（§12.3）时要不要自动重试。
 *
 * 页面靠它把按钮文案换成「人有点多，正在重试…」（原型 X4）。
 */
let retrying = false;
const retryListeners = new Set<(value: boolean) => void>();

/** 订阅「正在自动重试」的状态，返回取消订阅的函数。 */
export function subscribeRetry(fn: (value: boolean) => void): () => void {
  retryListeners.add(fn);
  fn(retrying);
  return () => {
    retryListeners.delete(fn);
  };
}

function setRetrying(value: boolean) {
  if (retrying === value) return;
  retrying = value;
  retryListeners.forEach((fn) => fn(value));
}

/** 服务端说多久后才能再试（Retry-After，秒）。缺省 1 秒，最多等 10 秒。 */
function retryAfterMs(resp: Response): number {
  const raw = resp.headers?.get?.("Retry-After");
  const seconds = raw ? Number(raw) : NaN;
  if (!Number.isFinite(seconds) || seconds <= 0) return 1000;
  return Math.min(seconds, 10) * 1000;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { ...opts.headers };
  // 会改数据的请求一律带上，哪怕没有请求体。
  if (method !== "GET" && method !== "HEAD") {
    headers["content-type"] = "application/json";
  }

  const send = () =>
    fetch(path, {
      method,
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    });

  // 会改数据的请求遇到 503（写锁等待超时或过载）自动重试一次。
  // 请求体、幂等键、If-Match 都没变，所以重试不会重复建局（§11.4）。
  let resp = await send();
  if (resp.status === 503 && method !== "GET" && method !== "HEAD") {
    setRetrying(true);
    try {
      await sleep(retryAfterMs(resp));
      resp = await send();
    } finally {
      setRetrying(false);
    }
  }

  const text = await resp.text();
  let parsed: any = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }

  if (!resp.ok) {
    const err = parsed?.error ?? {};
    throw new ApiError(resp.status, err.code ?? "unknown", err.message ?? "请求失败", parsed);
  }
  return parsed as T;
}

/** 每次「提交」都换一个新键（§11.4）。 */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

// ── 类型（和 internal/games/handlers.go 的 JSON 一一对应）────────────────────

export interface UserRef {
  id: string;
  nickname: string;
}

export interface Game {
  id: string;
  short_code: string;
  starts_at: number;
  area: string;
  venue: string;
  kind: string;
  note?: string;
  status: "open" | "full" | "cancelled" | "done";
  version: number;
  capacity: number;
}

export interface Seat {
  seat_no: number;
  user: UserRef;
}

export interface WaitEntry {
  position: number;
  user: UserRef;
}

/** 当前用户在这一局里的位置：seat / waitlist / none。 */
export interface Me {
  role: "seat" | "waitlist" | "none";
  seat_no?: number;
  position?: number;
}

export interface GameDetail {
  game: Game;
  host: UserRef;
  seats: Seat[];
  waitlist: WaitEntry[];
  missing: number;
  me: Me;
}

export interface GameSummary {
  short_code: string;
  starts_at: number;
  area: string;
  venue: string;
  kind: string;
  status: Game["status"];
  /** 已坐下的座位（首页卡片的座位圆点要用首字）。 */
  seat_list: Seat[];
  seats: number;
  missing: number;
  host: UserRef;
}

export interface Identity {
  user: UserRef;
}

export interface Device {
  id: string;
  user_agent?: string;
  last_seen: number;
}

export interface MeResponse {
  user: UserRef;
  /** 当前这台设备的 id（用来「退出当前设备」）。 */
  device_id: string;
  devices: Device[];
}

// ── 牌局 ────────────────────────────────────────────────────────────────────

export interface GameListFilter {
  area?: string;
  day?: "today" | "tomorrow" | "weekend" | "";
}

export async function listGames(filter: GameListFilter = {}): Promise<GameSummary[]> {
  const q = new URLSearchParams();
  if (filter.area) q.set("area", filter.area);
  if (filter.day) q.set("day", filter.day);
  const suffix = q.toString();
  const body = await request<{ games: GameSummary[] }>("GET", `/api/games${suffix ? `?${suffix}` : ""}`);
  return body.games ?? [];
}

export function getGame(code: string): Promise<GameDetail> {
  return request<GameDetail>("GET", `/api/games/${encodeURIComponent(code)}`);
}

export interface CreateGameInput {
  starts_at: number;
  area: string;
  venue: string;
  kind: string;
  note?: string;
}

/**
 * 开一局。`idempotencyKey` 由调用方在**开始这次提交时**生成一次并保存好，
 * 重试要复用同一个键，否则开两次局（§11.4）。
 */
export function createGame(input: CreateGameInput, idempotencyKey: string): Promise<GameDetail> {
  return request<GameDetail>("POST", "/api/games", {
    body: input,
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function joinGame(code: string): Promise<GameDetail> {
  return request<GameDetail>("POST", `/api/games/${encodeURIComponent(code)}/seat`);
}

export function leaveGame(code: string): Promise<GameDetail> {
  return request<GameDetail>("DELETE", `/api/games/${encodeURIComponent(code)}/seat`);
}

export function kickPlayer(code: string, userID: string): Promise<GameDetail> {
  return request<GameDetail>("POST", `/api/games/${encodeURIComponent(code)}/kick`, {
    body: { user_id: userID },
  });
}

export function cancelGame(code: string): Promise<{ status: string }> {
  return request<{ status: string }>("POST", `/api/games/${encodeURIComponent(code)}/cancel`);
}

export interface GamePatch {
  starts_at?: number;
  area?: string;
  venue?: string;
  kind?: string;
  note?: string;
}

/**
 * 修改牌局（§11.3）。版本不一致时抛 ApiError，`isVersionConflict` 为真，
 * 且 `body` 里带着最新的牌局，前端据此走 K4/K5 的冲突处理。
 */
export function updateGame(code: string, patch: GamePatch, version: number): Promise<GameDetail> {
  return request<GameDetail>("PATCH", `/api/games/${encodeURIComponent(code)}`, {
    body: patch,
    headers: { "If-Match": String(version) },
  });
}

// ── 身份 ────────────────────────────────────────────────────────────────────

export function getMe(): Promise<MeResponse> {
  return request<MeResponse>("GET", "/api/me");
}

/** 首次填称呼（§5.2）。回来之后 cookie 里就有身份了。 */
export function createMe(nickname: string): Promise<Identity> {
  return request<Identity>("POST", "/api/me", { body: { nickname } });
}

export function signOutDevice(deviceID: string): Promise<void> {
  return request<void>("DELETE", `/api/me/devices/${encodeURIComponent(deviceID)}`);
}

/**
 * 「不是我」：把当前这台设备退出，回到没有身份的状态（#36 第 1 条）。
 *
 * 自动兑换 claim 链接是 login CSRF 的入口——攻击者把链接发给受害者，
 * 受害者一打开就成了攻击者的身份。给一个立刻退出的出口来缓解。
 */
export async function signOutCurrentDevice(): Promise<void> {
  const me = await getMe();
  await signOutDevice(me.device_id);
}
