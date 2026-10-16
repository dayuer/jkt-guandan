// 随机对局（#87 第 8 步）：固定种子跑**完整的一场** —— 发牌 → 进贡 → 出牌 → 下一副 ——
// 直到过 A 或者打满上限。
//
// 驱动器是一个随机 AI：轮到谁就从 `moves` 给的合法出法里随机挑一手（跟牌时按一定概率**故意过牌**
// —— 要不起才过的话，「压得住却过牌」这条路径几乎走不到）。AI 自己不做判断：出法全部来自 `moves`、
// 合法性全部由引擎校验，所以这一层查的是**两个模块合起来**对不对。
//
// **期望值由测试自己按规则推**（第二份实现，不看引擎的内部字段）：
//
//   - 该轮到谁 / 这一圈结束没有 / 这一副结束没有 / 借风了几次 —— `SimModel`，
//     依据第八条 一、1（逆时针、出完的人跳过）、一、2（其余人可过牌）、一、3（其余在场的人都过了
//     就结束这一圈、最后出牌的人领出）、第九条（领出的人已经出完时由搭档领出）、一、4（三人出完、
//     或者同一方拿了上游和二游，这一副结束）；
//   - 下一副打几 —— `refNext`，依据第二条 12（只有头游所在的队升级，搭档二游 +3、三游 +2、
//     末游 +1）、第二条 14（第 1 局打 2，之后打上一局赢的那队的级）、第二条 13（A 级必打：打本队
//     的 A 时头游且搭档不是末游 = 过 A，本场结束；没过就一直打 A）。
//
// 引擎算一遍、这里独立算一遍，逐步 / 逐副对。这一条是 Go 的
// `server/internal/rules/sim_test.go` 里 `simModel` 的同一套路 —— auditor-246 的反证就是：
// 把那几处改坏，原来那套 sim 全绿。
//
// **天生盲区**（和 Go 那边一样）：`moves` 漏了某个牌型，sim 看不出来 —— 它用的就是 moves 给的
// 出法，漏掉的那种压根不会出现在对局里。那种漏法由暴力枚举对拍守着（`moves.test.ts`）。
//
// 查的性质：
//
//   守恒     —— 每张牌要么在某家手里、要么已经打出去，不重不漏，总数就是一副牌的张数；
//   合法     —— 每一步都走合法的；**灌非法动作必须被拒、原因码对得上、而且局面一点没动**；
//   必然结束 —— 每一副都在有限步内分出上游，不会卡住；
//   可重放   —— 引擎不读时间、不读随机数：同一个种子跑两遍，逐副结果一样。
import { describe, expect, it } from "vitest";
import { ACE, BIG_JOKER, TEN, TWO, cardKey, isJoker, newDeck, type Card, type Rank } from "./card";
import { HANDS_EACH, SEATS, deal, permShuffler } from "./deal";
import { moves } from "./moves";
import { Game, RuleError, nextSeat, partnerSeat, type GameResult } from "./play";
import { cardPower, isWild } from "./power";
import { nextHand, type RoundFact, type Side } from "./scoring";
import { Tribute } from "./tribute";
import { handOf } from "./testing/cards";
import { seededRandom, type Rng } from "./testing/random";

/**
 * 每一步最多跟三手过牌（第四手过就结束这一圈，第八条 一、3），出牌手数最多 107 手
 * （一副 108 张、一手至少一张）→ 一副牌的步数上界。**推出来的**，不是量出来的。
 */
const MAX_STEPS_PER_DEAL = 4 * newDeck().length;

/** 一场最多打几副（防「永远不过 A」时死循环；正常几十副就结束了）。 */
const MAX_DEALS_PER_MATCH = 60;

/** 「明明压得住也过牌」的概率（百分比）：真实牌局里常见（留牌、让搭档先走）。 */
const PASS_PERCENT = 25;

// ───────────────────────────────────────────────────────────────────────────
// A：测试自己按规则推一遍「该轮到谁、这一圈结束没有、这一副结束没有」
// ───────────────────────────────────────────────────────────────────────────

/**
 * 只吃**动作序列**（谁出了几张、谁过了），不看引擎的内部字段。
 * 引擎自己算一遍、这里独立算一遍、逐步对比：下一家算错、借风给成别人、该结束不结束，这里都会立刻
 * 对不上。
 */
interface SimModel {
  /** 该轮到谁；0 表示这一副已经结束。 */
  turn: number;
  /** 这一圈最后一个出牌的人；0 表示这一圈还没人出（或者刚结束一圈）。 */
  last: number;
  /** `last` 之后连着过了几家。 */
  passes: number;
  /** 下标是座位号（1–4），0 不用：手里还剩几张。 */
  left: number[];
  /** 出完的先后。 */
  done: number[];
  /** 借风发生了几次（第九条那条路走到过没有，要看得见）。 */
  borrowed: number;
}

function newSimModel(hands: readonly Card[][], leader: number): SimModel {
  const left = [0, 0, 0, 0, 0];
  hands.forEach((h, i) => {
    left[i + 1] = h.length;
  });
  return { turn: leader, last: 0, passes: 0, left, done: [], borrowed: 0 };
}

const active = (m: SimModel, seat: number): boolean => m.left[seat] > 0;
const activeCount = (m: SimModel): number => [1, 2, 3, 4].filter((s) => active(m, s)).length;

/** 从 seat 逆时针数第一个还有牌的人。 */
function nextActiveSeat(m: SimModel, seat: number): number {
  for (let s = nextSeat(seat); s !== seat; s = nextSeat(s)) {
    if (active(m, s)) return s;
  }
  return 0;
}

/** 这一副结束了吗：三人出完，或者同一方拿了上游和二游（第八条 一、4）。 */
function modelOver(m: SimModel): boolean {
  if (m.done.length >= 3) return true;
  return m.done.length === 2 && partnerSeat(m.done[0]!) === m.done[1];
}

/**
 * 吃一个动作：`played` 是这一手打出去几张（过牌是 0）。
 *
 * 出牌：他成为这一圈的 last、passes 清零（第八条 一、1）；
 * 过牌：passes 加一，除了 last 之外在场的人都过了，这一圈就结束（第 _3 条），最后由 last 领出 ——
 * 他已经出完就由搭档领出（第九条，借风）。
 */
function modelStep(m: SimModel, seat: number, played: number): void {
  m.left[seat] -= played;
  if (m.left[seat] === 0) m.done.push(seat);
  if (modelOver(m)) {
    m.turn = 0;
    return;
  }
  if (played > 0) {
    m.last = seat;
    m.passes = 0;
    m.turn = nextActiveSeat(m, seat);
    return;
  }
  m.passes++;
  const need = activeCount(m) - (active(m, m.last) ? 1 : 0);
  if (m.passes < need) {
    m.turn = nextActiveSeat(m, seat);
    return;
  }
  let leader = m.last;
  if (!active(m, leader)) {
    leader = partnerSeat(leader);
    m.borrowed++;
  }
  m.last = 0;
  m.passes = 0;
  m.turn = leader;
}

/** 每一步之后和引擎对：轮到谁、出完的先后（还有没有结束时，再加一条「是不是领出」）。 */
function expectMatch(who: string, m: SimModel, g: Game): void {
  expect(g.turn(), `${who}：轮到谁`).toBe(m.turn);
  expect(g.finished(), `${who}：出完的先后`).toStrictEqual(m.done);
  if (m.turn !== 0) expect(g.leading(), `${who}：是不是领出`).toBe(m.last === 0);
}

// ───────────────────────────────────────────────────────────────────────────
// B：发牌（引擎不掷骰子，随机源从外面注入）
// ───────────────────────────────────────────────────────────────────────────

/** 一个随机排列：`shuffle[i]` 是第 i 张发出去的牌的 ID。 */
function randomPerm(rng: Rng, n: number): number[] {
  const perm = Array.from({ length: n }, (_, i) => i);
  for (let i = n - 1; i > 0; i--) {
    const j = rng.int(i + 1);
    const t = perm[i]!;
    perm[i] = perm[j]!;
    perm[j] = t;
  }
  return perm;
}

/**
 * 发一副牌，并把「首抓者起逆时针第几家」旋转成座位号：首抓者坐 `leader` 号座，其余按逆时针排
 * （`deal` 的下标不是座位号）。
 */
function dealFor(rng: Rng, leader: number): Card[][] {
  const hands: Card[][] = [[], [], [], []];
  deal(permShuffler(randomPerm(rng, newDeck().length))).forEach((h, k) => {
    hands[(leader - 1 + k) % SEATS] = h;
  });
  return hands;
}

// ───────────────────────────────────────────────────────────────────────────
// C：一副牌打到底（随机 AI + 逐步对模型 + 灌非法动作）
// ───────────────────────────────────────────────────────────────────────────

/** 局面快照：灌非法动作之后要一字不差地还原。 */
function snapOf(g: Game): string {
  const hands = [1, 2, 3, 4].map((s) => g.hand(s).map(cardKey).join(" "));
  return [g.turn(), g.leading(), g.finished().join(","), g.moves().length, hands.join(" | ")].join(" / ");
}

/** 灌一个必须被拒的动作：确实被拒了、原因码对得上、局面一个字都没动。 */
function rejectWith(g: Game, reason: string, act: () => void): void {
  const before = snapOf(g);
  let err: unknown;
  try {
    act();
  } catch (e) {
    err = e;
  }
  expect(err, `灌的非法动作该被拒（指望 ${reason}）`).toBeInstanceOf(RuleError);
  expect((err as RuleError).reason, "被拒的原因码").toBe(reason);
  expect(snapOf(g), "被拒之后局面一个字都不该动").toBe(before);
}

/**
 * 偶尔灌几个非法动作。挑牌要挑得**只**违背想试的那一条 —— 比如试「不轮到你也得出牌」时，
 * 得挑一家手里真有牌的，否则先被 `cards-not-in-hand` 拦下来，就试不出想试的那条了
 * （jkt 在 #251 踩过这个坑）。
 */
function probeIllegal(g: Game, seat: number, step: number, level: Rank): void {
  if (step % 7 !== 1) return;

  const other = nextSeat(seat);
  const otherHand = g.hand(other);
  if (otherHand.length > 0) {
    const c = otherHand[0]!;
    rejectWith(g, "not-your-turn", () => g.play(other, [c], "single", c.rank));
  }
  if (g.leading()) {
    rejectWith(g, "leading-cannot-pass", () => g.pass(seat));
  }
  const donor = [1, 2, 3, 4].find((s) => g.hand(s).length > 0)!;
  const notMine = g.hand(donor).find((c) => !g.hand(seat).some((mine) => mine.id === c.id));
  if (notMine) {
    rejectWith(g, "cards-not-in-hand", () => g.play(seat, [notMine], "single", notMine.rank));
  }

  // 压不住上一手：挑一张**单张**，它一定压不住这一手 ——
  // 上一手不是单张时，单张和它「没得比」（incomparable）；是单张就挑牌力更小的那张。
  const last = g.lastPlay();
  if (last) {
    const isSingle = last.combo.type === "single";
    const low = isSingle ? cardPower(last.combo.cards[0]!, level) : -1;
    const victim = g.hand(seat).find((c) => cardPower(c, level) < low || !isSingle);
    if (victim) {
      rejectWith(g, "does-not-beat", () => g.play(seat, [victim], "single", victim.rank));
    }
  }
}

/** 打完已经摆好的这一副：从 `g` 现在的局面一路走到分出上游。 */
function playDeal(rng: Rng, level: Rank, g: Game, m: SimModel): { steps: number; borrowed: number; ledger: string } {
  let steps = 0;
  while (!g.over()) {
    if (++steps > MAX_STEPS_PER_DEAL) {
      throw new Error(`这一副打不完（步数超上限 ${MAX_STEPS_PER_DEAL}）—— 引擎卡住了`);
    }
    const seat = g.turn();
    expect(seat, "模型和引擎轮到的不是同一个人").toBe(m.turn);

    probeIllegal(g, seat, steps, level);

    const lead = g.lastPlay();
    const cands = moves(g.hand(seat), level, lead?.combo);
    if (g.leading()) {
      // 领出（手里有牌）时一定有能出的：一张都没有就是 `moves` 漏了牌型。
      expect(cands.length, "领出时该有能出的牌").toBeGreaterThan(0);
    }
    const pass = !g.leading() && (cands.length === 0 || rng.int(100) < PASS_PERCENT);

    if (pass) {
      g.pass(seat);
      modelStep(m, seat, 0);
    } else {
      const mv = cands[rng.int(cands.length)]!;
      g.play(seat, mv.cards, mv.type, mv.rank === 0 ? undefined : mv.rank);
      modelStep(m, seat, mv.cards.length);
    }
    expectMatch(`第 ${steps} 步（${seat} 号座${pass ? "过牌" : "出牌"}）之后`, m, g);
  }

  // 守恒：出掉的 + 手里的 = 一副牌的张数（每张牌要么在某家手里、要么已经打出去）。
  const played = g.moves().reduce((n, mv) => n + (mv.pass ? 0 : (mv.combo?.cards.length ?? 0)), 0);
  const held = [1, 2, 3, 4].reduce((n, s) => n + g.hand(s).length, 0);
  expect(played + held, "出掉的 + 手里的 = 一副牌").toBe(newDeck().length);

  const result = g.result();
  expect(result, "打完了就该有结果").toBeDefined();
  expect(result!.partnerPlace, "搭档名次该是 2–4").toBeGreaterThanOrEqual(2);
  return { steps, borrowed: m.borrowed, ledger: dealLedger(g, m) };
}

/** 这一副的账：每步谁出了什么（或过牌），加上**测试自己推的**出完顺序。 */
function dealLedger(g: Game, m: SimModel): string {
  const moves = g.moves().map((mv) =>
    mv.pass ? `${mv.seat}:过` : `${mv.seat}:${(mv.combo?.cards ?? []).map(cardKey).join("+")}`,
  );
  return `${moves.join(" ")} | 出完=${m.done.join(",")}`;
}

/**
 * 这一副最终的出完顺序：模型里出完的先后 + 没出完的按座位号排在后面（第二条 10、
 * `play.go` 的 `Result` 同一个口径）。**由模型推**，不读引擎的 `result()`。
 */
function finalOrder(m: SimModel): number[] {
  const rest = [1, 2, 3, 4].filter((s) => !m.done.includes(s)).sort((a, b) => a - b);
  return [...m.done, ...rest];
}

/** 把「出完的顺序」还原成上一副的结果（三个字段都由顺序推得）。 */
function prevFromOrder(order: readonly number[]): GameResult {
  return { order: [...order], headSeat: order[0]!, partnerPlace: refPartnerPlace(order) };
}

// ───────────────────────────────────────────────────────────────────────────
// D：独立推「下一副打几」（第二条 12–14、第十三条）
// ───────────────────────────────────────────────────────────────────────────

/**
 * 1、3 一方，2、4 一方 —— **本地另写一份**，不用 `scoring` 的 `teamOf`。
 *
 * 不共用是有意的：`teamOf` 被改坏时，如果这里也调它，两边一起错、谁都看不出来
 * （#256 复审：那条突变原来整个 rules 套件都是绿的，只有小游戏的测试红）。
 */
const refTeam = (seat: number): Side => (seat % 2 === 1 ? "red" : "black");

/**
 * 顺时针的下一家：座位 1→2→3→4 是**逆时针**，所以顺时针是 s−1（1 号座的是 4 号座）。
 * 同样本地另写，不用 `rules` 的 `ClockwiseSeat`。
 */
const refClockwise = (seat: number): number => ((seat + SEATS - 2) % SEATS) + 1;

/** 上一副出完的顺序里，上游的搭档第几个出完（2 二游 / 3 三游 / 4 末游）。 */
function refPartnerPlace(order: readonly number[]): number {
  return order.indexOf(partnerSeat(order[0]!)) + 1;
}

/** 按第十条独立推出来的一份贡牌过程。 */
interface RefTribute {
  kind: "single" | "double" | "resisted";
  /** 要进贡的人，按座位号从小到大。 */
  givers: number[];
  /** 进贡的人 → 贡给谁（抗贡时是空的）。 */
  to: Map<number, number>;
  /** 首圈领出的人。 */
  leader: number;
  /** 每个人该贡哪一张（驱动器照着贡；引擎必须接受）。 */
  give: Map<number, Card>;
  /**
   * 双贡时两张贡牌**一样大**（第十条 三：一样大就按顺时针方向进贡）。
   *
   * 单列出来是为了**钉覆盖面**：这条分支随机发牌很难撞上，主用例以前从没走到过它
   * （#256 复审发现的缺口）—— 没走到就等于没核，下面的主用例拿它断言 `> 0`。
   */
  equal: boolean;
}

/**
 * 按**第十条**独立推这一副的进贡：谁要贡、抗不抗贡、谁贡给谁、首圈谁领出。
 *
 * **只用两样输入**：上一副出完的顺序（测试自己的 `SimModel` 推的）、这一副发到的四手牌。
 * **不看引擎的任何输出** —— 不看 `givers()` / `kind()` / `receiver()`，也不看 `g.turn()`。
 * 这就是 #251 复审定下的要求：期望值要能独立推出来，否则引擎那边改坏了也没人知道。
 *
 * 依据：
 *   第十条 二：单贡 —— 从第二副牌起，下游把手里最大的一张（红心级牌除外）贡给上游；
 *               首圈由下游领出。
 *   第十条 三：双贡 —— 双下时双下方两人都贡，上游拿牌点大的那张、搭档拿小的；
 *               两张一样大时按顺时针方向进贡；还牌后由贡给上游的那位领出。
 *   第十条 四：抗贡 —— 要进贡的一方（双下时两人合计）抓到两个大王，就不贡不还，上游领出。
 */
function refTribute(hands: readonly Card[][], level: Rank, order: readonly number[]): RefTribute {
  const head = order[0]!;
  const partner = partnerSeat(head);
  const double = refPartnerPlace(order) === 2;

  // 谁要贡：双下是对方两家；单贡是下游（出完顺序里的最后一个）。
  const givers = double
    ? [1, 2, 3, 4].filter((s) => s !== head && s !== partner)
    : [order[SEATS - 1]!];
  givers.sort((a, b) => a - b);

  const give = new Map<number, Card>();
  for (const g of givers) give.set(g, biggestNonWild(hands[g - 1]!, level));

  // 抗贡：要贡的一方**合计**两个大王（两副牌一共就两个大王，所以就是「都在这边」）。
  let bigJokers = 0;
  for (const g of givers) {
    for (const c of hands[g - 1]!) if (c.rank === BIG_JOKER) bigJokers++;
  }
  if (bigJokers >= 2) {
    return { kind: "resisted", givers, to: new Map(), leader: head, give, equal: false };
  }

  const to = new Map<number, number>();
  let leader = head;
  /** 两张贡牌是不是一样大（只有双贡才有这回事）。 */
  let equal = false;
  if (!double) {
    to.set(givers[0]!, head); // 单贡贡给上游
    leader = givers[0]!; // 首圈由下游领出
  } else {
    const [a, b] = givers as [number, number];
    const pa = cardPower(give.get(a)!, level);
    const pb = cardPower(give.get(b)!, level);
    if (pa > pb) {
      to.set(a, head); // 大的给上游
      to.set(b, partner);
    } else if (pa < pb) {
      to.set(b, head);
      to.set(a, partner);
    } else {
      equal = true; // 一样大：按顺时针方向进贡（第十条 三）
      to.set(a, refClockwise(a));
      to.set(b, refClockwise(b));
    }
    for (const [g, r] of to) if (r === head) leader = g; // 贡给上游的那位领出
  }
  return { kind: double ? "double" : "single", givers, to, leader, give, equal };
}

/**
 * 第二份「下一副打几」：只按条文推，**不看 `scoring` 的实现**。
 * 那边是一个 `ScoreState` 一路 apply；这里把两队的级数、本场状态分开记 —— 摆法和算法都另写一套，
 * 只共享同一份规则条文。
 */
interface RefMatch {
  match: number;
  /** 本场打了几局（`state.match_rounds`）。 */
  rounds: number;
  playing: "" | Side;
  playingLevel: number;
  levels: Record<Side, number>;
  aFails: Record<Side, number>;
  wins: Record<Side, number>;
  doubleUps: Record<Side, number>;
  over: boolean;
  winner?: Side;
}

const newRefMatch = (): RefMatch => ({
  match: 1,
  rounds: 0,
  playing: "",
  playingLevel: 2,
  levels: { red: 2, black: 2 },
  aFails: { red: 0, black: 0 },
  wins: { red: 0, black: 0 },
  doubleUps: { red: 0, black: 0 },
  over: false,
});

/** 一局的事实换成**接口那套字段**（`ScoreState`），整份和引擎比。 */
function refState(ref: RefMatch) {
  const team = (t: Side) => ({
    level: ref.levels[t],
    a_fails: ref.aFails[t],
    wins: ref.wins[t],
    double_ups: ref.doubleUps[t],
  });
  const st: Record<string, unknown> = {
    match: ref.match,
    match_rounds: ref.rounds,
    playing: ref.playing,
    playing_level: ref.playingLevel,
    red: team("red"),
    black: team("black"),
    match_over: ref.over,
  };
  if (ref.over) st.winner = ref.winner;
  return st;
}

/** 记一局，推到下一副。 */
function refNext(ref: RefMatch, headSeat: number, partnerPlace: number): void {
  if (ref.over) {
    // 本场已结束：这一局是下一场的第 1 局，两队都从 2 开始（第十二条）。
    ref.match++;
    ref.rounds = 0;
    ref.playing = "";
    ref.playingLevel = 2;
    ref.levels = { red: 2, black: 2 };
    ref.aFails = { red: 0, black: 0 };
    ref.wins = { red: 0, black: 0 };
    ref.doubleUps = { red: 0, black: 0 };
    ref.over = false;
    delete ref.winner;
  }
  const winner = refTeam(headSeat);
  ref.rounds++;
  ref.wins[winner]++;
  if (partnerPlace === 2) ref.doubleUps[winner]++;

  const playingOwnA = ref.playing !== "" && ref.levels[ref.playing] === ACE;
  // 第二条 13：打本队的 A 时头游、搭档不是末游 = 过 A，本场结束（级数不再动）。
  if (playingOwnA && winner === ref.playing && partnerPlace !== 4) {
    ref.over = true;
    ref.winner = winner;
    ref.playing = "";
    ref.playingLevel = 2;
    return;
  }
  // 第二条 12：只有头游所在的队升级，搭档二游 +3、三游 +2、末游 +1；到 A 封顶（第二条 13）。
  ref.levels[winner] = Math.min(ref.levels[winner] + (5 - partnerPlace), ACE);
  if (playingOwnA) ref.aFails[ref.playing as Side]++; // A 必打：没过记一次，级数不动
  ref.playing = winner;
  ref.playingLevel = ref.levels[winner]; // 第二条 14：下一副打上一局赢的那一队的级
}

// ───────────────────────────────────────────────────────────────────────────
// E：进贡/还贡由驱动器按规则挑牌（引擎只校验）
// ───────────────────────────────────────────────────────────────────────────

/**
 * 读贡牌过程中的四家手牌。
 *
 * `hands()` 按口径要求「贡完还完」才给，而驱动器要边走边看 —— 所以直接读私有字段
 * （只有测试这么干；Go 那边同样是同包直读 `tr.hands`）。
 */
function tributeHands(tr: Tribute): Card[][] {
  return (tr as unknown as { handCards: Card[][] }).handCards;
}

/** 手里最大的一张（红心级牌除外，第十条 二）。 */
function biggestNonWild(hand: readonly Card[], level: Rank): Card {
  let best: Card | undefined;
  for (const c of hand) {
    if (isWild(c, level)) continue;
    if (best === undefined || cardPower(c, level) > cardPower(best, level)) best = c;
  }
  // 手里至少有一张非逢人配的牌：逢人配最多两张，而 27 张手里剩下 25 张都是实牌。
  return best!;
}

/** 该还哪张：有牌面不超过 10 的还那种（最小的），全都大于 10 时还最小的（第十条 二）。 */
function returnable(hand: readonly Card[], level: Rank): Card {
  const small = hand.filter((c) => !isJoker(c.rank) && c.rank <= TEN);
  const pool = small.length > 0 ? small : hand;
  let best = pool[0]!;
  for (const c of pool) {
    if (cardPower(c, level) < cardPower(best, level)) best = c;
  }
  return best;
}


// ───────────────────────────────────────────────────────────────────────────
// F：一场打到底
// ───────────────────────────────────────────────────────────────────────────

interface MatchLog {
  /** 是「过 A 结束」还是撞上了副数上限（上限只是防死循环，撞上说明那里不对）。 */
  over: boolean;
  deals: number;
  /**
   * **整串账**：每一副的动作（每步谁出了什么、谁过了）+ 测试自己推出来的出完顺序。
   * 「同一个种子跑两遍一样」那条比的就是它（对齐 Go 的整串账口径）。
   */
  ledger: string[];
  steps: number;
  tributeDeals: number;
  /**
   * 走到「双贡**两张一样大** → 按顺时针方向进贡」这条分支的副数。**按上游坐哪儿分成两类**，
   * 两类都得走到 —— 只数一个、或者只数「走到过」，都会漏掉一种「写反」（#261 复审）。
   *
   * 为什么必须分两类：双下方两家排序后是 a < b，正确的读法是「各贡给自己顺时针那家」。
   * 把它写反有两种写法，而**哪一种看不出来，取决于打平时谁被送去上游**：
   *
   *   - 上游坐 **2/3** 号座：正确读法把 **b**（大号座那个）送去上游
   *     → 「a→上游、b→搭档」这种写反（M2）在这些局面里与正确读法**完全一致**，看不出来；
   *     这一类局面能抓的是另一种写反（M3：b→上游、a→搭档）。
   *   - 上游坐 **1/4** 号座：正确读法把 **a** 送去上游 → 上面两种写反对调：
   *     这一类能抓 M2，抓不到 M3。
   *
   * 换句话说：**重不重合看「打平时谁给了上游」，不看座位本身**。两类语料各断言 > 0，
   * 是因为少了任何一类，都还有一整种写反能活着。
   */
  doubleEqualSeat23: number;
  /** 同上，上游坐 1/4 号座。和 `doubleEqualSeat23` 是一对，两个都要 > 0。 */
  doubleEqualSeat14: number;
  resistedDeals: number;
  borrowed: number;
  levels: number[];
}

/**
 * 打完一场：从 2 打起，每副「发牌 →（该进贡就进贡还贡）→ 打完 → 算下一副打几」，
 * 一路打到过 A 或者打满上限。
 */
function playMatch(rng: Rng, maxDeals = MAX_DEALS_PER_MATCH): MatchLog {
  const log: MatchLog = { over: false, deals: 0, steps: 0, tributeDeals: 0, doubleEqualSeat23: 0, doubleEqualSeat14: 0, resistedDeals: 0, borrowed: 0, levels: [], ledger: [] };
  const ref = newRefMatch();
  const facts: RoundFact[] = [];
  let level: Rank = TWO;
  /** 上一副出完的顺序 —— **测试自己推的**（`finalOrder`），不是引擎的 `result()`。 */
  let prevOrder: number[] | null = null;
  /** 第一副不进贡（第十条 二「从第二副牌开始」）。 */
  let needTribute = false;

  while (log.deals < maxDeals) {
    // 首抓者坐 1 号座（这一层只关心「谁进贡、谁领出」，和首抓者是谁无关）。
    const hands = dealFor(rng, 1);
    let g: Game;
    if (!needTribute) {
      // 不进贡的这一副：谁领出由调用方给。
      g = new Game(level, hands, 1);
    } else {
      // 按**第十条独立推**这一副的进贡，再和引擎对 —— 不看 givers()/kind()/receiver()/g.turn()。
      // 以前这里直接拿引擎那几样往下走，所以「双贡分配反」「抗贡只看一人」「单贡首圈领出错」
      // 那一批突变全都能活着（#256 复审）。
      const rt = refTribute(hands, level, prevOrder!);
      const tr = new Tribute(level, hands, prevFromOrder(prevOrder!));
      log.tributeDeals++;
      // 见 MatchLog.doubleEqualSeat23 / Seat14：两类都要数 —— 它们各能抓一种「写反」。
      if (rt.kind === "double" && rt.equal) {
        if (prevOrder![0] === 2 || prevOrder![0] === 3) log.doubleEqualSeat23++;
        else log.doubleEqualSeat14++;
      }
      expect(tr.kind(), `第 ${log.deals + 1} 副：单贡还是双贡、抗不抗贡`).toBe(rt.kind);
      expect(tr.givers(), `第 ${log.deals + 1} 副：谁要进贡`).toStrictEqual(rt.givers);

      if (rt.kind === "resisted") {
        log.resistedDeals++;
        expect(tr.done(), "抗贡了就不贡不还，一上来就该算完").toBe(true);
      } else {
        // 贡：用推出来的那张牌，引擎必须收下。
        for (const gi of rt.givers) tr.give(gi, rt.give.get(gi)!);
        for (const gi of rt.givers) {
          expect(tr.receiver(gi), `${gi} 号座贡给谁`).toBe(rt.to.get(gi));
        }
        // 还：收贡的人还给贡给自己的人；还完两边各一张，张数不变。
        for (const gi of rt.givers) {
          const seat = rt.to.get(gi)!;
          const back = returnable(tributeHands(tr)[seat - 1]!, level);
          const before = tributeHands(tr)[seat - 1]!.length;
          tr.returnCard(seat, back);
          expect(tributeHands(tr)[seat - 1]!.length, "还了一张出去，收贡的人该少一张").toBe(before - 1);
          expect(
            tributeHands(tr)[gi - 1]!.some((c) => c.id === back.id),
            "还出去的牌该在进贡的人手里",
          ).toBe(true);
          expect(
            tributeHands(tr)[seat - 1]!.some((c) => c.id === rt.give.get(gi)!.id),
            "贡进来的牌该在收贡的人手里",
          ).toBe(true);
        }
        expect(tr.done(), "贡完还完该算完了").toBe(true);
        // 贡一张、还一张，四家的张数一张都不该变。
        for (let s = 1; s <= SEATS; s++) {
          expect(tributeHands(tr)[s - 1]!.length, `${s} 号座贡完还完该还是 ${HANDS_EACH} 张`).toBe(HANDS_EACH);
        }
      }
      g = tr.start();
      expect(g.turn(), "首圈领出的人").toBe(rt.leader);
    }
    log.levels.push(level);

    // 模型从**这一副真正开打时的**手牌和领出人起算（进贡还完之后的那一副）。
    const m = newSimModel([g.hand(1), g.hand(2), g.hand(3), g.hand(4)], g.turn());
    const { steps, borrowed, ledger } = playDeal(rng, level, g, m);
    log.deals++;
    log.steps += steps;
    log.borrowed += borrowed;
    log.ledger.push(ledger);

    // 出完的顺序**由模型推**，再和引擎的结果对 —— 以前是直接拿 `g.result()` 往下走的，
    // 那样 `result()` 被改坏也没人知道（两边一起错）。
    const order = finalOrder(m);
    const res = g.result();
    expect(res, "打完了就该有结果").toBeDefined();
    expect(res!.order, "引擎的出完顺序该等于自己推的").toStrictEqual(order);
    expect(res!.headSeat, "引擎的上游该等于自己推的").toBe(order[0]);
    expect(res!.partnerPlace, "引擎的搭档名次该等于自己推的").toBe(refPartnerPlace(order));

    const head = order[0]!;
    const place = refPartnerPlace(order);
    facts.push({ head_seat: head, partner_place: place });

    // 独立推一遍下一副打几、本场结束没有，和引擎对。
    refNext(ref, head, place);
    const engine = nextHand(facts);
    // **整份状态**和独立推的比 —— 只比级数的话，「红黑标签对调」那种错看不出来
    // （级数会一致地跟着换名字，数字一点没变，#256 复审）。
    expect(engine.score, `第 ${log.deals} 副之后：整份计分状态（独立推的）`).toStrictEqual(refState(ref));
    expect(engine.level, `第 ${log.deals} 副之后：下一副打几`).toBe(ref.playingLevel);

    // 「要不要进贡」：走到这里这一场已经打过至少一副（第十条 二），所以只剩「本场刚结束」这一种
    // 不进贡 —— 下一副是新一场的第一副。
    expect(engine.tribute, `第 ${log.deals} 副之后：要不要进贡`).toBe(!ref.over);

    if (ref.over) break;
    level = engine.level as Rank;
    needTribute = engine.tribute;
    prevOrder = order;
  }
  log.over = ref.over;
  return log;
}

// ───────────────────────────────────────────────────────────────────────────
// 测试
// ───────────────────────────────────────────────────────────────────────────

describe("性质：随机对局（完整的一场：发牌 → 进贡 → 出牌 → 下一副）", () => {
  it("固定种子跑若干场：守恒、合法、必然结束，每步都和独立推的模型对得上", { timeout: 30_000 }, () => {
    let deals = 0;
    let steps = 0;
    let tributeDeals = 0;
    let doubleEqualSeat23 = 0;
    let doubleEqualSeat14 = 0;
    let resisted = 0;
    let borrowed = 0;
    // 语料取 5 个种子（~75 副、八九千步）。这只是一条**下界**：真正的覆盖面由下面几条断言钉着，
    // 而这条测试本来就重（每步都要和独立推的模型对、每副都要整份比计分状态）。
    //
    // 7 号和 9 号是**特意挑进来的**，一个一类：前 4 个种子一次都撞不上「双贡两张一样大」
    // （那条分支随机很难遇到，实测 80 个种子里 28 个能撞上、每次大约一副），而且光是撞上还不够
    // —— 这一类局面**分两种摆法**（上游坐 2/3 号座、上游坐 1/4 号座），少了任何一种，
    // 都有整整一种「把方向写反」的方式能活着（#261 复审）。实测：能走到「上游坐 2/3」的种子
    // 80 个里 14 个（9 号是其中一个），能走到「上游坐 1/4」的是**另外 14 个**（7 号）——
    // 两类互不重叠，所以两个种子都要。
    //
    // 这不是挑好看的数字，是挑覆盖面 —— 下面那两条断言的数字就是钉它的：哪天子料/发牌变了、
    // 哪一类又走不到了，测试会红，而不是悄悄少核一种写反。
    const seeds = [1, 2, 3, 4, 7, 9];
    for (const seed of seeds) {
      const log = playMatch(seededRandom(seed));
      // 一场总有打完的时候（过 A）：撞上副数上限就说明「打不完」——那正是要抓的。
      expect(log.over, `种子 ${seed} 的那一场打满 ${MAX_DEALS_PER_MATCH} 副都没过 A`).toBe(true);
      deals += log.deals;
      steps += log.steps;
      tributeDeals += log.tributeDeals;
      doubleEqualSeat23 += log.doubleEqualSeat23;
      doubleEqualSeat14 += log.doubleEqualSeat14;
      resisted += log.resistedDeals;
      borrowed += log.borrowed;
    }
    // 覆盖面：不能「跑是跑了、什么路径都没走到」。进贡和借风这两条路必须真的走到过。
    expect(deals, "一共打了多少副").toBeGreaterThan(20);
    expect(tributeDeals, "进贡过的副数（第 2 副起才有）").toBeGreaterThan(10);
    expect(borrowed, "借风发生过多少次").toBeGreaterThan(0);
    // 抗贡也要真的走到过：这条以前写的是 `>= 0`（恒真）—— 等于没核（#256 复审）。
    expect(resisted, "抗贡过的副数").toBeGreaterThan(0);
    // 双贡**两张一样大**（第十条 三：按顺时针进贡）这条路要真的走到，而且**两类摆法**都要走到
    // （上游坐 2/3 与坐 1/4，见 MatchLog.doubleEqualSeat23 的注释）—— 只走一类的话，
    // 另一种「写反」照样能活着（#261 复审就是这么发现的）。随机发牌很难撞上，以前主用例从没走到，
    // 等于那条分支没人核（#256 复审的缺口）。数到 0 就说明语料还不够：换种子或加种子，不许删断言。
    expect(doubleEqualSeat23, "双贡同大、上游坐 2/3 号座的副数").toBeGreaterThan(0);
    expect(doubleEqualSeat14, "双贡同大、上游坐 1/4 号座的副数").toBeGreaterThan(0);
  });

  it("同一个种子跑两遍：逐副结果一模一样（引擎不读时间、不读随机数）", () => {
    const a = playMatch(seededRandom(20261016), 4);
    const b = playMatch(seededRandom(20261016), 4);
    // 先确认这串账**真的记了东西**：每副一条、每条都有动作和出完顺序 ——
    // 不然「两遍一样」可能只是两个空串（恒真的另一副面孔）。
    expect(a.ledger, "一副一条账").toHaveLength(a.deals);
    for (const line of a.ledger) {
      expect(line, "每条账都该有出完顺序").toContain("出完=");
      expect(line.length, "每条账都该有动作").toBeGreaterThan(10);
    }
    expect(a, "同一个种子两遍该完全一样").toStrictEqual(b);
  });

  it("进贡还贡：贡来的进了收贡人手里、还回去的进了进贡人手里（一手固定的牌，期望值手写）", () => {
    // 单贡：上游 1 号座、搭档第 3 → 下游 4 号座要贡。4 号手里最大的一张是 ♠A/0
    // （♥7/0 是逢人配，不算）；1 号座还一张不超过 10 的 → ♣5/0。
    const hands = [
      handOf(["♠K/0", "♣5/0"]),
      handOf(["♠3/0"]),
      handOf(["♠4/0"]),
      handOf(["♠A/0", "♥7/0"]),
    ];
    const rt = refTribute(hands, TWO, [1, 2, 3, 4]);
    expect(rt.kind, "这套牌不抗贡，是单贡").toBe("single");
    expect(rt.givers, "要贡的是下游 4 号座（第十条 二）").toStrictEqual([4]);
    expect(rt.leader, "单贡之后由下游领出（第十条 二）").toBe(4);
    expect(cardKey(rt.give.get(4)!), "贡的是手里最大的一张（红心级牌除外）").toBe("♠A/0");

    const tr = new Tribute(TWO, hands, prevFromOrder([1, 2, 3, 4]));
    const keysOf = (seat: number) => tributeHands(tr)[seat - 1]!.map(cardKey).sort();

    expect(tr.kind()).toBe("single");
    expect(tr.givers()).toStrictEqual([4]);

    tr.give(4, rt.give.get(4)!);
    expect(tr.receiver(4), "单贡贡给上游").toBe(1);
    expect(keysOf(4), "贡出去就从手里没了").toStrictEqual(["♥7/0"]);
    expect(keysOf(1), "贡来的进了收贡人手里").toStrictEqual(["♠A/0", "♠K/0", "♣5/0"]);

    tr.returnCard(1, returnable(tributeHands(tr)[0]!, TWO));
    expect(keysOf(1), "还一张出去，收贡的人少一张").toStrictEqual(["♠A/0", "♠K/0"]);
    expect(keysOf(4), "还回来的进了进贡人手里").toStrictEqual(["♣5/0", "♥7/0"]);
    expect(tr.done()).toBe(true);
    expect(tr.start().turn(), "首轮还是 4 号座领出").toBe(4);
  });

  it("双贡同大：两张贡牌一样大时按顺时针方向进贡（第十条 三，期望值手写）", () => {
    // 上一副出完顺序 2 → 4 → …：2 号座上游、搭档 4 号座**第二个出完** → 双下（搭档名次 2），
    // 双下方两家（1、3 号座）都要贡。两家手里最大的一张**一样大**：都是 A，一个 ♠、一个 ♥
    // （打 2 时配牌是 ♥2，♥A 不是配牌）—— 正好落在「两张一样大」那一条上。
    //
    // 上游**特意坐 2 号座**：打平时谁该给上游是按座位顺序定的，两种摆法各能抓一种「写反」——
    // 上游坐 2/3 号座时，正确读法把**大号座那个**贡的人送去上游，所以这种摆法抓得住
    // 「小号座→上游、大号座→搭档」那一种；另一种写反（大号座→上游、小号座→搭档）在这类摆法下
    // 与正确读法**一模一样**，得靠下面那条「上游坐 1 号座」的用例（#261 复审）。
    const hands = [
      handOf(["♠A/0"]), // 1 号座（要贡）
      handOf(["♠K/0", "♣5/0"]), // 2 号座（上游）
      handOf(["♥A/0"]), // 3 号座（要贡）
      handOf(["♠4/0"]), // 4 号座（上游的搭档）
    ];
    const order = [2, 4, 1, 3];

    // 条文（第十条 三）：一样大时每个进贡的人贡给自己**顺时针**方向的那一家。
    // 座位 1 → 2 → 3 → 4 是逆时针，所以顺时针是 s−1（1 号座的是 4 号座）：
    // 1 号座 → 4 号座（搭档）、3 号座 → 2 号座（上游）。这两句是照条文手写的，
    // 不是从 refTribute 抄的 —— 引擎自己那套实现把方向写反，这条用例照样红。
    const tr = new Tribute(TWO, hands, prevFromOrder(order));
    const keysOf = (seat: number) => tributeHands(tr)[seat - 1]!.map(cardKey).sort();

    expect(tr.kind(), "搭档第二个出完 → 双下 → 双贡").toBe("double");
    expect(tr.givers(), "要贡的是对方两家（1、3 号座）").toStrictEqual([1, 3]);

    tr.give(1, hands[0]![0]!); // ♠A/0，1 号座手里最大的一张
    tr.give(3, hands[2]![0]!); // ♥A/0，3 号座手里最大的一张
    expect(tr.receiver(1), "一样大：1 号座顺时针贡给 4 号座").toBe(4);
    expect(tr.receiver(3), "一样大：3 号座顺时针贡给 2 号座").toBe(2);

    // 各自还给贡给自己的人。还的牌都不超过 10（♣5、♠4 在读法 A 和读法 B 下都算），
    // 所以这条用例不碰「还什么」那个读法（已按读法 A 拍板，见 testdata/rules/README.md）。
    tr.returnCard(4, returnable(tributeHands(tr)[3]!, TWO));
    tr.returnCard(2, returnable(tributeHands(tr)[1]!, TWO));
    expect(tr.done()).toBe(true);
    expect(keysOf(2), "上游：进来 ♥A/0、出去 ♣5/0").toStrictEqual(["♠K/0", "♥A/0"]);
    expect(keysOf(4), "搭档：进来 ♠A/0、出去 ♠4/0").toStrictEqual(["♠A/0"]);
    expect(keysOf(1), "1 号座：贡出 ♠A/0、收回 ♠4/0").toStrictEqual(["♠4/0"]);
    expect(keysOf(3), "3 号座：贡出 ♥A/0、收回 ♣5/0").toStrictEqual(["♣5/0"]);
    expect(tr.start().turn(), "贡给上游的那位（3 号座）领出").toBe(3);
  });

  it("双贡同大：上游坐 1 号座（同一件事的另一类摆法，期望值手写）", () => {
    // 和上面那条是**同一件事**、另一个摆法：上游坐 1 号座时，正确读法把**小号座那个**贡的人
    // （2 号座）送去上游，所以这一类抓的是「大号座→上游、小号座→搭档」那一种写反 ——
    // 上面那条抓不到它（反之亦然）。两条一起，两种写反都跑不掉。
    const hands = [
      handOf(["♠K/0", "♣5/0"]), // 1 号座（上游）
      handOf(["♠A/0"]), // 2 号座（要贡）
      handOf(["♠4/0"]), // 3 号座（上游的搭档）
      handOf(["♥A/0"]), // 4 号座（要贡）
    ];
    const order = [1, 3, 2, 4]; // 搭挡第二个出完 → 双下

    // 一样大 → 各贡给自己顺时针那家（座位 1 → 2 → 3 → 4 是逆时针，所以顺时针是 s−1）：
    // 2 号座 → 1 号座（上游）、4 号座 → 3 号座（搭档）。由贡给上游的那位（2 号座）领出。
    const tr = new Tribute(TWO, hands, prevFromOrder(order));
    const keysOf = (seat: number) => tributeHands(tr)[seat - 1]!.map(cardKey).sort();

    expect(tr.kind(), "搭档第二个出完 → 双下 → 双贡").toBe("double");
    expect(tr.givers(), "要贡的是对方两家（2、4 号座）").toStrictEqual([2, 4]);

    tr.give(2, hands[1]![0]!); // ♠A/0
    tr.give(4, hands[3]![0]!); // ♥A/0
    expect(tr.receiver(2), "一样大：2 号座顺时针贡给 1 号座（上游）").toBe(1);
    expect(tr.receiver(4), "一样大：4 号座顺时针贡给 3 号座（搭档）").toBe(3);

    tr.returnCard(1, returnable(tributeHands(tr)[0]!, TWO));
    tr.returnCard(3, returnable(tributeHands(tr)[2]!, TWO));
    expect(tr.done()).toBe(true);
    expect(keysOf(1), "上游：进来 ♠A/0、出去 ♣5/0").toStrictEqual(["♠A/0", "♠K/0"]);
    expect(keysOf(3), "搭档：进来 ♥A/0、出去 ♠4/0").toStrictEqual(["♥A/0"]);
    expect(keysOf(2), "2 号座：贡出 ♠A/0、收回 ♣5/0").toStrictEqual(["♣5/0"]);
    expect(keysOf(4), "4 号座：贡出 ♥A/0、收回 ♠4/0").toStrictEqual(["♠4/0"]);
    expect(tr.start().turn(), "贡给上游的那位（2 号座）领出").toBe(2);
  });
});




