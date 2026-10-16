// 进贡、还贡、抗贡（#87 第 6 步）：一副牌开打前，上副牌的下游把手里最大的一张贡给上游，
// 上游还一张回去。
//
// 口径（2022《竞技掼蛋竞赛规则（试行）》第十条）：
//
//   - 二、单贡：从第二副牌开始、出牌前，上副牌的下游把全手牌里最大的一张（红心级牌除外）贡给
//     上游；上游选一张牌点不超过 10 的牌还给下游，全手牌都大于 10 时还最小的。首圈由下游领出。
//   - 三、双贡：上副牌双下时，双下方两人都向对方进贡。上游拿牌点大的那张，搭档拿小的，各自还给
//     贡给自己的人。两张一样大时按顺时针方向进贡。还牌后由贡给上游的那位领出首圈。
//   - 四、抗贡：下游（双下时是双下方两人合起来）在进贡前抓到两个大王，就抗贡，不再贡牌，
//     首圈由上游领出。
//
// 三处条文有歧义的地方，这里的读法（和 server/internal/rules/tribute.go 一致）：
//
//   - 「牌点不超过 10」按**牌面数字**算（**读法 A**）：2–10 都能还，打 7 时的 ♠7、♥7 牌面是 7，
//     也能还。⚠️ **仓库所有者还没拍板**（另有读法 B：级牌排在 A 之上，不算「不超过 10」）。
//     判定集中在 `canReturn` 一处、牌例里单列了两条（`testdata/rules/tribute.json` 的 `pending`），
//     改读法只动这两处。「全都大于 10 时还最小的」两种读法都按当前级数下的大小（第四条 一、1）。
//   - 「按顺时针方向进贡」：两张贡牌一样大时，每个进贡的人贡给自己**顺时针**方向的那一家。座位
//     1 → 2 → 3 → 4 是逆时针，所以 s 号座顺时针的邻座是 s−1（1 的是 4）；双下方两家的顺时针
//     邻座正好是对方两家，不会撞。
//   - 「最大的一张」按当前级数下的大小；有几张一样大（两张 ♠A、不同花色的级牌）时任选一张都对。
//
// **引擎不替人挑牌**：要贡哪张、还哪张由玩家给，这里只校验合不合规（和出牌一样）。
//
// 打几、过 A、下一副要不要进贡（Go 那边的 `Next`）**不在这个文件里，但在客户端做**：
// 那是计分的事，住在 `./scoring`（第 6 步从 `app/minigame/src/lib/scoring.ts` 搬过来的），
// 两端由 `testdata/scoring/vectors.json` 那份共用用例锁住。
//
// ⚠️ 这里原先写的是「不在客户端做、服务端算好下发」—— **那是错的**：#87 的范围里明写了升级和
// 过 A 的规则、目标里写了「完整判定……胜负」。把「服务端主持练习局」错读成了「客户端不用算结算」。
// 「下一副要不要进贡」那层包装还没加（`ScoreState` 里已经够了：`playing_level` / `match_rounds` /
// `match_over`），是下一步。
//
// 「两端共用牌例」这条口径仍然成立：`tribute.json` **不含**升级（升级有自己的共用用例，见上）。
import { ACE, BIG_JOKER, TEN, TWO, isJoker, type Card, type Rank } from "./card";
import { SEATS } from "./deal";
import { Game, nextSeat, partnerSeat, type GameResult } from "./play";
import { cardPower, isWild } from "./power";

/** 顺时针的下一家（`nextSeat` 的反方向）。 */
export function clockwiseSeat(seat: number): number {
  return ((seat + 2) % SEATS) + 1;
}

/**
 * 进贡 / 还牌不合规则的原因，封闭取值（两端共用，牌例里也写这些）。
 *
 * 和 `play.ts` 的 `REJECT_REASONS` **分开各写一套**：两边的取值一个也不重合，合成一个联合类型
 * 只会让「这一手出牌」和「这一手贡牌」的取值域都变模糊。判别方式一样 —— 只看取值，不比句子。
 */
export const TRIBUTE_REJECT_REASONS = [
  "resisted", // 这一副抗贡了，不贡也不还（第十条 四）
  "not-a-giver", // 不用进贡的人来贡
  "already-gave", // 同一家贡了两次
  "not-biggest", // 贡的不是手里最大的一张（红心级牌除外）（第十条 二）
  "wild-cannot-give", // 贡红心级牌（第十条 二）
  "card-not-in-hand", // 贡 / 还手里没有的牌
  "not-all-given", // 还有人没贡完就还牌
  "not-a-receiver", // 没收贡的人来还
  "already-returned", // 同一家还了两次
  "cannot-return", // 还的牌不合「不超过 10 / 全都大于 10 还最小」（第十条 二）
] as const;

export type TributeRejectReason = (typeof TRIBUTE_REJECT_REASONS)[number];

/** 贡牌不合规则：`reason` 是封闭取值，`message` 是给人看的。牌例只比 `reason`，不比句子。 */
export class TributeError extends Error {
  constructor(
    readonly reason: TributeRejectReason,
    message: string,
  ) {
    super(message);
    this.name = "TributeError";
  }
}

/** 这一副开打前的贡牌情形：单贡、双贡、抗贡。 */
export type TributeKind = "single" | "double" | "resisted";

function holds(hand: readonly Card[], card: Card): boolean {
  return hand.some((c) => c.id === card.id);
}

function without(hand: readonly Card[], card: Card): Card[] {
  return hand.filter((c) => c.id !== card.id);
}

/**
 * 一副牌开打前的贡牌、还牌过程。
 *
 * 每一场（轮）的**第一副牌不进贡**（第十条 二「从第二副牌开始」），调用方不要为它建 `Tribute`。
 * 抗贡在这里直接判定，建出来的对象就已经结束了。
 */
export class Tribute {
  private readonly level: Rank;
  /** ⚠️ `hands[seat - 1]` 是 `seat` 号座的牌（和 `Game` 一样，别写成 `hands[seat]`）。 */
  private readonly handCards: Card[][];
  private readonly kindValue: TributeKind;
  private readonly giverSeats: number[];
  private readonly head: number;

  /** 进贡的人 → 贡出的牌。 */
  private readonly gave = new Map<number, Card>();
  /** 进贡的人 → 贡给谁（所有人都贡完之后才定得下来）。 */
  private readonly to = new Map<number, number>();
  /** 收贡的人 → 还出的牌。 */
  private readonly returned = new Map<number, Card>();
  /** 首圈领出的人（定下来之后）。 */
  private leaderSeat = 0;

  constructor(level: Rank, hands: readonly Card[][], prev: GameResult) {
    if (level < TWO || level > ACE) throw new Error(`贡牌：级数 ${level} 超出 2–A`);
    if (
      !Number.isInteger(prev.headSeat) ||
      prev.headSeat < 1 ||
      prev.headSeat > SEATS ||
      prev.partnerPlace < 2 ||
      prev.partnerPlace > 4 ||
      prev.order.length !== SEATS
    ) {
      throw new Error(`贡牌：上副牌的结果不完整：${JSON.stringify(prev)}`);
    }
    this.level = level;
    this.head = prev.headSeat;
    this.handCards = hands.map((h) => h.slice());

    let kind: TributeKind;
    let givers: number[];
    if (prev.partnerPlace === 2) {
      // 双下：对方两家都贡（第十条 三）。givers 按座位号排好，后面「一样大按顺时针」要用。
      const a = nextSeat(prev.headSeat);
      const b = partnerSeat(nextSeat(prev.headSeat));
      kind = "double";
      givers = a < b ? [a, b] : [b, a];
    } else {
      // 单贡：下游贡（第十条 二）。
      kind = "single";
      givers = [prev.order[SEATS - 1]];
    }

    // 抗贡：要进贡的一方（双下时两人合起来）抓到两个大王（第十条 四）。两副牌一共就两个大王，
    // 所以「合计两个」就等于「都在这边」。
    let bigJokers = 0;
    for (const g of givers) {
      for (const c of this.handCards[g - 1]) {
        if (c.rank === BIG_JOKER) bigJokers++;
      }
    }

    this.kindValue = kind;
    this.giverSeats = givers;
    if (bigJokers >= 2) {
      this.kindValue = "resisted";
      this.leaderSeat = prev.headSeat;
    }
  }

  /** 贡牌的情形。 */
  kind(): TributeKind {
    return this.kindValue;
  }

  /** 要进贡的人（抗贡时也照样给出，界面要显示是谁抗的贡）。 */
  givers(): number[] {
    return this.giverSeats.slice();
  }

  /** 进贡的人贡给谁；还没定下来（双贡要等两人都贡完）时是 `undefined`。 */
  receiver(giver: number): number | undefined {
    return this.to.get(giver);
  }

  /** 贡牌、还牌是不是都完了（抗贡一开始就完了）。 */
  done(): boolean {
    return this.kindValue === "resisted" || this.returned.size === this.giverSeats.length;
  }

  /** 首圈领出的人；还没完时是 `undefined`。 */
  leader(): number | undefined {
    if (!this.done()) return undefined;
    return this.leaderSeat;
  }

  /** 贡完、还完之后各家的手牌（下标是座位号减 1），可以直接交给 `Game`。还没完时抛错。 */
  hands(): Card[][] {
    if (!this.done()) throw new Error("贡牌还没完");
    return this.handCards.map((h) => h.slice());
  }

  /** 贡完之后开打：用贡牌后的手牌和定下来的领出人开一副 `Game`。 */
  start(): Game {
    const hands = this.hands();
    return new Game(this.level, hands, this.leaderSeat);
  }

  private isGiver(seat: number): boolean {
    return this.giverSeats.includes(seat);
  }

  /** 进贡：`seat` 把 `card` 贡出去。`card` 必须是他手里最大的一张（红心级牌除外）。 */
  give(seat: number, card: Card): void {
    if (this.kindValue === "resisted") {
      throw new TributeError("resisted", "进贡：这一副抗贡了，不贡牌（第十条 四）");
    }
    if (!this.isGiver(seat)) {
      throw new TributeError("not-a-giver", `进贡：${seat} 号座不用进贡（要进贡的是 ${this.giverSeats}）`);
    }
    if (this.gave.has(seat)) {
      throw new TributeError("already-gave", `进贡：${seat} 号座已经贡过了`);
    }
    const hand = this.handCards[seat - 1];
    if (!holds(hand, card)) {
      throw new TributeError("card-not-in-hand", `进贡：${seat} 号座手里没有这张牌（ID ${card.id}）`);
    }
    if (isWild(card, this.level)) {
      throw new TributeError("wild-cannot-give", `进贡：红心级牌不用贡（第十条 二）`);
    }
    let max = -1;
    for (const c of hand) {
      if (!isWild(c, this.level) && cardPower(c, this.level) > max) max = cardPower(c, this.level);
    }
    if (cardPower(card, this.level) !== max) {
      throw new TributeError("not-biggest", `进贡：要贡手里最大的一张（红心级牌除外），这张不是（第十条 二）`);
    }

    this.handCards[seat - 1] = without(hand, card);
    this.gave.set(seat, card);
    if (this.gave.size === this.giverSeats.length) this.assign();
  }

  /** 在所有人都贡完之后定下「谁贡给谁」，把贡牌放进收贡的人手里，并定下首圈领出的人。 */
  private assign(): void {
    if (this.kindValue === "single") {
      const g = this.giverSeats[0];
      this.to.set(g, this.head);
      this.leaderSeat = g; // 首圈由下游领出（第十条 二）
    } else {
      const [a, b] = this.giverSeats;
      const pa = cardPower(this.gave.get(a)!, this.level);
      const pb = cardPower(this.gave.get(b)!, this.level);
      if (pa > pb) {
        this.to.set(a, this.head); // 大的给上游、小的给搭档（第十条 三）
        this.to.set(b, partnerSeat(this.head));
      } else if (pa < pb) {
        this.to.set(b, this.head);
        this.to.set(a, partnerSeat(this.head));
      } else {
        this.to.set(a, clockwiseSeat(a)); // 一样大：按顺时针方向进贡
        this.to.set(b, clockwiseSeat(b));
      }
      for (const [g, r] of this.to) {
        if (r === this.head) this.leaderSeat = g; // 还牌后由贡给上游的那位领出首圈（第十条 三）
      }
    }
    for (const [g, r] of this.to) {
      this.handCards[r - 1].push(this.gave.get(g)!);
    }
  }

  /**
   * 这张牌能不能当还牌：手里有牌面不超过 10 的牌时只能还这种；全都大于 10 时只能还最小的
   * （第十条 二）。
   *
   * ⚠️ 「不超过 10」按**牌面数字**算（读法 A）。仓库所有者拍板若改成读法 B（级牌不算不超过 10），
   * **只改这一行**的 `small` 判定；牌例里受影响的也正好只有那两条带 `pending` 的。
   */
  private canReturn(hand: readonly Card[], card: Card): boolean {
    const small = (c: Card): boolean => !isJoker(c.rank) && c.rank <= TEN;
    if (hand.some(small)) return small(card);
    let min = -1;
    for (const c of hand) {
      const p = cardPower(c, this.level);
      if (min < 0 || p < min) min = p;
    }
    return cardPower(card, this.level) === min;
  }

  /**
   * 还牌：收了贡的 `seat` 把 `card` 还给贡给他的人。
   *
   * 方法名只能叫 `returnCard` —— `return` 是保留字。牌例里的字段名还是 `return`（JSON 的键，没问题）。
   */
  returnCard(seat: number, card: Card): void {
    if (this.kindValue === "resisted") {
      throw new TributeError("resisted", "还牌：这一副抗贡了，不还牌（第十条 四）");
    }
    if (this.gave.size < this.giverSeats.length) {
      throw new TributeError("not-all-given", "还牌：还有人没贡完");
    }
    const giver = [...this.to].find(([, r]) => r === seat)?.[0];
    if (giver === undefined) {
      throw new TributeError("not-a-receiver", `还牌：${seat} 号座没收贡，不用还`);
    }
    if (this.returned.has(seat)) {
      throw new TributeError("already-returned", `还牌：${seat} 号座已经还过了`);
    }
    const hand = this.handCards[seat - 1];
    if (!holds(hand, card)) {
      throw new TributeError("card-not-in-hand", `还牌：${seat} 号座手里没有这张牌（ID ${card.id}）`);
    }
    if (!this.canReturn(hand, card)) {
      throw new TributeError(
        "cannot-return",
        "还牌：这张不能还 —— 有牌面不超过 10 的牌时还那种，全都大于 10 时还最小的（第十条 二）",
      );
    }
    this.handCards[seat - 1] = without(hand, card);
    this.handCards[giver - 1].push(card);
    this.returned.set(seat, card);
  }
}
