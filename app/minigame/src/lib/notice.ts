// 状态提示的换设备兜底（#160）：改时间横幅、「轮到你了」弹层。
//
// 平时还是走 H5 的 status.ts：和本机快照比。但快照只在这台设备上——换了手机、清了微信缓存、
// 第一次在这台设备上打开某一局时没有快照，status.ts 就什么都不提示，接着把当前状态记成「已经看过」，
// 局头改过的时间、补上的座位就永远提示不出来了。
//
// 这里包一层：**有快照时原样交给 status.ts**；没有快照时，用服务端按身份给的时间兜底——
//   - `rescheduled_at` 晚于我加入这一局的时间，我在局里、不是局头 → 改时间横幅；
//   - 我在座位上、有补位时间 → 「轮到你了」。
// 不知道改的是时间还是地点，横幅的 timeChanged / venueChanged 都是 false，文案按「改了安排」写。
//
// 关掉一条：没有别的兜底提示了，就写下完整快照，以后回到快照那条路；还有别的没关（两条同时出现时），
// 先在 gd_game_fallback_seen 里记下关掉的这条，等全关完再写快照。H5 不用这一层，行为不变。

import {
  acknowledge, fromDetail, fromSummary, noticesFor, snapshotKey,
  type GameView, type Notice, type NoticeKind,
} from "../../../shared/src/status";
import type { GameDetail, GameSummary } from "./api";

type Store = Pick<Storage, "getItem" | "setItem">;

/** 服务端给的几个时间（Unix 秒），没有就是 undefined。 */
export interface ServerTimes {
  rescheduledAt?: number;
  joinedAt?: number;
  promotedAt?: number;
  /** 我**接手**当上局头的时刻（#189）；开局的人本人没有这一项。 */
  becameHostAt?: number;
}

export interface NoticeView {
  view: GameView;
  times: ServerTimes;
}

/**
 * 关掉过的兜底提示：r / p / h 是关掉时的 rescheduled_at / promoted_at / became_host_at，
 * s 是开局时间（过期清理用）。
 */
interface Seen {
  r?: number;
  p?: number;
  h?: number;
  s: number;
}

const KEEP_AFTER_START = 24 * 3600;

export function fallbackKey(meID: string): string {
  return `gd_game_fallback_seen:${meID}`;
}

export function detailView(d: GameDetail): NoticeView {
  // 这几个字段 H5 的类型里没有（H5 不用），这里就地补上。
  const game = d.game as GameDetail["game"] & { rescheduled_at?: number };
  const me = d.me as GameDetail["me"] & { joined_at?: number; promoted_at?: number; became_host_at?: number };
  return {
    view: fromDetail(d),
    times: {
      rescheduledAt: game.rescheduled_at, joinedAt: me.joined_at, promotedAt: me.promoted_at,
      becameHostAt: me.became_host_at,
    },
  };
}

export function summaryView(g: GameSummary): NoticeView {
  const x = g as GameSummary & {
    rescheduled_at?: number; my_joined_at?: number; my_promoted_at?: number; my_became_host_at?: number;
  };
  return {
    view: fromSummary(g),
    times: {
      rescheduledAt: x.rescheduled_at, joinedAt: x.my_joined_at, promotedAt: x.my_promoted_at,
      becameHostAt: x.my_became_host_at,
    },
  };
}

/** 拿到最新数据后：算出提示；没有提示就把快照跟上（同 status.ts 的 observe）。 */
export function observeNotices(v: NoticeView, meID: string | undefined, store: Store | null = safe()): Notice[] {
  if (!meID) return [];
  if (hasSnapshot(store, meID, v.view.code)) {
    const notices = noticesFor(v.view, meID, store);
    if (notices.length === 0) acknowledge(v.view, meID, undefined, store);
    return notices;
  }
  const seen = loadSeen(store, meID);
  const notices = fallback(v, meID, seen[v.view.code]);
  if (notices.length === 0) {
    acknowledge(v.view, meID, undefined, store);
    if (seen[v.view.code]) {
      delete seen[v.view.code];
      saveSeen(store, meID, seen);
    }
  }
  return notices;
}

/** 用户关掉了一条提示（同 status.ts 的 acknowledge(v, meID, [kind])）。 */
export function dismissNotice(
  v: NoticeView,
  meID: string | undefined,
  kind: NoticeKind,
  store: Store | null = safe(),
  now: number = Math.floor(Date.now() / 1000),
): void {
  if (!meID || !store) return;
  if (hasSnapshot(store, meID, v.view.code)) {
    acknowledge(v.view, meID, [kind], store, now);
    return;
  }
  const seen = loadSeen(store, meID);
  const entry: Seen = { ...seen[v.view.code], s: v.view.startsAt };
  if (kind === "rescheduled") entry.r = v.times.rescheduledAt;
  if (kind === "promoted") entry.p = v.times.promotedAt;
  if (kind === "host") entry.h = v.times.becameHostAt;

  if (fallback(v, meID, entry).length === 0) {
    delete seen[v.view.code];
    acknowledge(v.view, meID, undefined, store, now);
  } else {
    seen[v.view.code] = entry;
  }
  for (const [code, s] of Object.entries(seen)) {
    if (s.s + KEEP_AFTER_START < now) delete seen[code];
  }
  saveSeen(store, meID, seen);
}

/** 没有快照时，用服务端字段算出的提示。顺序和 status.ts 一致：先补位，后局头，再改时间。 */
function fallback(v: NoticeView, meID: string, seen: Seen | undefined): Notice[] {
  const { view, times } = v;
  const out: Notice[] = [];
  if (view.role === "seat" && times.promotedAt && seen?.p !== times.promotedAt) {
    out.push({ kind: "promoted", code: view.code });
  }
  // 接手当上局头（#189）：和补位同理，没有快照时靠服务端的 became_host_at。开局的人本人
  // 没有这一项，所以不会给自己弹。看不到上一位局头是谁，formerHost 留空（文案里有兜底）。
  if (view.role === "seat" && times.becameHostAt && seen?.h !== times.becameHostAt) {
    out.push({ kind: "host", code: view.code, formerHost: "" });
  }
  // 改时间横幅：**接手的局头也要看**（#185 复审意见 3）—— 上一位局头改的时间他也没看过。
  // 只有在「那次改时间的时候我已经是局头」时才不用说，那才是我自己改的。
  //   · 我不是局头 → 说；
  //   · 我是局头、became_host_at 是空的 → 我是开局那个人，按老规矩不说；
  //   · 我是接手的，而且接手**晚于**那次改时间 → 不是我改的，说。
  const iWasHostThen =
    view.hostID === meID && (!times.becameHostAt || times.becameHostAt <= (times.rescheduledAt ?? 0));
  if (
    view.role !== undefined && view.role !== "none" && !iWasHostThen &&
    times.rescheduledAt && times.joinedAt && times.rescheduledAt > times.joinedAt &&
    seen?.r !== times.rescheduledAt
  ) {
    out.push({ kind: "rescheduled", code: view.code, startsAt: view.startsAt, venue: view.venue, timeChanged: false, venueChanged: false });
  }
  return out;
}

function hasSnapshot(store: Pick<Storage, "getItem"> | null, meID: string, code: string): boolean {
  return Object.prototype.hasOwnProperty.call(parse(store, snapshotKey(meID)), code);
}

function loadSeen(store: Pick<Storage, "getItem"> | null, meID: string): Record<string, Seen> {
  return parse(store, fallbackKey(meID)) as Record<string, Seen>;
}

function saveSeen(store: Store | null, meID: string, seen: Record<string, Seen>): void {
  try {
    store?.setItem(fallbackKey(meID), JSON.stringify(seen));
  } catch {
    // 存不下就算了，最多再提示一次。
  }
}

function parse(store: Pick<Storage, "getItem"> | null, key: string): Record<string, unknown> {
  try {
    const raw = store?.getItem(key);
    const parsed = raw ? (JSON.parse(raw) as unknown) : {};
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function safe(): Storage | null {
  try {
    return (globalThis as any).window?.localStorage ?? null;
  } catch {
    return null;
  }
}
