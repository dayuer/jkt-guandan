import { describe, expect, it } from "vitest";
import { ACE, BIG_JOKER, cardKey, HEART, isJoker, KING, newDeck, SEVEN, SMALL_JOKER, TWO } from "./card";
import { cardPower, isWild, power } from "./power";

describe("牌点大小（第四条 一）", () => {
  it("打 7：大王 > 小王 > 7 > A > K > … > 2，15 档严格递减", () => {
    const order = [16, 15, 7, 14, 13, 12, 11, 10, 9, 8, 6, 5, 4, 3, 2];
    const values = order.map((rank) => power(rank, SEVEN));
    for (let i = 1; i < values.length; i++) {
      expect(values[i - 1], `打 7 时 ${order[i - 1]} 应比 ${order[i]} 大`).toBeGreaterThan(values[i]);
    }
  });

  it("级牌的 8 张牌点一样，都在 A 之上、小王之下", () => {
    const ace = power(ACE, SEVEN);
    const joker = power(SMALL_JOKER, SEVEN);
    const levelCards = newDeck().filter((c) => c.rank === SEVEN);
    expect(levelCards).toHaveLength(8);
    for (const card of levelCards) {
      expect(cardPower(card, SEVEN), cardKey(card)).toBeGreaterThan(ace);
      expect(cardPower(card, SEVEN), cardKey(card)).toBeLessThan(joker);
    }
  });

  it("打 A 时 A 自己就是级牌，头上没有另一档", () => {
    expect(power(ACE, ACE)).toBeGreaterThan(power(KING, ACE));
    expect(power(ACE, ACE)).toBeLessThan(power(SMALL_JOKER, ACE));
  });

  it("花色和副号都不影响大小（第四条 二、（一）4）", () => {
    for (let rank = TWO; rank <= ACE; rank++) {
      const sameRank = newDeck().filter((c) => c.rank === rank);
      expect(sameRank).toHaveLength(8);
      const want = power(rank, SEVEN);
      for (const card of sameRank) expect(cardPower(card, SEVEN), cardKey(card)).toBe(want);
    }
  });

  it("王永远是最大的两张，和打几无关", () => {
    for (let level = TWO; level <= ACE; level++) {
      expect(power(BIG_JOKER, level)).toBeGreaterThan(power(SMALL_JOKER, level));
      for (let rank = TWO; rank <= ACE; rank++) expect(power(15, level)).toBeGreaterThan(power(rank, level));
    }
  });
});

describe("逢人配（第二条 14）", () => {
  it("正好是两张红桃级牌", () => {
    const wild = newDeck().filter((c) => isWild(c, SEVEN));
    expect(wild).toHaveLength(2);
    for (const card of wild) {
      expect(card.rank).toBe(SEVEN);
      expect(card.suit).toBe(HEART);
    }
  });

  it("换了级数就不是了", () => {
    for (const card of newDeck()) expect(isWild(card, TWO)).toBe(card.suit === HEART && card.rank === TWO);
    expect(newDeck().filter((c) => isWild(c, TWO))).toHaveLength(2);
  });

  it("王永远不是逢人配", () => {
    for (const card of newDeck()) {
      if (isJoker(card.rank)) expect(isWild(card, card.rank)).toBe(false);
    }
  });
});
