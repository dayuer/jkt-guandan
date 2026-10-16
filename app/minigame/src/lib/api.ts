// 后端接口（和 app/h5/src/api.ts 同一套 JSON API），走 wx.request + Authorization: Bearer（#84）。
// H5 那边的三条约定这里同样要守：
//   1. 会改数据的请求一律带 Content-Type: application/json，没有请求体也带；
//   2. 每次「提交」一个新的 Idempotency-Key；
//   3. 修改牌局带 If-Match: <version>，409 时响应体里有最新数据。
//
// #253 P5 起，类型和 ApiError 从 app/shared/src/api.ts 拿（不再反向 import app/h5），
// 这一份只剩传输：前缀、Bearer 凭证、401 通知、503 重试。

import { ApiError } from "../../../shared/src/api";
import type {
  ClaimTicket,
  CreateGameInput,
  Friend,
  GameDetail,
  GameListFilter,
  GamePatch,
  GameSummary,
  GroupInfo,
  InviteResult,
  MeResponse,
  UserRef,
} from "../../../shared/src/api";
import type { RoundFact, Score } from "./scoring";

// 类型、ApiError 和邀请的两个纯文案函数住 app/shared（#253 P5）；这里只做转出，
// 小游戏各处的 import 路径不用动。传输仍是这一份 wx.request（前缀、Bearer、401 都在这边）。
export * from "../../../shared/src/api";

export const API_BASE = __API_BASE__;

// ── 凭证：device_token 存在本地存储里，只在请求头里出现 ──

const TOKEN_KEY = "gd_device_token";
let token: string = (() => {
  try {
    return wx.getStorageSync(TOKEN_KEY) || "";
  } catch {
    return "";
  }
})();
const unauthorizedListeners = new Set<() => void>();

export function getToken(): string {
  return token;
}
export function setToken(t: string): void {
  token = t;
  if (t) wx.setStorageSync(TOKEN_KEY, t);
  else {
    try {
      wx.removeStorageSync(TOKEN_KEY);
    } catch {
      // ignore
    }
  }
}
/** 服务端说凭证无效（401）：设备被退掉、注销了。 */
export function onUnauthorized(fn: () => void): () => void {
  unauthorizedListeners.add(fn);
  return () => unauthorizedListeners.delete(fn);
}

// ── 503 自动重试一次（§12.3），按钮据此显示「人有点多，正在重试…」──

let retrying = false;
const retryListeners = new Set<(v: boolean) => void>();
export function subscribeRetry(fn: (v: boolean) => void): () => void {
  retryListeners.add(fn);
  fn(retrying);
  return () => retryListeners.delete(fn);
}
export function isRetrying(): boolean {
  return retrying;
}
function setRetrying(v: boolean) {
  if (retrying === v) return;
  retrying = v;
  retryListeners.forEach((fn) => fn(v));
}

interface RawResponse {
  status: number;
  data: any;
  header: Record<string, string>;
}

function send(method: string, path: string, body: unknown, headers: Record<string, string>): Promise<RawResponse> {
  return new Promise((resolve, reject) => {
    wx.request({
      url: API_BASE + path,
      method,
      header: headers,
      data: body === undefined ? undefined : JSON.stringify(body),
      dataType: "text",
      responseType: "text",
      timeout: 15000,
      success: (res: any) => resolve({ status: res.statusCode, data: res.data, header: res.header || {} }),
      fail: (err: any) => reject(new ApiError(0, "network", "网络不通，稍后再试", err)),
    });
  });
}

function retryAfterMs(h: Record<string, string>): number {
  const raw = h["Retry-After"] ?? h["retry-after"];
  const s = raw ? Number(raw) : NaN;
  return !Number.isFinite(s) || s <= 0 ? 1000 : Math.min(s, 10) * 1000;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export async function request<T>(method: string, path: string, opts: { body?: unknown; headers?: Record<string, string> } = {}): Promise<T> {
  const headers: Record<string, string> = { ...opts.headers };
  if (method !== "GET") headers["content-type"] = "application/json";
  if (token) headers["Authorization"] = `Bearer ${token}`;
  let resp = await send(method, path, opts.body, headers);
  if (resp.status === 503 && method !== "GET") {
    setRetrying(true);
    try {
      await sleep(retryAfterMs(resp.header));
      resp = await send(method, path, opts.body, headers);
    } finally {
      setRetrying(false);
    }
  }
  let parsed: any = null;
  if (typeof resp.data === "string" && resp.data) {
    try {
      parsed = JSON.parse(resp.data);
    } catch {
      parsed = null;
    }
  } else if (resp.data && typeof resp.data === "object") parsed = resp.data;

  if (resp.status < 200 || resp.status >= 300) {
    const e = parsed?.error ?? {};
    if (resp.status === 401 && token) unauthorizedListeners.forEach((fn) => fn());
    throw new ApiError(resp.status, e.code ?? "unknown", e.message ?? "请求失败", parsed);
  }
  return parsed as T;
}

export function newIdempotencyKey(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
}

const enc = encodeURIComponent;

// ── 小游戏登录（#84）──

export interface WxLoginResponse {
  user?: UserRef;
  device_token?: string;
  device_id?: string;
  need_nickname?: boolean;
}
export function wxLogin(code: string, nickname?: string): Promise<WxLoginResponse> {
  return request("POST", "/api/wx/login", { body: nickname ? { code, nickname } : { code } });
}
/** 凭 H5 上拿到的 6 位认领码，把这个微信身份接到网页上的那个人身上。 */
export function wxLink(code: string, wxCode: string, confirm = false): Promise<WxLoginResponse> {
  return request("POST", "/api/wx/link", { body: confirm ? { code, wx_code: wxCode, confirm: true } : { code, wx_code: wxCode } });
}

// ── 牌局 ──

export async function listGames(f: GameListFilter = {}): Promise<GameSummary[]> {
  const q: string[] = [];
  if (f.area) q.push(`area=${enc(f.area)}`);
  if (f.day) q.push(`day=${enc(f.day)}`);
  const body = await request<{ games: GameSummary[] }>("GET", `/api/games${q.length ? "?" + q.join("&") : ""}`);
  return body?.games ?? [];
}
export const getGame = (code: string) => request<GameDetail>("GET", `/api/games/${enc(code)}`);
export const createGame = (input: CreateGameInput, key: string) =>
  request<GameDetail>("POST", "/api/games", { body: input, headers: { "Idempotency-Key": key } });
export const joinGame = (code: string) => request<GameDetail>("POST", `/api/games/${enc(code)}/seat`);
export const leaveGame = (code: string) => request<GameDetail>("DELETE", `/api/games/${enc(code)}/seat`);
export const kickPlayer = (code: string, userID: string) =>
  request<GameDetail>("POST", `/api/games/${enc(code)}/kick`, { body: { user_id: userID } });
export const cancelGame = (code: string) => request<{ status: string }>("POST", `/api/games/${enc(code)}/cancel`);
export const updateGame = (code: string, patch: GamePatch, version: number) =>
  request<GameDetail>("PATCH", `/api/games/${enc(code)}`, { body: patch, headers: { "If-Match": String(version) } });

// ── 牌桌计分（#165，docs/server-design.md §5.6）──
// 四个接口都返回整份计分；409 round_taken / round_changed 的响应体里也有（err.body）。

export const getScore = (code: string) => request<Score>("GET", `/api/games/${enc(code)}/score`);
// 开始记分没有请求参数了（#205：不再有「打 A 几次不过回到 2」这个约定），带 Idempotency-Key 就行。
export const startScore = (code: string, key: string) =>
  request<Score>("POST", `/api/games/${enc(code)}/score`, { body: {}, headers: { "Idempotency-Key": key } });
export const recordRound = (code: string, expectedRoundNo: number, fact: RoundFact, key: string) =>
  request<Score>("POST", `/api/games/${enc(code)}/score/rounds`, {
    body: { expected_round_no: expectedRoundNo, head_seat: fact.head_seat, partner_place: fact.partner_place },
    headers: { "Idempotency-Key": key },
  });
export const editRound = (code: string, no: number, fact: RoundFact, was?: RoundFact) =>
  request<Score>("PUT", `/api/games/${enc(code)}/score/rounds/${no}`, {
    body: { head_seat: fact.head_seat, partner_place: fact.partner_place, ...(was ? { was: { head_seat: was.head_seat, partner_place: was.partner_place } } : {}) },
  });

// ── 身份 ──

export const getMe = () => request<MeResponse>("GET", "/api/me");
export const signOutDevice = (id: string) => request<void>("DELETE", `/api/me/devices/${enc(id)}`);
export const deleteMe = () => request<void>("DELETE", "/api/me");
export const issueClaim = () => request<ClaimTicket>("POST", "/api/claim");
export const setWaPhone = (phone: string) => request<{ wa_phone: string }>("PUT", "/api/me/wa", { body: { phone } });
export const clearWaPhone = () => request<void>("DELETE", "/api/me/wa");
/** 改称呼（#210）。返回的是改完之后的 user，直接拿去刷缓存。 */
export const setNickname = (nickname: string) => request<{ user: UserRef }>("PUT", "/api/me/nickname", { body: { nickname } });

export async function signOutOtherDevices(): Promise<number> {
  const me = await getMe();
  const others = me.devices.filter((d) => d.id !== me.device_id);
  for (const d of others) await signOutDevice(d.id);
  return others.length;
}

// ── 我的牌局（#211）──

/** 历史里记过分的局带的比分摘要（和计分页同一套算法）。 */
export interface ScoreBrief {
  kind: string;
  rounds: number;
  /** 过了 A 的那一队；没打完没有。 */
  winner?: "red" | "black";
  red_level: number;
  black_level: number;
  red_double_ups: number;
  black_double_ups: number;
  red_wins: number;
  black_wins: number;
}
export type HistoryGame = GameSummary & { score?: ScoreBrief };
export interface HistoryPage { games: HistoryGame[]; has_more: boolean }

/** 要打的：我开的 / 已报名 / 候补中，还没开始的局，从近到远。 */
export async function listMyGames(): Promise<GameSummary[]> {
  return (await request<{ games: GameSummary[] }>("GET", "/api/me/games"))?.games ?? [];
}
/** 历史：开过或坐过、已开始或已取消的局，从近到远。after 是上一页最后一条，翻下一页时传。 */
export async function listMyHistory(after?: HistoryGame): Promise<HistoryPage> {
  const q = after ? `?before=${after.starts_at}&before_code=${enc(after.short_code)}` : "";
  const body = await request<HistoryPage>("GET", `/api/me/games/history${q}`);
  return { games: body?.games ?? [], has_more: !!body?.has_more };
}
/** 从我自己的历史里删掉一局：牌局本身和同局别人都不受影响。 */
export const hideFromHistory = (code: string) => request<void>("DELETE", `/api/me/games/history/${enc(code)}`);

// ── 牌友 ──

export async function listFriends(): Promise<Friend[]> {
  const body = await request<{ friends: Friend[] }>("GET", "/api/friends");
  return body?.friends ?? [];
}
export const setStar = (userID: string, starred: boolean) =>
  request<void>(starred ? "PUT" : "DELETE", `/api/follows/${enc(userID)}`);

/** 订阅消息模板 ID（#137），按用途：full 成局 / 有人退出，remind 开局前 1 小时 / 改时间。只有服务端配置了的。 */
export interface WxSubscribeTemplates {
  full?: string;
  remind?: string;
}
export interface PublicConfig {
  group: GroupInfo;
  wx_subscribe: WxSubscribeTemplates;
}
export async function getPublicConfig(): Promise<PublicConfig> {
  const body = await request<{ group?: Partial<GroupInfo>; wx_subscribe?: Record<string, unknown> }>("GET", "/api/config");
  const wx = body?.wx_subscribe ?? {};
  const pick = (v: unknown) => (typeof v === "string" && v ? v : undefined);
  return {
    group: { wechat_id: body?.group?.wechat_id ?? "", whatsapp_group_url: body?.group?.whatsapp_group_url ?? "" },
    wx_subscribe: { full: pick(wx.full), remind: pick(wx.remind) },
  };
}
/** 上报 wx.requestSubscribeMessage 里同意了的模板，服务端给每个模板记一次可发次数。 */
export const reportWxSubscribe = (accepted: string[]) =>
  request<{ added: number }>("POST", "/api/wx/subscribe", { body: { accepted } });

export async function getGroupInfo(): Promise<GroupInfo> {
  const body = await request<{ group?: Partial<GroupInfo> }>("GET", "/api/config");
  return { wechat_id: body?.group?.wechat_id ?? "", whatsapp_group_url: body?.group?.whatsapp_group_url ?? "" };
}

export async function inviteFriends(code: string, userIDs: string[]): Promise<InviteResult> {
  const body = await request<Partial<InviteResult>>("POST", `/api/games/${enc(code)}/invites`, { body: { user_ids: userIDs } });
  return { invited: body?.invited ?? [], skipped: body?.skipped ?? [] };
}

// ── 页面链接（分享话术、日历、海报都指向 H5 站点）──

export const shareURL = (code: string) => `${API_BASE}/g/${enc(code)}`;
export const posterURL = (code: string, version: number) => `${API_BASE}/g/${enc(code)}/poster.png?v=${version}`;
