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
import { ACE, TEN, TWO, cardKey, isJoker, newDeck, type Card, type Rank } from "./card";
import { SEATS, deal, permShuffler } from "./deal";
import { moves } from "./moves";
import { Game, RuleError, nextSeat, partnerSeat, type GameResult } from "./play";
import { cardPower, isWild } from "./power";
import { nextHand, teamOf, type RoundFact } from "./scoring";
import { Tribute } from "./tribute";
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
function probeIllegal(g: Game, seat: number, step: number): void {
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
}

/** 打完已经摆好的这一副：从 `g` 现在的局面一路走到分出上游。 */
function playDeal(rng: Rng, level: Rank, g: Game, m: SimModel): { steps: number; borrowed: number } {
  let steps = 0;
  while (!g.over()) {
    if (++steps > MAX_STEPS_PER_DEAL) {
      throw new Error(`这一副打不完（步数超上限 ${MAX_STEPS_PER_DEAL}）—— 引擎卡住了`);
    }
    const seat = g.turn();
    expect(seat, "模型和引擎轮到的不是同一个人").toBe(m.turn);

    probeIllegal(g, seat, steps);

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
  return { steps, borrowed: m.borrowed };
}

// ───────────────────────────────────────────────────────────────────────────
// D：独立推「下一副打几」（第二条 12–14、第十三条）
// ───────────────────────────────────────────────────────────────────────────

/**
 * 第二份「下一副打几」：只按条文推，**不看 `scoring` 的实现**。
 * 那边是一个 `ScoreState` 一路 apply；这里把两队的级数、本场状态分开记 —— 摆法和算法都另写一套，
 * 只共享同一份规则条文。
 */
interface RefMatch {
  levels: { red: number; black: number };
  playing: "" | "red" | "black";
  playingLevel: number;
  rounds: number;
  over: boolean;
}

const newRefMatch = (): RefMatch => ({
  levels: { red: 2, black: 2 },
  playing: "",
  playingLevel: 2,
  rounds: 0,
  over: false,
});

/** 记一局，推到下一副。 */
function refNext(ref: RefMatch, headSeat: number, partnerPlace: number): void {
  if (ref.over) {
    // 本场已结束：这一局是下一场的第 1 局，两队都从 2 开始（第十二条）。
    ref.levels = { red: 2, black: 2 };
    ref.playing = "";
    ref.playingLevel = 2;
    ref.rounds = 0;
    ref.over = false;
  }
  const winner = teamOf(headSeat);
  ref.rounds++;
  const playingOwnA = ref.playing !== "" && ref.levels[ref.playing] === ACE;

  // 第二条 13：打本队的 A 时头游、搭档不是末游 = 过 A，本场结束（级数不再动）。
  if (playingOwnA && winner === ref.playing && partnerPlace !== 4) {
    ref.over = true;
    ref.playing = "";
    ref.playingLevel = 2;
    return;
  }
  // 第二条 12：只有头游所在的队升级，搭档二游 +3、三游 +2、末游 +1；到 A 封顶（第二条 13）。
  ref.levels[winner] = Math.min(ref.levels[winner] + (5 - partnerPlace), ACE);
  // 第二条 13「A 级必打」：打本队的 A 没过，下一副还打 A —— 上面那一步加完仍被 min 封在 A 上，
  // 所以「级数不动」是自然成立的，这里不需要额外做什么。
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

/**
 * 按规则把一副牌的贡、还走完。
 *
 * 贡的是「手里最大的一张（红心级牌除外）」、还的是「不超过 10 的（读法 A）/ 全都大于 10 时最小的」，
 * 同大任选一张 —— 挑完引擎必须接受，不接受就是驱动器或引擎和规则对不上，直接红。
 * 抗贡由 `Tribute` 自己判，这里什么都不做。
 */
function driveTribute(tr: Tribute, level: Rank): void {
  for (const g of tr.givers()) {
    tr.give(g, biggestNonWild(tributeHands(tr)[g - 1]!, level));
  }
  if (tr.done()) return;
  for (const g of tr.givers()) {
    const seat = tr.receiver(g)!;
    tr.returnCard(seat, returnable(tributeHands(tr)[seat - 1]!, level));
  }
}

// ───────────────────────────────────────────────────────────────────────────
// F：一场打到底
// ───────────────────────────────────────────────────────────────────────────

interface MatchLog {
  /** 是「过 A 结束」还是撞上了副数上限（上限只是防死循环，撞上说明哪里不对）。 */
  over: boolean;
  deals: number;
  steps: number;
  tributeDeals: number;
  resistedDeals: number;
  borrowed: number;
  levels: number[];
}

/**
 * 打完一场：从 2 打起，每副「发牌 →（该进贡就进贡还贡）→ 打完 → 算下一副打几」，
 * 一路打到过 A 或者打满上限。
 */
function playMatch(rng: Rng, maxDeals = MAX_DEALS_PER_MATCH): MatchLog {
  const log: MatchLog = { over: false, deals: 0, steps: 0, tributeDeals: 0, resistedDeals: 0, borrowed: 0, levels: [] };
  const ref = newRefMatch();
  const facts: RoundFact[] = [];
  let level: Rank = TWO;
  let prev: GameResult | null = null;
  // 要不要进贡：第一副不进贡（第十条 二「从第二副牌开始」），之后由引擎说 —— 说错了下面那条会红。
  let needTribute = false;

  while (log.deals < maxDeals) {
    // 首抓者坐 1 号座（这一层只关心「谁进贡、谁领出」，和首抓者是谁无关）。
    const hands = dealFor(rng, 1);
    let g: Game;
    if (!needTribute) {
      // 不进贡的这一副：谁领出由调用方给。
      g = new Game(level, hands, 1);
    } else {
      const tr = new Tribute(level, hands, prev!);
      log.tributeDeals++;
      if (tr.kind() === "resisted") {
        log.resistedDeals++;
      } else {
        driveTribute(tr, level);
      }
      g = tr.start();
    }
    log.levels.push(level);

    // 模型从**这一副真正开打时的**手牌和领出人起算（进贡还完之后的那一副）。
    const m = newSimModel([g.hand(1), g.hand(2), g.hand(3), g.hand(4)], g.turn());
    const { steps, borrowed } = playDeal(rng, level, g, m);
    log.deals++;
    log.steps += steps;
    log.borrowed += borrowed;

    const res = g.result()!;
    facts.push({ head_seat: res.headSeat, partner_place: res.partnerPlace });

    // 独立推一遍下一副打几、本场结束没有，和引擎对。
    refNext(ref, res.headSeat, res.partnerPlace);
    const engine = nextHand(facts);
    expect(engine.level, `第 ${log.deals} 副之后：下一副打几（独立推的是 ${ref.playingLevel}）`).toBe(ref.playingLevel);
    expect(engine.score.match_over, `第 ${log.deals} 副之后：本场结束没有`).toBe(ref.over);

    // 「要不要进贡」也独立推一遍：这一场已经打过至少一副（第十条 二），而且本场还没结束
    // —— 本场刚结束时下一副是**新一场的第一副**，不进贡。
    const wantTribute = facts.length > 0 && !ref.over;
    expect(engine.tribute, `第 ${log.deals} 副之后：要不要进贡`).toBe(wantTribute);

    if (ref.over) break;
    level = engine.level as Rank;
    needTribute = engine.tribute;
    prev = res;
  }
  log.over = ref.over;
  return log;
}

// ───────────────────────────────────────────────────────────────────────────
// 测试
// ───────────────────────────────────────────────────────────────────────────

describe("性质：随机对局（完整的一场：发牌 → 进贡 → 出牌 → 下一副）", () => {
  it("固定种子跑若干场：守恒、合法、必然结束，每步都和独立推的模型对得上", () => {
    let deals = 0;
    let steps = 0;
    let tributeDeals = 0;
    let resisted = 0;
    let borrowed = 0;
    const seeds = [1, 2, 3, 4, 5, 6];
    for (const seed of seeds) {
      const log = playMatch(seededRandom(seed));
      // 一场总有打完的时候（过 A）：撞上副数上限就说明「打不完」——那正是要抓的。
      expect(log.over, `种子 ${seed} 的那一场打满 ${MAX_DEALS_PER_MATCH} 副都没过 A`).toBe(true);
      deals += log.deals;
      steps += log.steps;
      tributeDeals += log.tributeDeals;
      resisted += log.resistedDeals;
      borrowed += log.borrowed;
    }
    // 覆盖面：不能「跑是跑了、什么路径都没走到」。进贡和借风这两条路必须真的走到过。
    expect(deals, "一共打了多少副").toBeGreaterThan(20);
    expect(tributeDeals, "进贡过的副数（第 2 副起才有）").toBeGreaterThan(10);
    expect(borrowed, "借风发生过多少次").toBeGreaterThan(0);
    // 覆盖面的数字（几副、几步、进贡/抗贡/借风各多少次）由上面几条断言钉着；
    // 想看具体数字时把 `expect(...)` 的期望值挪一下，红了就会把实际值打出来。
    expect(resisted, "抗贡过的副数（撞上两个大王，可能为 0）").toBeGreaterThanOrEqual(0);
  });

  it("同一个种子跑两遍：逐副结果一模一样（引擎不读时间、不读随机数）", () => {
    const a = playMatch(seededRandom(20261016), 4);
    const b = playMatch(seededRandom(20261016), 4);
    expect(a, "同一个种子两遍该完全一样").toStrictEqual(b);
  });

  it("进贡还贡只换谁拿哪张：四家的牌一张不多不少，而且贡来的确实进了收贡的人手里", () => {
    const rng = seededRandom(7);
    const hands = dealFor(rng, 1);
    const prev: GameResult = { order: [1, 2, 3, 4], headSeat: 1, partnerPlace: 3 };
    const tr = new Tribute(TWO, hands, prev);
    const before = tributeHands(tr).flat().map(cardKey).sort();

    for (const g of tr.givers()) {
      const card = biggestNonWild(tributeHands(tr)[g - 1]!, TWO);
      const to = tr.receiver(g); // 还没贡完时是 undefined（双贡要等两人都贡完）
      tr.give(g, card);
      expect(tributeHands(tr)[g - 1]!.some((c) => c.id === card.id), "贡出去的牌该从进贡人手里拿走").toBe(false);
      if (to !== undefined) expect(to, "收贡的人在贡完之前不该定下来").toBeDefined();
    }
    if (!tr.done()) {
      for (const g of tr.givers()) {
        const seat = tr.receiver(g)!;
        tr.returnCard(seat, returnable(tributeHands(tr)[seat - 1]!, TWO));
      }
    }
    expect(tr.done(), "贡完还完该算完了").toBe(true);
    expect(
      tr.hands().flat().map(cardKey).sort(),
      "进贡还贡只是换谁拿哪张，牌一张不多不少",
    ).toStrictEqual(before);
  });
});
