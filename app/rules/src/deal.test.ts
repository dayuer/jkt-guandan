import { describe, expect, it } from "vitest";
import { cardKey, newDeck } from "./card";
import { deal, HANDS_EACH, permShuffler, SEATS, type Shuffler } from "./deal";

const identity = (n: number): number[] => Array.from({ length: n }, (_, i) => i);
const backwards = (n: number): number[] => Array.from({ length: n }, (_, i) => n - 1 - i);

describe("发牌（第七条 一、5：从首抓者起逆时针，每人每次一张）", () => {
  it("不洗牌：第 k 家拿的是整副牌的第 k、4、8…张，每家 27 张", () => {
    const deck = newDeck();
    const hands = deal(permShuffler(identity(108)));
    expect(hands).toHaveLength(SEATS);
    for (let seat = 0; seat < SEATS; seat++) {
      expect(hands[seat]).toHaveLength(HANDS_EACH);
      hands[seat].forEach((card, j) => expect(cardKey(card)).toBe(cardKey(deck[j * SEATS + seat])));
    }
  });

  it("发到的牌按发到的先后排，不排序", () => {
    const hands = deal(permShuffler(backwards(108)));
    expect(cardKey(hands[0][0])).toBe("大王/1");
    expect(cardKey(hands[1][0])).toBe("小王/1");
  });

  it("四手合起来正好是整副牌，一张不重复", () => {
    const hands = deal(permShuffler(identity(108)));
    const ids = hands.flat().map((card) => card.id);
    expect(ids).toHaveLength(108);
    expect(new Set(ids).size).toBe(108);
  });
});

describe("随机源（外部注入，见 #204「随机源注入」）", () => {
  // 发一副坏牌是最难查的一类错：复盘时牌对不上，却看不出是哪一步错的。
  const bad: [string, Shuffler | null | undefined][] = [
    ["没有随机源", null],
    ["随机源是 undefined", undefined],
    ["没返回排列", (() => undefined) as unknown as Shuffler],
    ["返回空", () => []],
    ["少了几张", (n) => identity(n - 1)],
    ["多给了几张", (n) => [...identity(n), 0]],
    ["位置越界", (n) => [n, ...identity(n).slice(1)]],
    ["负数位置", (n) => [-1, ...identity(n).slice(1)]],
    ["位置不是整数", (n) => [1.5, ...identity(n).slice(1)]],
    ["同一张牌给了两次", (n) => [1, 1, ...identity(n).slice(2)]],
  ];
  for (const [name, shuffle] of bad) {
    it(`${name} → 抛错`, () => expect(() => deal(shuffle)).toThrow(/发牌：/));
  }

  it("permShuffler 不改调用方的排列，同一个排列发两次牌一样", () => {
    const perm = identity(108);
    [perm[0], perm[1]] = [perm[1], perm[0]];
    const before = [...perm];

    const first = deal(permShuffler(perm));
    const second = deal(permShuffler(perm));
    expect(first).toStrictEqual(second);
    expect(perm).toStrictEqual(before);
  });
});
