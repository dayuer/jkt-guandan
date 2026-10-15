// 牌桌计分（#165，设计稿 design/guandan-minigame/README.md §7）的纯逻辑：类型、文案、冲突判断、去重。
//
// 规则以服务端 server/internal/scoring 为准，记下来的每一局、两队级数都用服务端返回的。
// 这里的 compute 只是那份规则的照抄，用在两个服务端还没回话的地方：
//   - 记一局弹层的预览（S5–S7「红方 9 → J」「过 A」「没过」）；
//   - 改一局弹层的「连带变化」（S10）和 S12 并排给出的「你记的」。
// 提交之后一律换成服务端的结果。不涉及金钱（#91）：没有分数、倍率、金额。

import type { UserRef } from "../../../h5/src/api";

export type Team = "" | "red" | "black";
export type Side = "red" | "black";

export interface ScoreSettings {
  kind: "classic" | "no-level";
  /** 打自己的 A 第几次没过回到 2；0 = 不回 2 */
  a_fail_limit: number;
}

export interface TeamState {
  level: number;
  a_fails: number;
  wins: number;
  double_ups: number;
}

export interface ScoreState {
  match: number;
  match_rounds: number;
  playing: Team;
  playing_level: number;
  red: TeamState;
  black: TeamState;
  match_over: boolean;
  winner?: Team;
}

/** 一局的事实：谁头游、他的搭档第几个出完（2 二游 / 3 三游 / 4 末游）。 */
export interface RoundFact {
  head_seat: number;
  partner_place: number;
}

export interface RoundResult extends RoundFact {
  round_no: number;
  match: number;
  playing: Team;
  playing_level: number;
  winner: Side;
  double_up: boolean;
  upgrade: number;
  level_before: number;
  level_after: number;
  passed_a: boolean;
  failed_a?: Team;
  a_fails?: number;
  reset_to_2?: boolean;
}

export interface ScoreRound extends RoundResult {
  recorded_by: UserRef;
  recorded_at: number;
  edited_by?: UserRef;
  edited_at?: number;
}

/** GET /api/games/{code}/score 和三个写接口的响应（docs/server-design.md §5.6）。 */
export interface Score {
  started: boolean;
  can_record: boolean;
  settings?: ScoreSettings;
  started_by?: UserRef;
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
  by: UserRef;
  rounds: number;
  settings: ScoreSettings;
  state: ScoreState;
  round?: ScoreRound;
}

// ── 座位和队 ──

export const A = 14;

/** 1、3 号红方，2、4 号黑方。 */
export function teamOf(seat: number): Side {
  return seat % 2 === 1 ? "red" : "black";
}
/** 对家：1↔3，2↔4。 */
export function mateOf(seat: number): number {
  return seat <= 2 ? seat + 2 : seat - 2;
}
export const TEAM_NAME: Record<Side, string> = { red: "红方", black: "黑方" };
export const TEAM_SHORT: Record<Side, string> = { red: "红", black: "黑" };
export const other = (t: Side): Side => (t === "red" ? "black" : "red");

export function levelName(n: number): string {
  return n === 11 ? "J" : n === 12 ? "Q" : n === 13 ? "K" : n === 14 ? "A" : String(n);
}
export function placeName(p: number): string {
  return p === 2 ? "二游" : p === 3 ? "三游" : "末游";
}
/** 「头游＋三游」「头游＋二游 · 双上」 */
export function howText(partnerPlace: number): string {
  return `头游＋${placeName(partnerPlace)}${partnerPlace === 2 ? " · 双上" : ""}`;
}

// ── 规则（照抄 server/internal/scoring.Compute，只做预览）──

function newMatch(match: number): ScoreState {
  const t = (): TeamState => ({ level: 2, a_fails: 0, wins: 0, double_ups: 0 });
  return { match, match_rounds: 0, playing: "", playing_level: 2, red: t(), black: t(), match_over: false };
}
function clone(st: ScoreState): ScoreState {
  return { ...st, red: { ...st.red }, black: { ...st.black } };
}

/** 记一局：返回这一局的结果和记完后的状态。st 本场已结束时，这一局是下一场的第 1 局。 */
export function apply(s: ScoreSettings, prev: ScoreState, rd: RoundFact): { round: RoundResult; state: ScoreState } {
  const st = prev.match_over ? newMatch(prev.match + 1) : clone(prev);
  const winner = teamOf(rd.head_seat);
  const rr: RoundResult = {
    ...rd, round_no: 0, match: st.match, playing: st.playing, playing_level: st.playing_level, winner,
    double_up: rd.partner_place === 2, upgrade: 0, level_before: 0, level_after: 0, passed_a: false,
  };
  st.match_rounds++;
  const w = st[winner];
  w.wins++;
  if (rr.double_up) w.double_ups++;
  if (s.kind === "no-level") return { round: rr, state: st };

  rr.upgrade = 5 - rd.partner_place;
  rr.level_before = w.level;
  const playingOwnA = st.playing !== "" && st[st.playing].level === A;
  if (playingOwnA && winner === st.playing && rd.partner_place !== 4) {
    rr.passed_a = true;
    rr.level_after = A;
    st.match_over = true;
    st.winner = winner;
    st.playing = "";
    st.playing_level = 2;
    return { round: rr, state: st };
  }
  w.level = Math.min(w.level + rr.upgrade, A);
  if (playingOwnA) {
    const p = st[st.playing as Side];
    p.a_fails++;
    rr.failed_a = st.playing;
    rr.a_fails = p.a_fails;
    if (s.a_fail_limit > 0 && p.a_fails >= s.a_fail_limit) {
      p.level = 2;
      p.a_fails = 0;
      rr.reset_to_2 = true;
    }
  }
  rr.level_after = w.level;
  st.playing = winner;
  st.playing_level = w.level;
  return { round: rr, state: st };
}

export function compute(s: ScoreSettings, facts: RoundFact[]): { rounds: RoundResult[]; state: ScoreState } {
  let st = newMatch(1);
  const rounds: RoundResult[] = [];
  facts.forEach((f, i) => {
    const r = apply(s, st, f);
    r.round.round_no = i + 1;
    rounds.push(r.round);
    st = r.state;
  });
  return { rounds, state: st };
}

export const factOf = (r: RoundFact): RoundFact => ({ head_seat: r.head_seat, partner_place: r.partner_place });
export const sameFact = (a: RoundFact, b: RoundFact) => a.head_seat === b.head_seat && a.partner_place === b.partner_place;

/** 改第 no 局之后会怎样（S10「连带变化」）。 */
export function previewEdit(s: ScoreSettings, rounds: RoundFact[], no: number, fact: RoundFact) {
  const facts = rounds.map(factOf);
  facts[no - 1] = factOf(fact);
  const after = compute(s, facts);
  return { round: after.rounds[no - 1], state: after.state, later: rounds.length - no };
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
    const count = failCountText(s, r);
    if (ft === team) {
      // 本队头游、搭档末游：没过，自己还在 A（或回到 2）
      return { kind: "fail", team, main: r.reset_to_2 ? `没过 A · ${fn}回到 2` : `没过 A · ${fn}下一局还打 A`, sub: count };
    }
    return {
      kind: "fail", team,
      main: `${fn}没过 A · ${tn} ${levelName(r.level_before)} → ${levelName(r.level_after)}`,
      sub: r.reset_to_2 ? `${count} · 下一局打 ${nextLv}` : `${count}`,
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

function failCountText(s: ScoreSettings, r: RoundResult): string {
  const n = r.a_fails ?? 0;
  if (r.reset_to_2) return `第 ${n} 次没过，按约定回到 2`;
  if (s.a_fail_limit <= 0) return `已没过 ${n} 次（约定不回 2）`;
  return `已没过 ${n} 次，第 ${s.a_fail_limit} 次再不过就回到 2`;
}

/** 一局的结果写成一行：「红方 头游＋三游，9 → J」（S11 顶部条、S12 并排）。 */
export function resultLine(kind: ScoreSettings["kind"], r: RoundResult): string {
  const base = `${TEAM_NAME[r.winner]} ${howText(r.partner_place)}`;
  if (kind === "no-level") return base;
  if (r.passed_a) return `${base}，过 A`;
  return `${base}，${levelName(r.level_before)} → ${levelName(r.level_after)}`;
}

/** 「阿杰 头游 · 老王 三游」 */
export function whoLine(fact: RoundFact, names: Names): string {
  return `${names(fact.head_seat)} 头游 · ${names(mateOf(fact.head_seat))} ${placeName(fact.partner_place)}`;
}

// ── 两人同时记同一局（S12）──

export type ConflictDecision = "mine" | "same" | "differ";

/**
 * 409 round_taken 之后怎么办：记下的那一局是我自己记的（比如没带幂等键的双击）就当成功；
 * 和我选的一样就静默合并，toast「和 X 记的一样」；不一样才弹框让用户选。
 * 不升级玩法里三游、末游只差在「双上」统计上也算不一样。
 */
export function decideConflict(existing: ScoreRound, mine: RoundFact, myId: string | undefined): ConflictDecision {
  if (myId && existing.recorded_by?.id === myId) return "mine";
  return sameFact(existing, mine) ? "same" : "differ";
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

// ── 时间 ──

/** 开局前 30 分钟起才能开始记分（服务端 ScoreOpensBefore；GET 的 can_record 不看这个窗口）。 */
export const SCORE_OPENS_BEFORE = 30 * 60;
export function canStartNow(startsAt: number, nowSec: number): boolean {
  return nowSec >= startsAt - SCORE_OPENS_BEFORE;
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
  if (!h) return `${m} 分`;
  return m % 60 ? `${h} 小时 ${m % 60} 分` : `${h} 小时`;
}
