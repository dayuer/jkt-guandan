// 牌桌计分（#165，设计稿 design/guandan-minigame/README.md §7）里**界面和会话**的那一半：
// 文案、冲突判断、去重、实时拼装、时间。
//
// 规则那一半（类型 + `compute` / `apply` / `previewEdit`）**已经搬到 `app/rules/src/scoring.ts`**
// （#87 第 6 步）—— 引擎不该住在小游戏里，而且那份代码原来还反向 import 了 `app/h5` 的用户类型。
// 这里把搬走的名字**原样再导出**，所以下面这些文件一行都不用改：
// 记分页、实时、冲突判断都是 `import { ... } from "./scoring"`。
//
// 规则以服务端 server/internal/scoring 为准，记下来的每一局、两队级数都用服务端返回的。
// 这里的 `apply` 只用在两个服务端还没回话的地方：记一局弹层的预览（S5–S7）、改一局弹层的
// 「连带变化」（S10）。提交之后一律换成服务端的结果。不涉及金钱（#91）：没有分数、倍率、金额。

import {
  A,
  apply,
  mateOf,
  newMatch,
  sameOutcome,
  type RoundFact,
  type RoundResult,
  type ScoreSettings,
  type ScoreState,
  type Side,
  type Team,
  type TeamState,
} from "../../../rules/src/scoring";

// 规则那一半：搬去 app/rules 了，这里照原样转出去（现有 import 点不用动）。
export { A, apply, compute, factOf, mateOf, other, previewEdit, sameFact, sameOutcome, teamOf } from "../../../rules/src/scoring";
export type { RoundFact, RoundResult, ScoreSettings, ScoreState, Side, Team, TeamState } from "../../../rules/src/scoring";

/**
 * 记分里用到的「一个人」：只要 id 和称呼。
 *
 * **故意不 import `app/h5/src/api` 的 `UserRef`** —— 引擎和这一块都不该反向依赖 H5
 * （小游戏上线后 H5 就废弃了）。形状和 `UserRef` 一样，传 `UserRef` 进来照样成立。
 */
export interface ScoreUser {
  id: string;
  nickname: string;
}

export interface ScoreRound extends RoundResult {
  recorded_by: ScoreUser;
  recorded_at: number;
  edited_by?: ScoreUser;
  edited_at?: number;
  /**
   * 这一局当时 1–4 号座上的人（#172，下标 0 是 1 号）。中途换过人，换人前的局仍是原来的人。
   * 0009 迁移前记的局、那时空着的座号是 null，退回用现在的座位；更早的服务端没有这个字段。
   */
  seats?: (ScoreUser | null)[];
}

/** GET /api/games/{code}/score 和三个写接口的响应（docs/server-design.md §5.6）。 */
export interface Score {
  started: boolean;
  can_record: boolean;
  /** 现在点「开始记分」会成功：在座位上、满座、还没开始、已到开局前 30 分钟（服务端算，客户端不看本机时钟） */
  can_start?: boolean;
  settings?: ScoreSettings;
  started_by?: ScoreUser;
  started_at?: number;
  revision?: number;
  rounds?: ScoreRound[];
  state?: ScoreState;
}

/** 已经开始的计分：字段都在。 */
export type StartedScore = Required<Score>;

export function isStarted(s: Score | null | undefined): s is StartedScore {
  return !!s && s.started && !!s.state && !!s.settings && Array.isArray(s.rounds);
}

/** score.started / score.round / score.edited 的 data。没有顶层 v，按 revision 去重。 */
export interface ScoreEventData {
  revision: number;
  by: ScoreUser;
  rounds: number;
  settings: ScoreSettings;
  state: ScoreState;
  round?: ScoreRound;
}

// ── 队名（界面文案；「红方」这种叫法只在界面上用，所以没跟着规则一起搬）──

export const TEAM_NAME: Record<Side, string> = { red: "红方", black: "黑方" };
export const TEAM_SHORT: Record<Side, string> = { red: "红", black: "黑" };

export function levelName(n: number): string {
  return n === 11 ? "J" : n === 12 ? "Q" : n === 13 ? "K" : n === 14 ? "A" : String(n);
}
export function placeName(p: number): string {
  return p === 2 ? "二游" : p === 3 ? "三游" : "末游";
}
/**
 * 「头游＋三游」「头游＋二游 · 双上」。
 * 不升级玩法只关心是不是双上：服务端要求 partner_place 是 2–4，没选时客户端只能占位传 3，
 * 所以不是双上就只写「头游」，不冒充「三游」（#174 第 6 项）。
 *
 * 「不比三游和末游的区别」这条规则本身在 `app/rules/src/scoring.ts` 的 `sameOutcome` 里。
 */
export function howText(partnerPlace: number, kind: ScoreSettings["kind"] = "classic"): string {
  if (kind === "no-level") return partnerPlace === 2 ? "头游＋二游 · 双上" : "头游";
  return `头游＋${placeName(partnerPlace)}${partnerPlace === 2 ? " · 双上" : ""}`;
}

// ── 看的时候：下一局打什么 ──

/** 记分页上要看的「下一局」：本场结束后点了「再来一场」，两队都从 2 开始。 */
export function boardState(st: ScoreState, nextMatch: boolean): ScoreState {
  return nextMatch && st.match_over ? newMatch(st.match + 1) : st;
}

/** 「第 6 局 · 打黑方的 7」 */
export function boardTitle(nextRoundNo: number, st: ScoreState, kind: ScoreSettings["kind"]): string {
  if (kind === "no-level") return `不升级 · 已打 ${st.match_rounds} 局`;
  if (!st.playing) return `第 ${nextRoundNo} 局 · 打 2`;
  return `第 ${nextRoundNo} 局 · 打${TEAM_NAME[st.playing]}的 ${levelName(st.playing_level)}`;
}

/** 级牌下面的「打 A · 第 1 次 / 已没过 N 次」，没到 A 时为空。 */
export function aTip(t: TeamState): string {
  if (t.level !== A) return "";
  return t.a_fails ? `打 A · 已没过 ${t.a_fails} 次` : "打 A · 第 1 次";
}

/** 每局记录里的签：「打黑 7」「打 2」 */
export function playingTag(r: { playing: Team; playing_level: number }): string {
  return r.playing ? `打${TEAM_SHORT[r.playing]} ${levelName(r.playing_level)}` : `打 ${levelName(r.playing_level)}`;
}

// ── 预览文案（S5–S7、S13）──

export type Names = (seat: number) => string;

export interface Preview {
  kind: "normal" | "pass" | "fail" | "plain";
  team: Side;
  main: string;
  sub: string;
}

export function previewText(s: ScoreSettings, st: ScoreState, fact: RoundFact, names: Names): Preview {
  const { round: r, state: next } = apply(s, st, fact);
  const team = r.winner, tn = TEAM_NAME[team];
  if (s.kind === "no-level") {
    return { kind: "plain", team, main: `${tn}赢一局`, sub: r.double_up ? "双上" : `${names(fact.head_seat)} 头游` };
  }
  if (r.passed_a) {
    return {
      kind: "pass", team, main: `过 A！${tn}赢下这一场`,
      sub: `${names(fact.head_seat)}头游、${names(mateOf(fact.head_seat))}${placeName(fact.partner_place)} · 一共 ${next.match_rounds} 局`,
    };
  }
  const nextLv = levelName(next.playing_level);
  if (r.failed_a) {
    const ft = r.failed_a as Side, fn = TEAM_NAME[ft];
    const count = failCountText(r);
    if (ft === team) {
      // 本队头游、搭档末游：没过，自己还在 A（A 必打，下一局还打）。
      return { kind: "fail", team, main: `没过 A · ${fn}下一局还打 A`, sub: count };
    }
    return {
      kind: "fail", team,
      main: `${fn}没过 A · ${tn} ${levelName(r.level_before)} → ${levelName(r.level_after)}`,
      sub: `${count} · 下一局打 ${nextLv}`,
    };
  }
  const up = r.level_after - r.level_before;
  const how = up === 0 ? "已在 A" : up < r.upgrade ? `升 ${up} 级（到 A 封顶）` : r.double_up ? "双上" : `升 ${up} 级`;
  return {
    kind: "normal", team,
    main: `${tn} ${levelName(r.level_before)} → ${levelName(r.level_after)}`,
    sub: `${how} · 下一局打 ${nextLv}`,
  };
}

/** 没过 A 的说明：A 必打，没过就一直打 A（2022 竞赛规则第二条 13），只报次数。 */
function failCountText(r: RoundResult): string {
  return `已没过 ${r.a_fails ?? 0} 次`;
}

/** 一局的结果写成一行：「红方 头游＋三游，9 → J」（S11 顶部条、S12 并排）。 */
export function resultLine(kind: ScoreSettings["kind"], r: RoundResult): string {
  const base = `${TEAM_NAME[r.winner]} ${howText(r.partner_place, kind)}`;
  if (kind === "no-level") return base;
  if (r.passed_a) return `${base}，过 A`;
  return `${base}，${levelName(r.level_before)} → ${levelName(r.level_after)}`;
}

/** 「阿杰 头游 · 老王 三游」；不升级玩法不是双上时只写「阿杰 头游」 */
export function whoLine(fact: RoundFact, names: Names, kind: ScoreSettings["kind"] = "classic"): string {
  const head = `${names(fact.head_seat)} 头游`;
  if (kind === "no-level" && fact.partner_place !== 2) return head;
  return `${head} · ${names(mateOf(fact.head_seat))} ${placeName(fact.partner_place)}`;
}

/**
 * 某一局的称呼：用这一局当时的座位（#172 的 seats），那个座号是 null 或者服务端没给，
 * 退回用现在坐在那里的人（current）。S9 每局记录、S10 改一局、S12 并排、S15 战报都用它。
 */
export function roundNames(round: { seats?: (ScoreUser | null)[] } | null | undefined, current: Names): Names {
  return (seat) => round?.seats?.[seat - 1]?.nickname || current(seat);
}
/** 某一局当时这个座号上是不是我；seats 没有这个座号时退回看现在的座位（isMeNow）。 */
export function roundIsMe(round: { seats?: (ScoreUser | null)[] } | null | undefined, seat: number, myId: string | undefined, isMeNow: (seat: number) => boolean): boolean {
  const who = round?.seats?.[seat - 1];
  return who ? !!myId && who.id === myId : isMeNow(seat);
}

// ── 两人同时记同一局（S12）──

export type ConflictDecision = "mine" | "same" | "differ" | "mine-differ";

/**
 * 409 round_taken 之后怎么办：
 * - mine：是我自己记的、结果也一样（弱网下第一次其实写进去了）→ 就是成功了；
 * - mine-differ：是我自己记的，但和这次选的不一样（第一次写进去了，用户以为没成，改了选择再提交，
 *   换了新的幂等键）→ 不能静默当成功，弹 S12 并排框，左边写「你刚才记的」；
 * - same：别人记的、结果一样 → 静默合并，toast「和 X 记的一样」；
 * - differ：别人记的、不一样 → 弹 S12。
 * 「一样」按玩法比（sameOutcome）：不升级玩法里三游、末游没有区别。
 */
export function decideConflict(existing: ScoreRound, mine: RoundFact, myId: string | undefined, kind: ScoreSettings["kind"] = "classic"): ConflictDecision {
  const same = sameOutcome(kind, existing, mine);
  if (myId && existing.recorded_by?.id === myId) return same ? "mine" : "mine-differ";
  return same ? "same" : "differ";
}

/**
 * 改一局收到 409 round_changed：如果最新计分里这一局已经就是我要改成的结果（上一次 PUT 写成功了、
 * 响应丢了，重试时服务端拿 was 比对不上），就当成功，不提示「刚被别人改过」。
 */
export function editAlreadyApplied(latest: Score | null | undefined, no: number, mine: RoundFact): boolean {
  if (!isStarted(latest)) return false;
  const r = latest.rounds[no - 1];
  return !!r && r.round_no === no && sameOutcome(latest.settings.kind, r, mine);
}

/**
 * 实时 score.round 能不能直接拼在本地后面：局号正好接上，而且 revision 正好是本地的下一个。
 * 中间漏了事件（比如一次 score.edited），前面的行就是旧的，要重新拉整份（#170 复审意见 3）。
 */
export function canAppendRound(local: StartedScore | null, ev: Pick<ScoreEventData, "revision" | "round">): boolean {
  if (!local || !ev.round) return false;
  return ev.round.round_no === local.rounds.length + 1 && ev.revision === local.revision + 1;
}

// ── 实时去重：计分事件不带顶层 v，按 data.revision 丢掉不比本地新的 ──

export class RevisionGate {
  private rev = 0;
  get current() { return this.rev; }
  /** REST 拿到的：记下来，比它旧的事件以后都丢掉。返回这份数据是否不比本地旧（旧的也丢）。 */
  seen(rev: number | undefined): boolean {
    if (typeof rev !== "number") return true;
    if (rev < this.rev) return false;
    this.rev = rev;
    return true;
  }
  /** 实时事件：比本地新才收。 */
  accept(rev: unknown): boolean {
    if (typeof rev !== "number" || rev <= this.rev) return false;
    this.rev = rev;
    return true;
  }
  reset() { this.rev = 0; }
}

// ── 退出确认（#168）──

/**
 * 记分开始以后退出前的说明。有候补时服务端会立刻让候补第一位补上空出来的座位（promoteHead），
 * 退出的人回不来；没有候补时局回到 open、记分暂停，回原座位可以接着记（server-design §5.6）。
 */
export function leaveConfirmText(waitlist: { user: { nickname: string } }[]): string {
  if (!waitlist.length) return "退出后这局记分会暂停，回到原座位可以继续";
  const name = waitlist[0].user.nickname;
  return name ? `退出后座位会由候补第一位 ${name} 补上，记分接着记` : "退出后座位会由候补第一位补上，记分接着记";
}

// ── 时间 ──

/**
 * 详情页什么时候再问一次服务端 can_start（毫秒；null = 不用问）。能不能开始只看服务端的 can_start，
 * 本机时钟只用来挑「大概到点了」的时刻去重新拉 GET /score：开局前 30 分钟后 2 秒；本机算着已经到了、
 * 服务端还说不行（时钟偏了），每分钟问一次。离得远时最多 10 分钟问一次，避免一个很长的定时器。
 */
export function startRecheckDelay(o: { startsAt: number; nowSec: number; canStart: boolean; started: boolean; eligible: boolean }): number | null {
  if (o.started || o.canStart || !o.eligible) return null;
  const opens = o.startsAt - 30 * 60;
  if (o.nowSec >= opens) return 60_000;
  return Math.min(opens - o.nowSec + 2, 600) * 1000;
}

/** 第 match 场的用时（秒）：从开始记分或上一场最后一局，到这一场最后一局。 */
export function matchDuration(startedAt: number, rounds: ScoreRound[], match: number): number {
  const mine = rounds.filter((r) => r.match === match);
  if (!mine.length) return 0;
  const prev = rounds.filter((r) => r.match < match);
  const from = prev.length ? prev[prev.length - 1].recorded_at : startedAt;
  return Math.max(0, mine[mine.length - 1].recorded_at - from);
}
/** 「1:52」 */
export function durationShort(sec: number): string {
  const m = Math.floor(sec / 60);
  return `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")}`;
}
/** 「1 小时 52 分」「38 分」 */
export function durationLong(sec: number): string {
  const m = Math.floor(sec / 60), h = Math.floor(m / 60);
  if (!h) return m ? `${m} 分` : "不到 1 分";
  return m % 60 ? `${h} 小时 ${m % 60} 分` : `${h} 小时`;
}
