// 两端共用的牌例（#87 第 1 步）：这里和服务端的 server/internal/rules/fixtures_test.go
// 读仓库根 testdata/rules/ 下同一批文件。规则口径改在哪一头，这里都会红。
//
// 结构校验（`rule` 非空、拒绝未知字段、不能空用例列表……）由 testing/fixtures.ts 统一把关
// 并有自己的单测，这里只查「算出来的结果和牌例写的一样」。
import { describe, expect, it } from "vitest";
import { cardKey, newDeck, parseKey, parseRank, type Card } from "./card";
import { deal, HANDS_EACH, permShuffler, SEATS } from "./deal";
import { power } from "./power";
import { dealFixture, deckFixture, powerFixture } from "./testing/testdata";

const keys = (cards: readonly Card[]): string[] => cards.map(cardKey);

describe("deck.json：整副牌的顺序是两端的约定", () => {
  it("和 newDeck() 逐张一样", () => {
    expect(deckFixture.order).toStrictEqual(keys(newDeck()));
  });

  it("写法能读回同一张牌（ID 由位置定，不写在写法里）", () => {
    const deck = newDeck();
    deckFixture.order.forEach((written, i) => {
      const face = parseKey(written);
      expect(face, written).toStrictEqual({ rank: deck[i].rank, suit: deck[i].suit, deck: deck[i].deck });
    });
  });
});

describe("power.json：每个级数下牌点由大到小（第四条 一）", () => {
  it("13 个级数全列了，没有重复", () => {
    const levels = powerFixture.map((v) => parseRank(v.level));
    expect(levels).toHaveLength(13);
    expect(new Set(levels).size).toBe(13);
  });

  for (const vector of powerFixture) {
    it(vector.name, () => {
      const level = parseRank(vector.level);
      expect(vector.order, "应该列 15 个牌点（2–A + 大小王）").toHaveLength(15);
      const ranks = vector.order.map(parseRank);
      expect(new Set(ranks).size, "同一个牌点不该出现两次").toBe(15);
      for (let i = 1; i < ranks.length; i++) {
        expect(
          power(ranks[i - 1], level),
          `打 ${vector.level} 时 ${vector.order[i - 1]} 应比 ${vector.order[i]} 大`,
        ).toBeGreaterThan(power(ranks[i], level));
      }
    });
  }
});

describe("deal.json：发牌顺序 → 四手牌（第七条 一、5）", () => {
  for (const vector of dealFixture) {
    it(vector.name, () => {
      const hands = deal(permShuffler(vector.shuffle));

      // 结构上的硬条件，和牌例写的是什么无关：每名运动员 27 张（第二条 6），
      // 四手合起来正好是整副 108 张、没有一张重复。
      expect(hands).toHaveLength(SEATS);
      for (const hand of hands) expect(hand).toHaveLength(HANDS_EACH);
      expect(new Set(hands.flat().map((card) => card.id)).size).toBe(108);

      expect(hands.map(keys)).toStrictEqual(vector.hands);
    });
  }
});
