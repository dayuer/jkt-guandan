import { describe, expect, it } from "vitest";
import {
  RevisionGate, aTip, apply, boardState, boardTitle, canAppendRound, compute, decideConflict, durationLong, durationShort,
  editAlreadyApplied, howText, leaveConfirmText, levelName, mateOf, matchDuration, playingTag, previewEdit, previewText, resultLine, roundIsMe,
  roundNames, sameOutcome, startRecheckDelay, teamOf, whoLine,
  type RoundFact, type Score, type ScoreRound, type ScoreSettings, type StartedScore,
} from "./scoring";

const classic: ScoreSettings = { kind: "classic" };
const noLevel: ScoreSettings = { kind: "no-level" };
const f = (head_seat: number, partner_place: number): RoundFact => ({ head_seat, partner_place });
// 设计稿的例子：红方 1 老王 + 3 阿杰，黑方 2 Lily + 4 小周
const NAMES: Record<number, string> = { 1: "老王", 2: "Lily", 3: "阿杰", 4: "小周" };
const names = (s: number) => NAMES[s];
// ①打2 红双上 2→5 ②打5 黑头+三 2→4 ③打4 红头+末 5→6 ④打6 红双上 6→9 ⑤打9 黑双上 4→7
const FIVE = [f(1, 2), f(4, 3), f(3, 4), f(1, 2), f(2, 2)];

describe("座位和级数", () => {
  it("1、3 红方，2、4 黑方；对家是搭档", () => {
    expect([1, 2, 3, 4].map(teamOf)).toEqual(["red", "black", "red", "black"]);
    expect([1, 2, 3, 4].map(mateOf)).toEqual([3, 4, 1, 2]);
  });
  it("级数写成牌面", () => {
    expect([2, 10, 11, 12, 13, 14].map(levelName)).toEqual(["2", "10", "J", "Q", "K", "A"]);
  });
});

describe("规则照抄（只做预览）与设计稿例子一致", () => {
  it("前 5 局，S3：第 6 局打黑方的 7", () => {
    const { rounds, state } = compute(classic, FIVE);
    expect(rounds.map((r) => [r.playing_level, r.level_before, r.level_after])).toEqual([[2, 2, 5], [5, 2, 4], [4, 5, 6], [6, 6, 9], [9, 4, 7]]);
    expect(state.red.level).toBe(9);
    expect(state.black.level).toBe(7);
    expect(state.playing).toBe("black");
    expect(boardTitle(6, state, "classic")).toBe("第 6 局 · 打黑方的 7");
    expect(playingTag(rounds[0])).toBe("打 2");
    expect(playingTag(rounds[4])).toBe("打红 9");
  });
  it("S5 预览：阿杰头游、老王三游 → 红方 9 → J", () => {
    const { state } = compute(classic, FIVE);
    expect(previewText(classic, state, f(3, 3), names)).toEqual({ kind: "normal", team: "red", main: "红方 9 → J", sub: "升 2 级 · 下一局打 J" });
    expect(previewText(classic, state, f(3, 2), names).sub).toBe("双上 · 下一局打 Q");
  });
  it("S10：把第 4 局改成头游＋三游，后面 2 局重算，下一局打 10", () => {
    const six = [...FIVE, f(3, 3)];
    const p = previewEdit(classic, six, 4, f(1, 3));
    expect([p.round.level_before, p.round.level_after]).toEqual([6, 8]);
    expect(p.later).toBe(2);
    expect(p.state.red.level).toBe(10);
    expect(p.state.black.level).toBe(7);
    expect(p.state.playing_level).toBe(10);
  });
  it("打本队 A：头游＋二游 / 三游过 A，末游没过；对方头游也算没过", () => {
    // 红方先打到 A：2→5→8→11→14
    const toA = [f(1, 2), f(1, 2), f(1, 2), f(1, 2)];
    const { state } = compute(classic, toA);
    expect(state.red.level).toBe(14);
    expect(aTip(state.red)).toBe("打 A · 第 1 次");
    const pass = previewText(classic, state, f(1, 2), names);
    expect(pass.kind).toBe("pass");
    expect(pass.main).toBe("过 A！红方赢下这一场");
    expect(pass.sub).toBe("老王头游、阿杰二游 · 一共 5 局");
    const failOwn = previewText(classic, state, f(1, 4), names);
    expect(failOwn).toMatchObject({ kind: "fail", main: "没过 A · 红方下一局还打 A", sub: "已没过 1 次" });
    const failOpp = previewText(classic, state, f(2, 3), names);
    expect(failOpp).toMatchObject({ kind: "fail", main: "红方没过 A · 黑方 2 → 4" });
  });
  it("打对方的级时在 A 上赢了不算过 A", () => {
    // 红方到 A，黑方赢一局让下一局打黑方的级，红方再赢：只升级（封顶）
    const { state } = compute(classic, [f(1, 2), f(1, 2), f(1, 2), f(1, 2), f(2, 4)]);
    expect(state.playing).toBe("black");
    const p = previewText(classic, state, f(1, 2), names);
    expect(p.kind).toBe("normal");
    expect(p.sub).toBe("已在 A · 下一局打 A");
  });
  it("A 必打：没过就一直打 A，计数一直累加（2022 竞赛规则第二条 13）", () => {
    const toA = [f(1, 2), f(1, 2), f(1, 2), f(1, 2)];
    const two = compute(classic, [...toA, f(1, 4), f(1, 4)]).state;
    expect(aTip(two.red)).toBe("打 A · 已没过 2 次");
    const p = previewText(classic, two, f(1, 4), names);
    expect(p).toMatchObject({ kind: "fail", main: "没过 A · 红方下一局还打 A", sub: "已没过 3 次" });
    const after = apply(classic, two, f(1, 4)).state;
    expect([after.red.level, after.red.a_fails, after.playing_level]).toEqual([14, 3, 14]);
    // 再没过几次也一样：级数不动，次数接着涨。
    const st = compute(classic, [...toA, f(1, 4), f(1, 4), f(1, 4), f(1, 4)]).state;
    expect([st.red.level, st.red.a_fails]).toEqual([14, 4]);
  });
  it("过 A 后再记一局就是下一场：两队都从 2 开始，局号接着编", () => {
    const toA = [f(1, 2), f(1, 2), f(1, 2), f(1, 2), f(1, 2)];
    const { state } = compute(classic, toA);
    expect(state.match_over).toBe(true);
    expect(state.red.level).toBe(14);
    const b = boardState(state, true);
    expect([b.match, b.red.level, b.black.level, b.playing]).toEqual([2, 2, 2, ""]);
    expect(boardState(state, false)).toBe(state);
    expect(boardTitle(6, b, "classic")).toBe("第 6 局 · 打 2");
    const next = compute(classic, [...toA, f(2, 3)]);
    expect([next.rounds[5].match, next.rounds[5].round_no, next.state.black.level]).toEqual([2, 6, 4]);
  });
  it("不升级：只记哪队赢、双上几次", () => {
    const { state } = compute(noLevel, [f(1, 2), f(2, 3), f(3, 4)]);
    expect([state.red.wins, state.black.wins, state.red.double_ups]).toEqual([2, 1, 1]);
    expect(state.red.level).toBe(2);
    expect(boardTitle(4, state, "no-level")).toBe("不升级 · 已打 3 局");
    expect(previewText(noLevel, state, f(2, 2), names)).toEqual({ kind: "plain", team: "black", main: "黑方赢一局", sub: "双上" });
  });
  it("一局写成一行", () => {
    const { rounds } = compute(classic, [...FIVE, f(3, 3)]);
    expect(resultLine("classic", rounds[5])).toBe("红方 头游＋三游，9 → J");
    expect(resultLine("classic", rounds[0])).toBe("红方 头游＋二游 · 双上，2 → 5");
  });
});

describe("S12 冲突", () => {
  const existing = { head_seat: 3, partner_place: 3, recorded_by: { id: "lily", nickname: "Lily" } } as ScoreRound;
  it("记下的那一局是我自己记的、结果一样：当成功", () => {
    expect(decideConflict(existing, f(3, 3), "lily")).toBe("mine");
  });
  it("是我自己记的但这次选的不一样（弱网下第一次其实记上了）：不能静默，弹框", () => {
    expect(decideConflict(existing, f(3, 2), "lily")).toBe("mine-differ");
    expect(decideConflict(existing, f(1, 3), "lily", "classic")).toBe("mine-differ");
  });
  it("不升级玩法：三游、末游没有区别，只比头游和是不是双上", () => {
    expect(decideConflict(existing, f(3, 4), "ajie", "no-level")).toBe("same");
    expect(decideConflict(existing, f(3, 4), "lily", "no-level")).toBe("mine");
    expect(decideConflict(existing, f(3, 2), "ajie", "no-level")).toBe("differ");
    expect(decideConflict(existing, f(1, 3), "ajie", "no-level")).toBe("differ");
    expect(decideConflict(existing, f(3, 4), "ajie", "classic")).toBe("differ");
    expect(sameOutcome("no-level", f(2, 3), f(2, 4))).toBe(true);
    expect(sameOutcome("no-level", f(2, 2), f(2, 4))).toBe(false);
    expect(sameOutcome("classic", f(2, 3), f(2, 4))).toBe(false);
  });
  it("结果一样：静默合并", () => {
    expect(decideConflict(existing, f(3, 3), "ajie")).toBe("same");
  });
  it("结果不一样：让用户选", () => {
    expect(decideConflict(existing, f(3, 2), "ajie")).toBe("differ");
    expect(decideConflict(existing, f(1, 3), undefined)).toBe("differ");
  });
});

describe("改一局重试收到 round_changed", () => {
  const latest = (rounds: RoundFact[], kind: ScoreSettings["kind"] = "classic"): Score => {
    const settings = { kind }, c = compute(settings, rounds);
    return { started: true, can_record: true, settings, started_at: 0, revision: 9, started_by: { id: "x", nickname: "x" },
      rounds: c.rounds.map((r) => ({ ...r, recorded_by: { id: "x", nickname: "x" }, recorded_at: 0 })), state: c.state };
  };
  it("最新的这一局已经是我要改成的：上一次其实写成功了，当成功", () => {
    expect(editAlreadyApplied(latest([f(1, 2), f(2, 3), f(1, 3)]), 2, f(2, 3))).toBe(true);
  });
  it("不一样：真的是别人改过", () => {
    expect(editAlreadyApplied(latest([f(1, 2), f(2, 3), f(1, 3)]), 2, f(4, 3))).toBe(false);
    expect(editAlreadyApplied(latest([f(1, 2)]), 2, f(2, 3))).toBe(false); // 没有这一局
    expect(editAlreadyApplied({ started: false, can_record: false }, 1, f(1, 2))).toBe(false);
    expect(editAlreadyApplied(undefined, 1, f(1, 2))).toBe(false);
  });
  it("不升级：三游改末游算同一个结果", () => {
    expect(editAlreadyApplied(latest([f(1, 3)], "no-level"), 1, f(1, 4))).toBe(true);
  });
});

describe("实时 score.round 拼接", () => {
  const local = { revision: 7, rounds: [{ round_no: 1 }, { round_no: 2 }] } as unknown as StartedScore;
  const ev = (revision: number, round_no: number) => ({ revision, round: { round_no } as ScoreRound });
  it("局号和 revision 都接上才拼", () => {
    expect(canAppendRound(local, ev(8, 3))).toBe(true);
  });
  it("revision 跳了（中间漏了一次 score.edited）：不拼，重新拉", () => {
    expect(canAppendRound(local, ev(9, 3))).toBe(false);
  });
  it("局号接不上、没有本地计分、事件没带这一局：不拼", () => {
    expect(canAppendRound(local, ev(8, 4))).toBe(false);
    expect(canAppendRound(null, ev(8, 3))).toBe(false);
    expect(canAppendRound(local, { revision: 8 })).toBe(false);
  });
});

describe("按每局当时的座位显示称呼（#172）", () => {
  const now = (s: number) => ({ 1: "老王", 2: "Lily", 3: "阿杰", 4: "小周" })[s] as string;
  const r = { seats: [{ id: "u1", nickname: "老王" }, { id: "u9", nickname: "大刘" }, null, { id: "u4", nickname: "小周" }] };
  it("用这一局的 seats；null 的座号退回现在的人；没有 seats 全用现在的", () => {
    const n = roundNames(r, now);
    expect([1, 2, 3, 4].map(n)).toEqual(["老王", "大刘", "阿杰", "小周"]);
    expect([1, 2, 3, 4].map(roundNames({}, now))).toEqual(["老王", "Lily", "阿杰", "小周"]);
    expect([1, 2, 3, 4].map(roundNames(undefined, now))).toEqual(["老王", "Lily", "阿杰", "小周"]);
    expect(whoLine(f(2, 3), n)).toBe("大刘 头游 · 小周 三游");
  });
  it("「我」也按那一局算", () => {
    const isMeNow = (s: number) => s === 2; // 现在 2 号是我
    expect(roundIsMe(r, 2, "u2", isMeNow)).toBe(false); // 那一局 2 号是大刘
    expect(roundIsMe(r, 2, "u9", isMeNow)).toBe(true);
    expect(roundIsMe(r, 3, "u2", isMeNow)).toBe(false); // null 的座号看现在
    expect(roundIsMe({}, 2, "u2", isMeNow)).toBe(true);
  });
});

describe("不升级玩法不写搭档名次（#174 第 6 项）", () => {
  it("不是双上只写头游；双上照写", () => {
    expect(howText(3, "no-level")).toBe("头游");
    expect(howText(4, "no-level")).toBe("头游");
    expect(howText(2, "no-level")).toBe("头游＋二游 · 双上");
    expect(howText(3)).toBe("头游＋三游");
    expect(whoLine(f(1, 3), names, "no-level")).toBe("老王 头游");
    expect(whoLine(f(1, 2), names, "no-level")).toBe("老王 头游 · 阿杰 二游");
    const { rounds } = compute(noLevel, [f(1, 3), f(2, 2)]);
    expect(resultLine("no-level", rounds[0])).toBe("红方 头游");
    expect(resultLine("no-level", rounds[1])).toBe("黑方 头游＋二游 · 双上");
  });
});

describe("记分中退出的说明（#168）", () => {
  it("没有候补：记分暂停、回原座位接着记；有候补：座位由候补第一位补上", () => {
    expect(leaveConfirmText([])).toBe("退出后这局记分会暂停，回到原座位可以继续");
    expect(leaveConfirmText([{ user: { nickname: "老张" } }, { user: { nickname: "小李" } }])).toBe("退出后座位会由候补第一位 老张 补上，记分接着记");
  });
});

describe("按 revision 去重", () => {
  it("事件只收比本地新的；REST 拿到的更新本地", () => {
    const g = new RevisionGate();
    expect(g.seen(5)).toBe(true);
    expect(g.accept(5)).toBe(false); // 自己提交的响应已经是 5，事件再来就丢
    expect(g.accept(4)).toBe(false);
    expect(g.accept(6)).toBe(true);
    expect(g.accept("7")).toBe(false);
    expect(g.seen(5)).toBe(false); // 慢到的旧 GET 不覆盖
    expect(g.seen(9)).toBe(true);
    expect(g.current).toBe(9);
  });
});

describe("时间", () => {
  const starts = 1_800_000_000;
  it("能不能开始看服务端 can_start；本机时钟只挑重新问的时刻", () => {
    const o = { startsAt: starts, canStart: false, started: false, eligible: true };
    expect(startRecheckDelay({ ...o, nowSec: starts - 31 * 60 })).toBe(62_000); // 到点后 2 秒问
    expect(startRecheckDelay({ ...o, nowSec: starts - 5 * 3600 })).toBe(600_000); // 离得远：最多 10 分钟问一次
    expect(startRecheckDelay({ ...o, nowSec: starts - 10 * 60 })).toBe(60_000); // 本机算着到了、服务端说不行：每分钟问
    expect(startRecheckDelay({ ...o, nowSec: starts - 31 * 60, canStart: true })).toBeNull();
    expect(startRecheckDelay({ ...o, nowSec: starts - 31 * 60, started: true })).toBeNull();
    expect(startRecheckDelay({ ...o, nowSec: starts - 31 * 60, eligible: false })).toBeNull();
  });
  it("一场的用时从开始记分或上一场最后一局算", () => {
    const r = (match: number, at: number) => ({ match, recorded_at: at }) as ScoreRound;
    const rounds = [r(1, 1000), r(1, 2000), r(2, 3000), r(2, 9000)];
    expect(matchDuration(400, rounds, 1)).toBe(1600);
    expect(matchDuration(400, rounds, 2)).toBe(7000);
    expect(durationShort(112 * 60)).toBe("1:52");
    expect(durationLong(112 * 60)).toBe("1 小时 52 分");
    expect(durationLong(38 * 60)).toBe("38 分");
    expect(durationLong(120 * 60)).toBe("2 小时");
    expect(durationLong(30)).toBe("不到 1 分");
  });
});
