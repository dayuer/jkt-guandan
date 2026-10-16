// 牌型识别和比较（#87 第 2 步）。规则口径按 2022《竞技掼蛋竞赛规则（试行）》，条款号写在旁边；
// 对照用例见仓库根 testdata/rules/combos.json 和 compare.json（格式见那里的 README.md）。
//
// 和服务端的 server/internal/rules/combo.go 是同一套语义，两边对着改。
//
// 这一步**不含逢人配**（红桃级牌顶替别的牌，第 3 步）：这里只认实打实的牌。
import { ACE, BIG_JOKER, cardKey, isJoker, rankName, SMALL_JOKER, TWO, type Card, type Rank } from "./card";
import { power } from "./power";

/**
 * 牌型（第三条 1–10）。**这个数组的顺序就是解读的排序，也是比大小的档次**：
 * 普通牌型只在同牌型之间比，炸弹、同花顺、四大天王压普通牌型，炸弹之间按 `bombTier`。
 *
 * 取值和 testdata/rules README 里那张表、以及 Go 端 `ComboType` 的 `String()` 一一对应。
 */
export const COMBO_TYPES = [
  "single", // 单张（第三条 1）
  "pair", // 对子（第三条 2）
  "triple", // 三同张（第三条 4）
  "triple-pair", // 三带对（第三条 6）
  "tube", // 三连对，俗称木板（第三条 3）
  "plate", // 三同连张，俗称钢板（第三条 5）
  "straight", // 顺子，正好 5 张（第三条 7）
  "straight-flush", // 同花顺（第三条 9）
  "bomb", // 炸弹：四到十张同点数（第三条 8）
  "joker-bomb", // 四大天王：大小王各两张（第三条 10）
] as const;

export type ComboType = (typeof COMBO_TYPES)[number];

const LABELS: Record<ComboType, string> = {
  single: "单张",
  pair: "对子",
  triple: "三同张",
  "triple-pair": "三带对",
  tube: "三连对",
  plate: "三同连张",
  straight: "顺子",
  "straight-flush": "同花顺",
  bomb: "炸弹",
  "joker-bomb": "四大天王",
};

/** 牌型的中文名，报错和测试消息里用。 */
export function comboLabel(type: ComboType): string {
  return LABELS[type];
}

/** 认牌型的名字（牌例里写的那些）。 */
export function parseComboType(name: string): ComboType {
  for (const type of COMBO_TYPES) {
    if (type === name) return type;
  }
  throw new Error(`认不出牌型 ${JSON.stringify(name)}`);
}

/** 排序和档次都按 COMBO_TYPES 的下标。 */
function typeOrder(type: ComboType): number {
  return COMBO_TYPES.indexOf(type);
}

/**
 * 一手牌的一种解读：这手牌能当成什么牌型打出去。
 *
 * 同一手牌可能有多种解读：同花色的 34567 既能当顺子跟牌，也能当同花顺压炸弹。所以出牌动作
 * 要带上**声明的牌型**，引擎只校验声明成不成立，不替出牌的人挑一种。
 */
export interface Combo {
  type: ComboType;
  /** 这手牌，按牌点从小到大排（王在最后）。 */
  cards: Card[];
  /**
   * 这手牌的牌面：单张、对子、三同张是那个点数；三带对是**三同张的点数**
   * （第四条 二、（一）2：不比所带的对子）；顺子类是顶张（A2345 的顶张是 5）；
   * 炸弹是点数；**四大天王没有点数**（0，写法见 comboRankName）。
   */
  rank: Rank;
  /**
   * 比大小用的数值，只在同一牌型之间比（第四条 二、（一））。两种算法，差别在级牌上：
   *
   *   - 单张、对子、三同张、三带对、炸弹：按牌点在当前级数下的大小（第四条 一），
   *     所以打 7 的时候，单张 7 压单张 A；
   *   - 顺子类（三连对、三同连张、顺子、同花顺）：按牌面原位置算顶张（第五条 1），
   *     打 7 的时候 34567 的顶张还是 7，不会变成最大的一张。
   */
  value: number;
}

/** 牌面写成牌例里的写法：四大天王是空串，其余是牌面。 */
export function comboRankName(combo: Combo): string {
  return combo.type === "joker-bomb" ? "" : rankName(combo.rank);
}

/** 炸弹、同花顺、四大天王都能压普通牌型（第四条 二、（二））。 */
export function isBomb(combo: Combo): boolean {
  return combo.type === "bomb" || combo.type === "straight-flush" || combo.type === "joker-bomb";
}

/**
 * 炸弹之间的档次，越大越强（第四条 二、（二）：四大天王 ＞ 超过五张的炸弹 ＞ 同花顺
 * ＞ 五张的炸弹 ＞ 四张的炸弹）。非炸弹是 0。
 */
export function bombTier(combo: Combo): number {
  if (combo.type === "joker-bomb") return 5;
  if (combo.type === "bomb" && combo.cards.length > 5) return 4;
  if (combo.type === "straight-flush") return 3;
  if (combo.type === "bomb" && combo.cards.length === 5) return 2;
  if (combo.type === "bomb") return 1;
  return 0;
}

/** 两手牌比出来的关系（第四条 二）。两手的解读要在**同一个级数**下认出来才可比。 */
export type Order =
  /** 牌型不同、又不是炸弹压普通牌型，压根没得比。 */
  | "incomparable"
  /** a 比 b 小。 */
  | "less"
  /** 一样大（同牌型同张数同 value；四大天王碰上四大天王也只有这一种）。 */
  | "equal"
  /** a 压得住 b。 */
  | "greater";

/**
 * 比出 a 和 b 的关系（第四条 二）：
 *
 *   - 都不是炸弹：牌型和张数都得一样才谈得上比，一样了再比 value（第四条 二、（一）：
 *     三带对比三同张的点数，其余比牌点）；牌型或张数不同就是 `incomparable`；
 *   - 炸弹压任何普通牌型；普通牌型压不了炸弹；
 *   - 都是炸弹：先比档次，同档再比 —— 超过五张的比张数、张数相同比 value，同花顺比顶张。
 */
export function compare(a: Combo, b: Combo): Order {
  if (!isBomb(a) && !isBomb(b)) {
    if (a.type !== b.type || a.cards.length !== b.cards.length) return "incomparable";
    return orderOf(a.value, b.value);
  }
  if (!isBomb(a)) return "less";
  if (!isBomb(b)) return "greater";

  const [ta, tb] = [bombTier(a), bombTier(b)];
  if (ta !== tb) return orderOf(ta, tb);
  if (a.type !== b.type) return "incomparable"; // 同档次必定同牌型，这里是防守
  if (a.type === "joker-bomb") return "equal"; // 四大天王只有一副，碰上了就是一样大
  if (a.cards.length !== b.cards.length) return orderOf(a.cards.length, b.cards.length); // 张数多者大（第四条 二、（一）3）
  return orderOf(a.value, b.value);
}

/** `compare` 的简写：能不能压住。跟牌判断只问这一句。 */
export function beats(a: Combo, b: Combo): boolean {
  return compare(a, b) === "greater";
}

function orderOf(a: number, b: number): Order {
  if (a > b) return "greater";
  if (a < b) return "less";
  return "equal";
}

/** 顺子类的牌型按牌面原位置比大小，不按级数（第五条 1）。 */
function isSeqType(type: ComboType): boolean {
  return type === "tube" || type === "plate" || type === "straight" || type === "straight-flush";
}

/**
 * 列出这手牌**全部**合法的解读。`level` 是当前级数（打几）。
 *
 * 一手牌可能有好几种打法，比如同花色的 34567 既是顺子又是同花顺；返回的顺序固定
 * （先按牌型，再按牌点），两端比对时不会因为顺序不同而不一致。
 *
 * 这手牌什么牌型都不是时返回空数组，不是错误 —— 调用方按「这手牌打不出去」处理。
 * 空手牌、或者同一张牌写了两次是调用方的错，抛错。
 */
export function recognize(cards: readonly Card[], level: Rank): Combo[] {
  if (cards.length === 0) throw new Error("认牌型：没有牌");
  if (level < TWO || level > ACE) throw new Error(`认牌型：级数 ${rankName(level)} 超出 2–A`);
  checkDistinctCards(cards);

  const sorted = sortCards(cards);
  const counts = rankCounts(sorted); // 牌点 → 张数
  const distinct = [...counts.keys()].sort((a, b) => a - b);

  const out: Combo[] = [];
  const add = (type: ComboType, rank: Rank): void => {
    out.push({ type, cards: sorted, rank, value: isSeqType(type) ? rank : power(rank, level) });
  };

  // 同点数的：单张、对子、三同张、炸弹（第三条 1、2、4、8）。
  if (distinct.length === 1) {
    const n = sorted.length;
    if (n === 1) add("single", distinct[0]);
    else if (n === 2) add("pair", distinct[0]);
    else if (n === 3) add("triple", distinct[0]);
    else if (n >= 4 && n <= 10) add("bomb", distinct[0]);
  }

  // 四大天王：大小王各两张（第三条 10）。它不是同点数的炸弹，单独认；没有点数可标。
  if (sorted.length === 4 && counts.get(BIG_JOKER) === 2 && counts.get(SMALL_JOKER) === 2) {
    add("joker-bomb", 0);
  }

  // 三带对：一个三同张带一个对子（第三条 6）。
  if (sorted.length === 5 && distinct.length === 2) {
    for (const rank of distinct) {
      if (counts.get(rank) === 3) add("triple-pair", rank);
    }
  }

  // 三连对：三个牌点相邻的对子（第三条 3）。
  if (sorted.length === 6 && distinct.length === 3 && allCounts(counts, 2)) {
    const top = seqTop(distinct, 3);
    if (top !== undefined) add("tube", top);
  }

  // 三同连张：两个牌点相邻的三同张（第三条 5）。
  if (sorted.length === 6 && distinct.length === 2 && allCounts(counts, 3)) {
    const top = seqTop(distinct, 2);
    if (top !== undefined) add("plate", top);
  }

  // 顺子：五个牌点相邻的单张，花色不限（第三条 7）。王不参与。
  if (sorted.length === 5 && distinct.length === 5 && allCounts(counts, 1)) {
    const top = seqTop(distinct, 5);
    if (top !== undefined) {
      add("straight", top);
      // 同一个顺子还是同一个花色时，它也是同花顺（第三条 9）：两种解读都给出来，
      // 出牌的人自己声明打哪一种 —— 当顺子能跟牌，当同花顺能压炸弹。
      if (sameSuit(sorted)) add("straight-flush", top);
    }
  }

  out.sort((x, y) => (x.type !== y.type ? typeOrder(x.type) - typeOrder(y.type) : x.rank - y.rank));
  return out;
}

/** 在这手牌的全部解读里找声明的那一种；没有就是这手牌打不出这个牌型。 */
export function findCombo(cards: readonly Card[], level: Rank, type: ComboType): Combo | undefined {
  return recognize(cards, level).find((combo) => combo.type === type);
}

/** 把一组解读写成给人看的一行（日志、报错、测试消息里用）。 */
export function comboSummary(combos: readonly Combo[]): string {
  if (combos.length === 0) return "打不出去";
  return combos.map((c) => `${comboLabel(c.type)} ${comboRankName(c)}（${c.cards.length} 张）`).join("、");
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

function checkDistinctCards(cards: readonly Card[]): void {
  const seen = new Set<number>();
  for (const card of cards) {
    if (seen.has(card.id)) throw new Error(`认牌型：${cardKey(card)} 出现了两次`);
    seen.add(card.id);
  }
}

/** 按牌点、花色、副号从小到大。 */
function sortCards(cards: readonly Card[]): Card[] {
  return cards.slice().sort((a, b) => a.rank - b.rank || a.suit - b.suit || a.deck - b.deck);
}

function rankCounts(cards: readonly Card[]): Map<Rank, number> {
  const counts = new Map<Rank, number>();
  for (const card of cards) counts.set(card.rank, (counts.get(card.rank) ?? 0) + 1);
  return counts;
}

function allCounts(counts: Map<Rank, number>, want: number): boolean {
  for (const n of counts.values()) {
    if (n !== want) return false;
  }
  return true;
}

function sameSuit(cards: readonly Card[]): boolean {
  return cards.every((card) => card.suit === cards[0].suit);
}

/**
 * 判断这组点数能不能排成 `count` 个连着的牌点，能就返回顶张的牌面。
 *
 * A 在顺子类牌型里可以当最小的 1 用（A2345、AA2233），也可以当最大的 14 用（10JQKA、QQKKAA），
 * 但**不能两头都接**（第四条 二、（二）2）。王不参与顺子类牌型。
 */
function seqTop(ranks: readonly Rank[], count: number): Rank | undefined {
  if (ranks.length !== count) return undefined;
  if (ranks.some(isJoker)) return undefined;
  for (const aceLow of [false, true]) {
    const hasAce = ranks.includes(ACE);
    if (aceLow && !hasAce) continue; // 没 A 的话两种读法一样，别重复判
    const values = ranks.map((rank) => (aceLow && rank === ACE ? 1 : rank)).sort((a, b) => a - b);
    let ok = true;
    for (let i = 1; i < values.length; i++) {
      if (values[i] !== values[i - 1] + 1) {
        ok = false;
        break;
      }
    }
    if (ok) return values[values.length - 1];
  }
  return undefined;
}

/** `parseComboType` 的数组版：认一组牌型名（牌例里用）。 */
export function parseComboTypes(names: readonly string[]): ComboType[] {
  return names.map(parseComboType);
}
