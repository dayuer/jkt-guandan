/**
 * 后端 JSON API 的**形状**（#253 P5）：两端共用的类型、`ApiError`、以及两个纯文案函数。
 *
 * 这一层只有**类型和纯函数** —— 没有 `fetch`、没有 `wx`、没有 `window`，也不 import 任何一端的
 * 模块。传输各端各留自己的那份：
 *
 * | | H5（`app/h5/src/api.ts`） | 小游戏（`app/minigame/src/lib/api.ts`） |
 * |---|---|---|
 * | 发请求 | `fetch` | `wx.request` |
 * | 路由前缀 | 同源（相对路径） | `__API_BASE__` |
 * | 凭证 | cookie | 本地存储里的 `device_token` + `Authorization: Bearer` |
 * | 503 自动重试 | `Response` 头 + `Retry-After` | 响应头小写化后取 `Retry-After` |
 *
 * 两边都还有自己的 `newIdempotencyKey()`：**实现故意不一样**（H5 优先用 `crypto.randomUUID()`，
 * 小游戏用时间戳 + 随机串），服务端只当它是不透明字符串，所以不强行合成一份。
 *
 * 路由、凭证、重试这三件事在两端都是各自实现的，唯一必须**两端一致**的是下面这些形状和错误语义
 * （和 `server/internal/games/handlers.go` 的 JSON 一一对应）。
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

// ── 类型（和 server/internal/games/handlers.go 的 JSON 一一对应）────────────────────

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
  /** 局头邀请过我（#115）。只有被邀请的人自己看得到。 */
  invited_by?: UserRef;
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
  /** 我在这一局里的位置（W4，#114）。服务端还没给时是 undefined，首页就不显示标签。 */
  my_role?: "seated" | "waitlist" | "none";
  /** my_role 是 waitlist 时，排第几位。 */
  waitlist_pos?: number;
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
  /** 绑定的 WhatsApp 号码（E.164，#116）。只返回给本人；没绑定时没有这个字段。 */
  wa_phone?: string;
}

// ── 牌局 ────────────────────────────────────────────────────────────────────

export interface GameListFilter {
  area?: string;
  day?: "today" | "tomorrow" | "weekend" | "";
}

export interface CreateGameInput {
  starts_at: number;
  area: string;
  venue: string;
  kind: string;
  note?: string;
}

export interface GamePatch {
  starts_at?: number;
  area?: string;
  venue?: string;
  kind?: string;
  note?: string;
}

// ── 认领（跳出微信、加到主屏幕时把身份带过去，§5.2） ─────────────────────────

export interface ClaimTicket {
  /** 放进 `?claim=` 的一次性令牌，10 分钟内有效。 */
  claim_token: string;
  /** 6 位认领码，同一网络里手动输入用（主屏幕打开时 cookie 不一定跟过来）。 */
  code: string;
  expires_at: number;
}

// ── 牌友（#108，设计稿 F1 / F2）──────────────────────────────────────────────

/** 一起坐过同一局的人（docs/server-design.md §6「09 牌友」）。服务端只给这些人，不提供搜用户的接口。 */
export interface Friend {
  user: UserRef;
  /** 一起坐过几局（没取消的局，含还没开打的）。 */
  together_count: number;
  /** 最近一起那局的开局时间，Unix 秒。 */
  last_together_at: number;
  /** 我星标了 TA。 */
  starred: boolean;
}

/** F1 顶部「雅加达掼蛋群」横幅（GET /api/config 的 group）。空字符串表示没配置。 */
export interface GroupInfo {
  wechat_id: string;
  whatsapp_group_url: string;
}

// ── 邀请牌友（#115，08「邀请谁」）────────────────────────────────────────────

export interface InviteResult {
  /** 这次新邀请的人（每人收到一条 invited 推送）。 */
  invited: string[];
  /** 已经在局里、邀请过、被移出过的人：跳过，不算错。 */
  skipped: string[];
}

/** 邀请结果翻成一句提示。 */
export function inviteSummary(r: InviteResult): string {
  if (r.invited.length === 0) return "他们都已经在局里或邀请过了";
  const base = `已邀请 ${r.invited.length} 位牌友，他们会收到通知`;
  return r.skipped.length > 0 ? `${base}（${r.skipped.length} 位已在局里或邀请过）` : base;
}

/** 邀请失败的原因。 */
export function inviteError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "quota_exceeded") return "今天邀请太多了，明天再试";
    if (err.code === "not_friend") return "有人不在你的牌友里了，刷新一下再选";
    if (err.code === "game_closed") return "这一局已经结束或取消了";
    if (err.code === "not_host") return "只有局头能邀请";
  }
  return "邀请没发出去，稍后再试";
}
