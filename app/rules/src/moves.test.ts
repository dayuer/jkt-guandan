// 合法出法的单测和性质测试。牌例那份（两端共用）在 testdata.test.ts；这里管牌例没覆盖到的
// 边界，以及**和暴力枚举对拍** —— 那条是 #204 第 4 步验证过、抓出过真 bug 的（见下面）。
import { describe, expect, it } from "vitest";
import { SEVEN, type Card, type Rank } from "./card";
import { comboRankName, compare, recognize, sortCards, type Combo, type ComboType } from "./combo";
import { comboKey, moves } from "./moves";
import { handOf } from "./testing/cards";
import { sampleHands } from "./testing/random";

/** 各牌型定死的张数；炸弹 4–10 张不固定，所以是 null。 */
const FIXED_SIZE: Record<ComboType, number | null> = {
  single: 1,
  pair: 2,
  triple: 3,
  "triple-pair": 5,
  tube: 6,
  plate: 6,
  straight: 5,
  "straight-flush": 5,
  bomb: null,
  "joker-bomb": 4,
};

/** 列出打法，写成 `牌型 牌面` 的一串，方便断言。 */
const listed = (combos: readonly Combo[]): string[] => combos.map((c) => `${c.type} ${comboRankName(c)}`);

/** 从这手牌里取指定牌型的那一种解读，用来当「上一手」。 */
function comboOf(written: string[], type: ComboType, level: Rank = SEVEN): Combo {
  const found = moves(handOf(written), level).find((c) => c.type === type);
  if (!found) throw new Error(`这手牌打不出 ${type}：${written.join(" ")}`);
  return found;
}

describe("列合法出法：领出", () => {
  it("手里没逢人配：每个点数都能单出，成对的、成三的、三带对也列出来", () => {
    const hand = ["♠5/0", "♥5/1", "♣9/0", "♠9/0", "♥9/0", "♦K/0"];
    expect(listed(moves(handOf(hand), SEVEN))).toEqual([
      "single 5",
      "single 9",
      "single K",
      "pair 5",
      "pair 9",
      "triple 9",
      "triple-pair 9",
    ]);
  });

  it("手里只有逢人配：一张＝单张级牌，两张＝一对级牌（不枚举顶法）", () => {
    expect(listed(moves(handOf(["♥7/0"]), SEVEN))).toEqual(["single 7"]);
    // 两张逢人配时 `single 7` 会出现**两次**：单出 ♥7/0 和单出 ♥7/1 是两张不同的牌，
    // 去重键里带「哪张逢人配」，所以算两条。和 Go 的 Moves 一致（同一段代码）。
    // ⚠️ moves.json 的 8 条里没有这个场景，两端没被牌例钉过 —— 已报给 coord 请它和 jkt 确认。
    expect(listed(moves(handOf(["♥7/0", "♥7/1"]), SEVEN))).toEqual(["single 7", "single 7", "pair 7"]);
  });

  it("同一个打法只留一套牌：两张实牌 9 和「一张 9 + 逢人配当 9」是两种打法，各留一条", () => {
    const pairs = moves(handOf(["♠9/0", "♥9/0", "♥7/0"]), SEVEN).filter((c) => c.type === "pair");
    expect(pairs.map((c) => c.wilds.length).sort()).toEqual([0, 1]);
  });

  // #204 第 4 步踩过的坑：去重键漏了张数，手里七个 3 就只剩「4 张炸弹」，跟 5 张炸弹时报「无牌可跟」。
  it("炸弹 4、5、6、7 张是四种打法，不能被当成同一条", () => {
    const seven = ["♠3/0", "♥3/0", "♣3/0", "♦3/0", "♠3/1", "♥3/1", "♣3/1"];
    expect(
      moves(handOf(seven), SEVEN)
        .filter((c) => c.type === "bomb")
        .map((c) => c.cards.length),
    ).toEqual([4, 5, 6, 7]);
  });
});

describe("列合法出法：跟牌", () => {
  it("同牌型比点数：只列压得住的那几种", () => {
    const got = moves(handOf(["♠A/0", "♦A/1", "♥3/0"]), SEVEN, comboOf(["♠K/0", "♥K/1"], "pair"));
    expect(listed(got)).toEqual(["pair A"]);
  });

  it("压不住就是空数组（过牌不在这个集合里，由调用方自己加）", () => {
    expect(moves(handOf(["♠3/0", "♥5/0"]), SEVEN, comboOf(["♠A/0", "♥A/0"], "pair"))).toEqual([]);
  });

  it("炸弹压普通牌型；普通牌型压不了炸弹", () => {
    const bomb = comboOf(["♠8/0", "♥8/0", "♣8/0", "♦8/0"], "bomb");
    const triplePair = comboOf(["♠9/0", "♥9/0", "♣9/1", "♠4/0", "♥4/1"], "triple-pair");
    // 三带对再大也压不了炸弹
    expect(moves(handOf(["♠9/0", "♥9/0", "♣9/0", "♠4/0", "♥4/0"]), SEVEN, bomb)).toEqual([]);
    // 而炸弹压得住三带对
    expect(moves(handOf(["♠8/0", "♥8/0", "♣8/0", "♦8/0"]), SEVEN, triplePair).map((c) => c.type)).toEqual(["bomb"]);
  });

  it("逢人配凑出来的炸弹也算炸弹", () => {
    const got = moves(handOf(["♠A/0", "♥A/0", "♣A/0", "♥7/0"]), SEVEN, comboOf(["♠K/0", "♥K/1"], "pair"));
    expect(got.some((c) => c.type === "bomb" && comboRankName(c) === "A")).toBe(true);
  });
});

describe("列合法出法的入参错误", () => {
  it("手里没有牌", () => expect(() => moves([], SEVEN)).toThrow(/手里没有牌/));
  it("级数超出 2–A", () => expect(() => moves(handOf(["♠7/0"]), 15)).toThrow(/超出 2–A/));
  it("同一张牌两次", () => {
    const card = handOf(["♠7/0"])[0];
    expect(() => moves([card, card], SEVEN)).toThrow(/出现了两次/);
  });
  it("上一手没有牌", () => {
    const empty: Combo = { type: "single", cards: [], wilds: [], rank: 7, value: 15 };
    expect(() => moves(handOf(["♠7/0"]), SEVEN, empty)).toThrow(/上一手没有牌/);
  });
});

const idsAsc = (cards: readonly Card[]): number[] => cards.map((c) => c.id).sort((a, b) => a - b);

/** 按 ID 排好之后比两套牌（和实现里那个 `lessCards` 同义，这里独立写一遍）。 */
function lessIds(a: readonly number[], b: readonly number[]): boolean {
  for (let i = 0; i < a.length; i++) {
    if (i >= b.length) return false;
    if (a[i] !== b[i]) return a[i] < b[i];
  }
  return a.length < b.length;
}

/**
 * 把一个出法集合压成「打法 → 留下那套牌的 ID」。
 *
 * **对拍比的是这个映射，不是「有几套牌」**：两个人各拿一张 ♠K，单出它俩是同一个打法
 * （实现按 `comboKey` 只留 ID 最小的那一套），可暴力枚举会把两张都列出来。
 */
function treatments(combos: readonly Combo[]): Map<string, number[]> {
  const out = new Map<string, number[]>();
  for (const combo of combos) {
    const ids = idsAsc(combo.cards);
    const key = comboKey(combo);
    const old = out.get(key);
    if (!old || lessIds(ids, old)) out.set(key, ids);
  }
  return out;
}

/** 暴力：把 ≤ 8 张手牌的**全部子集**都认一遍，收出全部合法出法。只用来对拍，不追求性能。 */
function bruteMoves(hand: readonly Card[], level: Rank, against?: Combo): Combo[] {
  const cards = sortCards(hand);
  const out: Combo[] = [];
  for (let mask = 1; mask < 1 << cards.length; mask++) {
    const subset = cards.filter((_, i) => (mask & (1 << i)) !== 0);
    if (subset.length > 10) continue; // 一手最多 10 张
    for (const combo of recognize(subset, level)) {
      if (against && compare(combo, against) !== "greater") continue;
      out.push(combo);
    }
  }
  return out;
}

/** ≤ 8 张才好穷举子集（256 个）；手牌从固定种子里取，红了能复现。 */
const SMALL = sampleHands(400, 20261017)
  .filter((s) => s.cards.length <= 8)
  .slice(0, 40);

describe("性质：列合法出法", () => {
  const corpus = sampleHands(300, 20261018).map(({ cards, level }) => ({ cards, level, moves: moves(cards, level) }));

  it("每一手的牌都在手里、不重复、张数符合牌型", () => {
    for (const { cards, moves: list } of corpus) {
      const ids = new Set(cards.map((c) => c.id));
      for (const move of list) {
        const used = move.cards.map((c) => c.id);
        expect(new Set(used).size, `${move.type} 里有重复的牌`).toBe(used.length);
        for (const id of used) expect(ids.has(id), `${move.type} 用了不在手里的牌`).toBe(true);
        const fixed = FIXED_SIZE[move.type];
        if (fixed === null) {
          expect(move.cards.length, `炸弹的张数`).toBeGreaterThanOrEqual(4);
          expect(move.cards.length).toBeLessThanOrEqual(10);
        } else {
          expect(move.cards.length, `${move.type} 的张数`).toBe(fixed);
        }
      }
    }
  });

  it("领出永远非空：手里有牌就一定打得出去（至少每张牌能单出）", () => {
    for (const { cards, moves: list } of corpus) expect(list.length, `${cards.length} 张`).toBeGreaterThan(0);
  });

  // 「没有两手**互相**压得住」—— 不是「任意两手都不互相压」（那一手牌里单张 2…A 会互相压）。
  // 和 Go 的 `TestMovesAntisymmetric` 一个口径。
  it("同一个集合里没有两手互相压得住", () => {
    for (const { moves: list } of corpus) {
      for (const a of list) {
        for (const b of list) {
          if (a === b) continue;
          const mutual = compare(a, b) === "greater" && compare(b, a) === "greater";
          expect(mutual, `${a.type} ${comboRankName(a)} 和 ${b.type} ${comboRankName(b)} 互相压得住`).toBe(false);
        }
      }
    }
  });

  it("跟牌时每一手都真的压得住上一手", () => {
    for (const { cards, level, moves: list } of corpus) {
      for (const against of list.slice(0, 3)) {
        for (const follow of moves(cards, level, against)) {
          expect(compare(follow, against), `${follow.type} vs ${against.type}`).toBe("greater");
        }
      }
    }
  }, 30000);
});

describe("性质：和暴力枚举对拍（≤ 8 张）", () => {
  it("领出：全部子集认一遍，打法集合和各自留下的那套牌都必须一模一样", () => {
    for (const { cards, level } of SMALL) {
      expect(treatments(moves(cards, level)), `${cards.length} 张`).toStrictEqual(
        treatments(bruteMoves(cards, level)),
      );
    }
  }, 60000);

  it("跟牌：拿别的手牌的一手当上一手，同样必须一模一样", () => {
    for (let i = 0; i < SMALL.length; i++) {
      const { cards, level } = SMALL[i];
      const against = moves(SMALL[(i + 1) % SMALL.length].cards, level)[0];
      const what = `${cards.length} 张，跟 ${against.type} ${comboRankName(against)}`;
      expect(treatments(moves(cards, level, against)), what).toStrictEqual(
        treatments(bruteMoves(cards, level, against)),
      );
    }
  }, 60000);
});
