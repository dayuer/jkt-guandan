import { describe, expect, it } from "vitest";
import {
  ACE,
  BIG_JOKER,
  cardKey,
  isJoker,
  newDeck,
  NO_SUIT,
  parseKey,
  parseRank,
  rankName,
  SMALL_JOKER,
  suitName,
  TWO,
  type CardFace,
  type Suit,
} from "./card";

const keys = (cards: readonly CardFace[]) => cards.map(cardKey);

describe("整副牌（第二条 5：两副牌共 108 张）", () => {
  const deck = newDeck();

  it("108 张，ID 就是它在整副牌里的位置", () => {
    expect(deck).toHaveLength(108);
    deck.forEach((card, i) => expect(card.id).toBe(i));
  });

  it("每个牌点 8 张，大小王各 2 张", () => {
    const counts = new Map<number, number>();
    for (const card of deck) counts.set(card.rank, (counts.get(card.rank) ?? 0) + 1);
    for (let rank = TWO; rank <= ACE; rank++) expect(counts.get(rank), rankName(rank)).toBe(8);
    expect(counts.get(SMALL_JOKER)).toBe(2);
    expect(counts.get(BIG_JOKER)).toBe(2);
  });

  it("王没有花色，别的牌都有", () => {
    for (const card of deck) {
      if (isJoker(card.rank)) expect(card.suit, cardKey(card)).toBe(NO_SUIT);
      else expect(card.suit, cardKey(card)).not.toBe(NO_SUIT);
    }
  });

  it("顺序：先第 0 副再第 1 副，花色 ♠♥♣♦，花色内 2…A，每副最后是小王、大王", () => {
    expect(keys(deck.slice(0, 4))).toEqual(["♠2/0", "♠3/0", "♠4/0", "♠5/0"]);
    expect(keys(deck.slice(52, 54))).toEqual(["小王/0", "大王/0"]);
    expect(keys(deck.slice(54, 56))).toEqual(["♠2/1", "♠3/1"]);
    expect(keys(deck.slice(106, 108))).toEqual(["小王/1", "大王/1"]);
  });
});

describe("牌的写法", () => {
  it("整副牌一来一回都是原样", () => {
    for (const card of newDeck()) {
      expect(parseKey(cardKey(card))).toStrictEqual({ rank: card.rank, suit: card.suit, deck: card.deck });
    }
  });

  it("王写成 小王/0、大王/1，不带花色符号", () => {
    expect(cardKey({ rank: BIG_JOKER, suit: NO_SUIT, deck: 1 })).toBe("大王/1");
    expect(cardKey({ rank: 10, suit: 1, deck: 0 })).toBe("♥10/0");
  });

  // 最后一档是**非规范但读得出**的：小写牌面、补零的副号、花色带变体选择符 U+FE0F ——
  // 这些也一律拒绝（#213 复审：一端松一点，逐字符串比对的另一端就会挂）。
  for (const bad of ["", "♠7", "♠7/2", "♠7/x", "7/0", "♠X/0", "大王♠/0", "A/1", "♠j/0", "♠07/0", "♠7/00", "♥️7/0", "小王♠/0"]) {
    it(`认不出来：${JSON.stringify(bad)}`, () => {
      expect(() => parseKey(bad)).toThrow();
    });
  }

  it("牌面只认大写、副号只认 0 和 1", () => {
    expect(parseRank("10")).toBe(10);
    expect(parseRank("J")).toBe(11);
    expect(() => parseRank("j")).toThrow();
    expect(() => parseRank("11")).toThrow();
    expect(() => parseKey("♠7/1")).not.toThrow();
  });

  it("花色符号只有四个，王是空串", () => {
    expect([0, 1, 2, 3, 4].map((s) => suitName(s as Suit))).toEqual(["♠", "♥", "♣", "♦", ""]);
  });
});
