/**
 * 状态提示（#114）：局头改了时间（A3 / R1）、候补转正（W3）、局头转交（K2）。
 *
 * 服务端把这几件事作为**推送**发出（notify/message.go 的 rescheduled / promoted / host_transferred），
 * 实时通道上只有「这一局变了」（game.updated、seat.left……），不说变了什么、和我有没有关系。
 * 推送又不一定开着。所以前端自己记一份「我上次看到的这一局」（本机快照），
 * 每次拿到最新数据就和它比：
 *
 *   - 我还在局里，时间或地点和快照不一样，而且改的人不是我 → 改时间横幅；
 *   - 快照里我是候补，现在坐上了 → 「轮到你了」弹层；
 *   - 快照里我坐着、不是局头，现在成了局头 → 「你现在是局头」横幅。
 *
 * 详情页每收到一条实时事件都会重新拉 REST，首页每次加载列表也会比一次，
 * 所以「事件来的时候」和「下次打开的时候」走的是同一条路。
 *
 * 快照就是「用户已经知道的状态」：提示被关掉之前不更新对应的字段，所以没关掉的横幅刷新后还在。
 * 按身份分开存；只记我在里面的局，开局时间过了一天的自动清掉。不是凭证，存不下也只是少一次提示。
 */

import type { GameDetail, GameSummary } from "./api";

export type Role = "seat" | "waitlist" | "none";

/** 比较时用到的那几样，详情和列表项都能归一成它。 */
export interface GameView {
  code: string;
  startsAt: number;
  venue: string;
  /** 列表项在服务端还没给 my_role 时是 undefined：这时不判断和角色有关的提示。 */
  role: Role | undefined;
  hostID: string;
  hostName: string;
}

interface Snapshot {
  startsAt: number;
  venue: string;
  role: Role;
  hostID: string;
  hostName: string;
}

export type Notice =
  | { kind: "rescheduled"; code: string; startsAt: number; venue: string; timeChanged: boolean; venueChanged: boolean }
  | { kind: "promoted"; code: string }
  | { kind: "host"; code: string; formerHost: string };

export type NoticeKind = Notice["kind"];

/**
 * 快照按身份分开存（#127 复审）：同一台设备可能换身份（认领、「不是我」、切换成别人），
 * B 不能拿 A 的快照来比——否则 A 排的候补会让 B 看到一个莫名其妙的「你补上了」。
 */
export function snapshotKey(meID: string): string {
  return `gd_game_seen:${meID}`;
}
const KEEP_AFTER_START = 24 * 3600;

export function fromDetail(d: GameDetail): GameView {
  return {
    code: d.game.short_code,
    startsAt: d.game.starts_at,
    venue: d.game.venue,
    role: d.me.role,
    hostID: d.host.id,
    hostName: d.host.nickname,
  };
}

/** 列表项（W4）：服务端的 my_role 是 seated / waitlist / none，这里换成和详情一样的写法。 */
export function fromSummary(g: GameSummary): GameView {
  const role: Role | undefined =
    g.my_role === "seated" ? "seat" : g.my_role === "waitlist" ? "waitlist" : g.my_role === "none" ? "none" : undefined;
  return { code: g.short_code, startsAt: g.starts_at, venue: g.venue, role, hostID: g.host.id, hostName: g.host.nickname };
}

/** 和快照比，得出要给用户看的提示。没有快照（第一次看到这一局）就什么都不提示。 */
export function noticesFor(v: GameView, meID: string | undefined, store: Pick<Storage, "getItem"> | null = safe()): Notice[] {
  if (!meID) return [];
  const prev = load(store, meID)[v.code];
  if (!prev) return [];
  const out: Notice[] = [];
  const role = v.role ?? prev.role;
  const isHost = v.hostID === meID;

  if (prev.role === "waitlist" && role === "seat") out.push({ kind: "promoted", code: v.code });

  if (prev.hostID !== meID && isHost && prev.role === "seat") {
    out.push({ kind: "host", code: v.code, formerHost: prev.hostName });
  }

  const timeChanged = prev.startsAt !== v.startsAt;
  const venueChanged = prev.venue !== v.venue;
  // 改的时候我就是局头，那改时间的就是我自己，不用告诉我。看快照里的局头，而不是现在的：
  // 先被改了时间、后来又接手成了局头的人，那条横幅还没看过。
  if ((timeChanged || venueChanged) && role !== "none" && prev.hostID !== meID) {
    out.push({ kind: "rescheduled", code: v.code, startsAt: v.startsAt, venue: v.venue, timeChanged, venueChanged });
  }
  return out;
}

/**
 * 记下「用户已经知道了」。
 *
 * - `kinds` 不传：没有待处理的提示时，整份快照跟上最新状态；
 * - 传了：只把这几种提示对应的字段跟上，其余的保持原样（别的横幅还没关）。
 *
 * 不在局里（none）的局不记，原来记过的删掉。
 */
export function acknowledge(
  v: GameView,
  meID: string | undefined,
  kinds?: NoticeKind[],
  store: Pick<Storage, "getItem" | "setItem"> | null = safe(),
  now: number = Math.floor(Date.now() / 1000),
): void {
  if (!meID || !store) return;
  const all = load(store, meID);
  const prev = all[v.code];
  const role = v.role ?? prev?.role;

  if (!role || role === "none") {
    delete all[v.code];
  } else if (!prev || !kinds) {
    all[v.code] = { startsAt: v.startsAt, venue: v.venue, role, hostID: v.hostID, hostName: v.hostName };
  } else {
    const next = { ...prev };
    if (kinds.includes("rescheduled")) {
      next.startsAt = v.startsAt;
      next.venue = v.venue;
    }
    if (kinds.includes("promoted")) next.role = role;
    if (kinds.includes("host")) {
      next.hostID = v.hostID;
      next.hostName = v.hostName;
    }
    all[v.code] = next;
  }

  for (const [code, s] of Object.entries(all)) {
    if (s.startsAt + KEEP_AFTER_START < now) delete all[code];
  }
  try {
    store.setItem(snapshotKey(meID), JSON.stringify(all));
  } catch {
    // 存不下就算了。
  }
}

/** 一次拿到最新数据后的标准动作：算出提示，没有提示就顺手把快照跟上。 */
export function observe(v: GameView, meID: string | undefined): Notice[] {
  const notices = noticesFor(v, meID);
  if (notices.length === 0) acknowledge(v, meID);
  return notices;
}

function load(store: Pick<Storage, "getItem"> | null, meID: string): Record<string, Snapshot> {
  try {
    const raw = store?.getItem(snapshotKey(meID));
    const parsed = raw ? (JSON.parse(raw) as unknown) : {};
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, Snapshot>) : {};
  } catch {
    return {};
  }
}

function safe(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}
