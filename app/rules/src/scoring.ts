// 计分：升级、过 A、本场结束（#87 第 6 步）。**从 `app/minigame/src/lib/scoring.ts` 搬过来的**
// （纯搬家，一行逻辑都没改）—— 那份文件原来是「规则 + 记分弹层的界面/会话」混在一起。
//
// 规则以服务端 `server/internal/scoring` 为准，一致性由**两端共用的对照用例**锁住：
// `testdata/scoring/vectors.json`。Go 那边 `scoring/vectors_test.go` 读它，
// 小游戏那边 `app/minigame/src/lib/scoring.vectors.test.ts` 读同一份文件，CI 里两边都会跑
// （`scripts/test-local-ci.sh` 的 scope）。改了规则这个文件、或者改了那份用例，两边一起红。
//
// 这里**只有纯逻辑**：不读时间、不读随机数、不认识用户、不认识界面。记一局的弹层预览、两人同时
// 记同一局的冲突判断、实时事件的去重、各种中文文案，都留在小游戏那边（那些是界面的事，不是规则）。
//
// 为什么搬：引擎不该住在小游戏里（H5 只是个试验田，小游戏上线后废弃；引擎两边都要用），
// 而且原来那份文件还反向 `import` 了 `app/h5` 的 `UserRef` —— 见 `app/rules/README.md` 的上游映射。

export type Team = "" | "red" | "black";
export type Side = "red" | "black";

export interface ScoreSettings {
  kind: "classic" | "no-level";
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
export const other = (t: Side): Side => (t === "red" ? "black" : "red");

// ── 规则（照抄 server/internal/scoring.Compute）──

/** 新的一场：两队都从 2 开始、计数清零（`apply` 在本场结束时也用它）。 */
export function newMatch(match: number): ScoreState {
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
    // 打自己的 A 没过：A 必打（2022 竞赛规则第二条 13），没过就一直打 A，级数不动，
    // 只把次数记上去给界面展示。
    const p = st[st.playing as Side];
    p.a_fails++;
    rr.failed_a = st.playing;
    rr.a_fails = p.a_fails;
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
/**
 * 两个结果在这个玩法下是不是一回事：不升级玩法里三游、末游都只算「不是双上」，没有区别
 * （名次没选时客户端占位传 3，见小游戏那边的 `howText`）。
 */
export function sameOutcome(kind: ScoreSettings["kind"], a: RoundFact, b: RoundFact): boolean {
  if (kind !== "no-level") return sameFact(a, b);
  return a.head_seat === b.head_seat && (a.partner_place === 2) === (b.partner_place === 2);
}

/** 改第 no 局之后会怎样（S10「连带变化」）。 */
export function previewEdit(s: ScoreSettings, rounds: RoundFact[], no: number, fact: RoundFact) {
  const facts = rounds.map(factOf);
  facts[no - 1] = factOf(fact);
  const after = compute(s, facts);
  return { round: after.rounds[no - 1], state: after.state, later: rounds.length - no };
}

// ── 接下一副 ──

/** 「接下来这一副」要知道的：打几、要不要进贡，以及完整的计分状态（界面直接用）。 */
export interface NextHand {
  /** 下一副打几（到 A 封顶；本场刚结束时是下一场的 2）。 */
  level: number;
  /** 要不要进贡：这一场已经打过至少一副（新一场的第一副不贡，第十条 二）。 */
  tribute: boolean;
  score: ScoreState;
}

/**
 * 按本场到现在为止每一副的结果，算下一副打几、要不要进贡 —— 对应服务端的 `NextHand`
 * （Go 的 `res.Next`）。
 *
 * 升级、到 A 封顶、过 A、打谁的级**全由 `compute` 算**（第二条 12-14、第十二条），这里只是把
 * 下一副要用的那两项端出来；不另写一份规则。玩法固定按升级玩法（和服务端 `Next` 一样）。
 *
 * 和服务端的一处差别：Go 的 `Compute` 会校验头游座号（1–4）和搭档名次（2–4），这边不校验
 * （`compute` 本来就不校验，输入的合法性由调用方保证）—— 模拟对局、界面都是拿真实结果来算。
 */
export function nextHand(facts: RoundFact[]): NextHand {
  const { state } = compute({ kind: "classic" }, facts);
  return { level: state.playing_level, tribute: state.match_rounds > 0 && !state.match_over, score: state };
}
