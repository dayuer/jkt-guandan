// 出牌状态机（#87 第 5 步）：一副牌从首圈领出到产生游数的全过程。
//
// 口径（2022《竞技掼蛋竞赛规则（试行）》）：
//
//   - 第八条 一、1：逆时针出牌。领出牌的人可以出任意牌型的一手牌，其余的人按序出更大的一手牌
//     压上去，或者过牌（第八条 一、2）。
//   - 第八条 一、3、第二条 8：其余在场的人都过牌，这一圈结束，最后出牌的人领出下一圈。
//   - 第九条（借风）：上游或二游打完最后一手牌、余下各家都不压时，由他的搭档领出下一圈。
//   - 第八条 一、4：三人出完，或者同一方拿了上游和二游，这一副结束。
//   - 第二条 10：游数按出完的先后排（上游、二游、三游、下游）。
//
// 首圈谁领出由调用方给（和进贡有关，第十条；不在这一步）。能不能压上家用 `recognize` / `pick` /
// `compare` 判断；出牌要带完整声明（牌型 + 牌面），引擎只校验、不替出牌的人挑。
//
// 座位用 1–4：1 → 2 → 3 → 4 是逆时针，1、3 对家，2、4 对家。
//
// **不读时间、不读随机数**：同样的初始局面和同样的动作序列，一定走到同样的局面（复盘、断线恢复）。
//
// 和服务端的 server/internal/rules/play.go 是同一套语义，两边对着改。
import { ACE, TWO, type Card, type Rank } from "./card";
import { compare, pick, recognize, type Combo, type ComboType } from "./combo";
// 桌子是四家 —— 座位数只定义一处（发牌那边也是同一个常量），别各写各的。
import { SEATS } from "./deal";

/** 逆时针的下一家。 */
export function nextSeat(seat: number): number {
  return (seat % SEATS) + 1;
}

/** 对家。 */
export function partnerSeat(seat: number): number {
  return ((seat + 1) % SEATS) + 1;
}

/**
 * **拒绝的原因**，封闭取值（两端共用，牌例里也写这些）。
 *
 * 为什么不是「能不能出」的布尔值：布尔值分不出「不是你的回合」和「压不住」，而客户端恰恰靠这个
 * 区别显示不同的提示（和 `Order` 分 `incomparable`/`equal` 是同一条理由，#216 复审）。
 *
 * 座位号不是 1–4 这类**调用方的错**不在这里 —— 那是普通 `Error`，不是"这一手不合规则"。
 */
export const REJECT_REASONS = [
  "game-over", // 这一副已经结束了还在动
  "not-your-turn", // 没轮到这一家（第八条 一、1）
  "leading-cannot-pass", // 领出时不能过牌（第八条 一、1）
  "no-cards", // 出牌时一张牌都没给
  "cards-not-in-hand", // 打了手里没有的牌
  "bad-combo", // 声明的那一种解读不成立
  "does-not-beat", // 压不住上一手（第八条 一、1）
] as const;

export type RejectReason = (typeof REJECT_REASONS)[number];

/** 不合规则的一手：`reason` 是封闭取值，`message` 是给人看的。牌例只比 `reason`，不比句子。 */
export class RuleError extends Error {
  constructor(
    readonly reason: RejectReason,
    message: string,
  ) {
    super(message);
    this.name = "RuleError";
  }
}

/** 一个动作：出一手牌，或者过牌。复盘就是从初始局面把动作重放一遍。 */
export interface Move {
  seat: number;
  pass: boolean;
  /** 出牌时：打出去的牌和声明。`combo` 由引擎按声明认出来，不信调用方给的。 */
  combo?: Combo;
}

/** 一副牌的结果。 */
export interface GameResult {
  /** 出完的先后：`order[0]` 是上游。双下时没出完的两家按座位号排在后面，同为下游。 */
  order: number[];
  /** 上游的座位。 */
  headSeat: number;
  /** 上游的搭档第几个出完：2 二游（对方双下）、3 三游、4 下游。口径同 `scoring.Round`。 */
  partnerPlace: number;
}

function validSeat(seat: number): boolean {
  return Number.isInteger(seat) && seat >= 1 && seat <= SEATS;
}

/**
 * 一副牌打到一半的局面。
 *
 * 牌一律按 ID 认（两副牌里两张 ♠7 是两张不同的牌）；`hands` 用座位号索引（下标 1–4）。
 */
export class Game {
  private readonly level: Rank;
  /** `hands[seat - 1]` 是 `seat` 号座的牌。⚠️ 别写成 `hands[seat]` —— 座位号是 1–4、下标是 0–3，
   * 第一版就栽在这儿（测试报「手里没有这张牌」，其实是查了隔壁那家的手牌）。 */
  private readonly hands: Card[][];
  private turnSeat: number;
  /** 这一圈的情况。`lastSeat === 0` 表示现在是领出（这一圈还没人出牌）。 */
  private lastSeat = 0;
  private lastCombo: Combo | undefined;
  private passes = 0;
  private finishedSeats: number[] = [];
  private moveLog: Move[] = [];

  /** 开一副牌：`hands[i]` 是 `i+1` 号座的手牌，`leader` 是首圈领出的座位。 */
  constructor(level: Rank, hands: readonly Card[][], leader: number) {
    if (level < TWO || level > ACE) throw new Error(`开局：级数 ${level} 超出 2–A`);
    if (!validSeat(leader)) throw new Error(`开局：领出的座位 ${leader} 不是 1–4`);
    if (hands.length !== SEATS) throw new Error(`开局：要 ${SEATS} 家的手牌，给了 ${hands.length} 家`);

    const seen = new Map<number, number>();
    this.hands = hands.map((hand, i) => {
      const seat = i + 1;
      if (hand.length === 0) throw new Error(`开局：${seat} 号座没有牌`);
      for (const card of hand) {
        const other = seen.get(card.id);
        if (other !== undefined) throw new Error(`开局：${card.id} 同时在 ${other} 号座和 ${seat} 号座手里`);
        seen.set(card.id, seat);
      }
      return hand.slice();
    });
    this.level = level;
    this.turnSeat = leader;
  }

  /** 这一副打几。 */
  get playingLevel(): Rank {
    return this.level;
  }

  /** 轮到谁（1–4）；这一副结束后是 0。 */
  turn(): number {
    return this.turnSeat;
  }

  /** 这一副是不是结束了。 */
  over(): boolean {
    return this.turnSeat === 0;
  }

  /** 现在是不是领出（这一圈还没人出牌）：领出时不能过牌，什么牌型都能出。 */
  leading(): boolean {
    return this.lastSeat === 0;
  }

  /** 这个座位手里还剩的牌（复制一份）。 */
  hand(seat: number): Card[] {
    return validSeat(seat) ? this.hands[seat - 1].slice() : [];
  }

  /** 这一圈最近的一手牌（被压的那一手）；领出时不给。 */
  lastPlay(): { seat: number; combo: Combo } | undefined {
    return this.lastSeat === 0 || !this.lastCombo ? undefined : { seat: this.lastSeat, combo: this.lastCombo };
  }

  /** 已经出完的座位，按先后。 */
  finished(): number[] {
    return this.finishedSeats.slice();
  }

  /** 到现在为止的全部动作（复盘用）。 */
  moves(): Move[] {
    return this.moveLog.slice();
  }

  /**
   * 出一手牌：`seat` 出 `cards`，声明成牌型 `kind`、牌面 `rank`（`rank` 不给表示「这个牌型只有
   * 一种解读」）。成功时局面往前走；不合法时抛 `RuleError`，**局面不变**。
   */
  play(seat: number, cards: readonly Card[], kind: ComboType, rank?: Rank): void {
    this.checkTurn(seat);
    if (cards.length === 0) throw new RuleError("no-cards", "出牌：没有牌（不出请过牌）");

    const held = new Set(this.hands[seat - 1].map((card) => card.id));
    for (const card of cards) {
      if (!held.has(card.id)) throw new RuleError("cards-not-in-hand", `出牌：${seat} 号座手里没有这张牌（ID ${card.id}）`);
    }

    const combos = recognize(cards, this.level);
    const combo = pick(combos, kind, rank);
    if (!combo) throw new RuleError("bad-combo", `出牌：这手牌当不成声明的「${kind}」`);

    const last = this.lastPlay();
    if (last && compare(combo, last.combo) !== "greater") {
      throw new RuleError("does-not-beat", `出牌：压不住 ${last.seat} 号座的上一手`);
    }

    // 从手里拿掉。
    const out = new Set(cards.map((card) => card.id));
    this.hands[seat - 1] = this.hands[seat - 1].filter((card) => !out.has(card.id));
    this.moveLog.push({ seat, pass: false, combo });
    this.lastSeat = seat;
    this.lastCombo = combo;
    this.passes = 0;

    if (this.done(seat)) {
      this.finishedSeats.push(seat);
      if (this.gameOver()) {
        this.turnSeat = 0;
        return;
      }
    }
    this.turnSeat = this.nextActive(seat);
  }

  /** 过牌（第八条 一、2）。领出时不能过。 */
  pass(seat: number): void {
    this.checkTurn(seat);
    if (this.lastSeat === 0) {
      throw new RuleError("leading-cannot-pass", `过牌：${seat} 号座是领出，不能过牌（第八条 一、1）`);
    }
    this.moveLog.push({ seat, pass: true });
    this.passes++;

    // 这一圈还有谁能接：除了出牌的那一家，在场的人都过了，这一圈就结束（第八条 一、3）。
    let need = this.activeCount();
    if (!this.done(this.lastSeat)) need--; // 出牌的那一家还在场，不用等他自己过
    if (this.passes < need) {
      this.turnSeat = this.nextActive(seat);
      return;
    }

    // 这一圈结束：最后出牌的人领出；他已经出完了的话，由他的搭档领出（第九条，借风）。
    let leader = this.lastSeat;
    if (this.done(leader)) {
      leader = partnerSeat(leader);
      if (this.done(leader)) {
        // 搭档也出完了：同一方拿了上游、二游，这一副在那时就该结束了，到不了这里。
        throw new Error(`过牌：内部状态不对，${this.lastSeat} 号座和搭档都出完了这一副却没结束`);
      }
    }
    this.lastSeat = 0;
    this.lastCombo = undefined;
    this.passes = 0;
    this.turnSeat = leader;
  }

  /** 这一副的结果；还没结束时不给。 */
  result(): GameResult | undefined {
    if (!this.over()) return undefined;
    const order = this.finishedSeats.slice();
    const rest: number[] = [];
    for (let seat = 1; seat <= SEATS; seat++) {
      if (!this.done(seat)) rest.push(seat);
    }
    rest.sort((a, b) => a - b);
    order.push(...rest);

    const headSeat = order[0];
    let partnerPlace = 0;
    order.forEach((seat, i) => {
      if (seat === partnerSeat(headSeat)) partnerPlace = i + 1;
    });
    return { order, headSeat, partnerPlace };
  }

  private checkTurn(seat: number): void {
    if (this.turnSeat === 0) throw new RuleError("game-over", "这一副已经结束");
    if (!validSeat(seat)) throw new Error(`座位 ${seat} 不是 1–4`);
    if (seat !== this.turnSeat) {
      throw new RuleError("not-your-turn", `没轮到 ${seat} 号座，现在轮到 ${this.turnSeat} 号座（第八条 一、1）`);
    }
  }

  private done(seat: number): boolean {
    return this.hands[seat - 1].length === 0;
  }

  private activeCount(): number {
    let n = 0;
    for (let seat = 1; seat <= SEATS; seat++) {
      if (!this.done(seat)) n++;
    }
    return n;
  }

  /** `seat` 之后逆时针第一个还没出完的座位（出完的人跳过）。 */
  private nextActive(seat: number): number {
    for (let s = nextSeat(seat); s !== seat; s = nextSeat(s)) {
      if (!this.done(s)) return s;
    }
    return 0;
  }

  /** 这一副是不是结束了（第八条 一、4）。 */
  private gameOver(): boolean {
    if (this.finishedSeats.length === 2) {
      return partnerSeat(this.finishedSeats[0]) === this.finishedSeats[1]; // 同一方上游、二游，对方双下
    }
    return this.finishedSeats.length === 3;
  }
}

/** `new Game(...)` 的简写，方便测试和调用方。 */
export function newGame(level: Rank, hands: readonly Card[][], leader: number): Game {
  return new Game(level, hands, leader);
}
