// 牌、牌点、花色：两副牌 108 张，每张都有唯一的 ID（#87 第 1 步）。
//
// 口径按 2022《竞技掼蛋竞赛规则（试行）》（国家体育总局棋牌运动管理中心审定，2022-09-08），
// 条款号写在规则旁边。对照用例在仓库根 testdata/rules/，格式见那里的 README.md ——
// 那份文档是**两端的接口说明**，服务端的 Go 版（server/internal/rules）读同一批文件。
//
// 逻辑移植自 mimaima699-ux/Guandan-Poker-Platform 的 packages/core（MIT，见 ../LICENSE）：
// 牌堆的顺序、牌的写法、ID 的含义都按 testdata/rules 的约定重写，没有照抄它原来的 8 位整数编码。
//
// 纯逻辑：不读时间、不读随机数（发牌的随机源从外面注入，见 deal.ts）。

/** 牌点。2–10 就是数字，J=11、Q=12、K=13、A=14；大小王排在 A 之后（15、16）。 */
export type Rank = number;

export const TWO = 2;
export const THREE = 3;
export const FOUR = 4;
export const FIVE = 5;
export const SIX = 6;
export const SEVEN = 7;
export const EIGHT = 8;
export const NINE = 9;
export const TEN = 10;
export const JACK = 11;
export const QUEEN = 12;
export const KING = 13;
export const ACE = 14;
/** 小王。 */
export const SMALL_JOKER = 15;
/** 大王。 */
export const BIG_JOKER = 16;

/** 是不是王。红桃级牌不能顶替王（第二条 14）。 */
export function isJoker(rank: Rank): boolean {
  return rank >= SMALL_JOKER;
}

/** 把牌点写成牌面：11 → J、15 → 小王。 */
export function rankName(rank: Rank): string {
  switch (rank) {
    case JACK:
      return "J";
    case QUEEN:
      return "Q";
    case KING:
      return "K";
    case ACE:
      return "A";
    case SMALL_JOKER:
      return "小王";
    case BIG_JOKER:
      return "大王";
  }
  return String(rank);
}

/**
 * 认牌面：2–10、J、Q、K、A、小王、大王。
 *
 * 这里**只认规范写法**（大写牌面）。Go 版的 `ParseRank` 用 `strings.ToUpper`，`"j"` 也认，
 * 但它的 `ParseKey` 有「读回来再写出去仍然一样」的校验，所以到了牌例这一层两端一样严；
 * 不同只在这条更靠里的函数上，TS 端这里严一点不会放过任何 README 说无效的写法。
 */
export function parseRank(s: string): Rank {
  switch (s) {
    case "J":
      return JACK;
    case "Q":
      return QUEEN;
    case "K":
      return KING;
    case "A":
      return ACE;
    case "小王":
      return SMALL_JOKER;
    case "大王":
      return BIG_JOKER;
  }
  if (!/^(?:[2-9]|10)$/.test(s)) {
    throw new Error(`认不出牌点 ${JSON.stringify(s)}`);
  }
  return Number(s);
}

/** 花色。`NO_SUIT` 只用在王身上。 */
export type Suit = 0 | 1 | 2 | 3 | 4;

export const SPADE = 0;
export const HEART = 1;
export const CLUB = 2;
export const DIAMOND = 3;
export const NO_SUIT = 4;

/** 把花色写成符号；王是空串。 */
export function suitName(suit: Suit): string {
  switch (suit) {
    case SPADE:
      return "♠";
    case HEART:
      return "♥";
    case CLUB:
      return "♣";
    case DIAMOND:
      return "♦";
  }
  return "";
}

/**
 * 认花色符号。空串表示没有花色（王）。符号后面**不许**跟变体选择符 U+FE0F ——
 * 有些编辑器会把 ♥ 存成 ♥️，两端按字符串比对就会对不上（见 parseKey）。
 */
export function parseSuit(s: string): Suit {
  switch (s) {
    case "♠":
      return SPADE;
    case "♥":
      return HEART;
    case "♣":
      return CLUB;
    case "♦":
      return DIAMOND;
    case "":
      return NO_SUIT;
  }
  throw new Error(`认不出花色 ${JSON.stringify(s)}`);
}

/** 第几副牌（两副牌）。 */
export type Deck = 0 | 1;

/** 一张牌，不带 ID。牌例里写的就是它 —— 写法见 cardKey。 */
export interface CardFace {
  rank: Rank;
  suit: Suit;
  deck: Deck;
}

/**
 * 一张牌。两副牌一共 108 张，每张都不一样：`id` 是它的身份证，记牌、出牌记录、断线恢复
 * 都靠它区分「就是那一张」。ID 就是它在整副牌里的位置（见 newDeck）。
 */
export interface Card extends CardFace {
  id: number;
}

/**
 * 牌在牌例里的写法：「花色 + 牌面 / 副号」，比如 ♠7/0、♥10/1、大王/1。
 * 王没有花色，写成 小王/0、大王/1；副号只有 0 和 1。
 */
export function cardKey(card: CardFace): string {
  return `${suitName(card.suit)}${rankName(card.rank)}/${card.deck}`;
}

/**
 * 读回 cardKey 写出来的那张牌。ID 不在里面 —— ID 由整副牌的位置定（newDeck），
 * 调用方拿结果去索引即可。
 *
 * **只认规范写法**：读出来再写回去必须还是原来那串（`cardKey(parseKey(s)) === s`）。
 * `♠j/0`、`♠07/0`、`♥️7/0`（多一个变体选择符 U+FE0F）这些一律拒绝 ——
 * 牌例是两端逐字符串比对的。
 */
export function parseKey(key: string): CardFace {
  const slash = key.lastIndexOf("/");
  if (slash < 0) {
    throw new Error(`认不出牌 ${JSON.stringify(key)}：要写成「花色+牌面/副号」，比如 ♠7/0、大王/1`);
  }
  const deckText = key.slice(slash + 1);
  if (deckText !== "0" && deckText !== "1") {
    throw new Error(`认不出牌 ${JSON.stringify(key)}：副号只能是 0 或 1`);
  }
  const body = key.slice(0, slash);
  if (body === "") {
    throw new Error(`认不出牌 ${JSON.stringify(key)}：没写牌面`);
  }
  // 王写成 小王/0、大王/1，没有花色；其余的牌第一格是花色符号（四个符号都在 BMP，切一个字就够）。
  let suit: Suit = NO_SUIT;
  let rest = body;
  if (!body.startsWith("小王") && !body.startsWith("大王")) {
    suit = parseSuit(body.slice(0, 1));
    rest = body.slice(1);
  }
  const rank = parseRank(rest);
  if (isJoker(rank) !== (suit === NO_SUIT)) {
    throw new Error(`认不出牌 ${JSON.stringify(key)}：王没有花色，别的牌要有花色`);
  }
  const face: CardFace = { rank, suit, deck: deckText === "0" ? 0 : 1 };
  // 只认规范写法（见函数注释）。这一条同时兜住小写牌面、补零的副号、花色带 U+FE0F。
  const canonical = cardKey(face);
  if (canonical !== key) {
    throw new Error(`认不出牌 ${JSON.stringify(key)}：要写成 ${canonical}`);
  }
  return face;
}

/**
 * 造出两副牌共 108 张，顺序固定：先第 0 副、再第 1 副；每副里花色按 ♠♥♣♦，
 * 花色内按 2、3……10、J、Q、K、A，最后小王、大王。**下标就是牌的 ID**。
 *
 * 这个顺序被 testdata/rules/deck.json 锁着：两端（这里的 TS 版和 server/internal/rules 的
 * Go 版）必须一样，否则同一串牌例在两边指的不是同一张牌。改顺序就是改两端的格式，
 * deck.json 不会由哪一边的 `-update` 重新生成，要先在 #87 上谈好。
 */
export function newDeck(): Card[] {
  const deck: Card[] = [];
  for (let d = 0; d < 2; d++) {
    const copy = d as Deck;
    for (const suit of [SPADE, HEART, CLUB, DIAMOND] as const) {
      for (let rank = TWO; rank <= ACE; rank++) {
        deck.push({ id: deck.length, rank, suit, deck: copy });
      }
    }
    deck.push({ id: deck.length, rank: SMALL_JOKER, suit: NO_SUIT, deck: copy });
    deck.push({ id: deck.length, rank: BIG_JOKER, suit: NO_SUIT, deck: copy });
  }
  return deck;
}
