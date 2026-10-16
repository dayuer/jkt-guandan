// 性质测试（#87 第 4 步里写着的那几条）：随机手牌上必须恒成立的性质，逐条穷举不出来，用性质兜。
//
// 用固定种子的伪随机数（见 testing/random.ts），红了能照着种子复现；**不读牌例**，所以换了
// 牌例格式也不用动这里。
import { describe, expect, it } from "vitest";
import type { Card, Rank } from "./card";
import { COMBO_TYPES, compare, comboRankName, recognize, type Combo, type Order } from "./combo";
import { isWild } from "./power";
import { sampleHands } from "./testing/random";

const SAMPLES = 400;

interface Sample {
  cards: readonly Card[];
  level: Rank;
  combos: Combo[];
}

/** 一批固定种子的随机手牌，连同它们的全部解读。 */
const corpus: Sample[] = sampleHands(SAMPLES).map(({ cards, level }) => ({
  cards,
  level,
  combos: recognize(cards, level),
}));

/** 手里有两条以上解读的，用来两两比。 */
const pairable = corpus.filter((s) => s.combos.length >= 2);

/** 互为反面：greater↔less，equal 和 incomparable 各自对称。 */
const REVERSED: Record<Order, Order> = {
  greater: "less",
  less: "greater",
  equal: "equal",
  incomparable: "incomparable",
};

const isBombType = (combo: Combo): boolean =>
  combo.type === "bomb" || combo.type === "straight-flush" || combo.type === "joker-bomb";

describe("性质：认牌型", () => {
  it("确定性：同一手牌跑两次结果完全一样（含逢人配的顶法）", () => {
    for (const { cards, level, combos } of corpus) {
      expect(recognize(cards, level)).toStrictEqual(combos);
    }
  });

  it("牌数守恒：每条解读手上的牌就是打出去那几张，牌数不变", () => {
    for (const { cards, combos } of corpus) {
      for (const combo of combos) {
        expect(combo.cards).toHaveLength(cards.length);
        const got = combo.cards.map((c) => c.id).sort((x, y) => x - y);
        const want = cards.map((c) => c.id).sort((x, y) => x - y);
        expect(got, combo.type).toStrictEqual(want);
      }
    }
  });

  it("没有重复解读：同一种牌型下不会有两条一样牌面的", () => {
    for (const { combos } of corpus) {
      const keys = combos.map((c) => `${c.type} ${comboRankName(c)}`);
      expect(new Set(keys).size, keys.join("、")).toBe(keys.length);
    }
  });

  it("顺序固定：先按牌型表的顺序，再按牌面", () => {
    for (const { combos } of corpus) {
      for (let i = 1; i < combos.length; i++) {
        const prev = combos[i - 1];
        const cur = combos[i];
        const byType = COMBO_TYPES.indexOf(prev.type) - COMBO_TYPES.indexOf(cur.type);
        expect(byType < 0 || (byType === 0 && prev.rank <= cur.rank), `${prev.type} → ${cur.type}`).toBe(true);
      }
    }
  });

  it("手里只有逢人配时，只有一种解读（一张＝单张级牌，两张＝一对级牌）", () => {
    let checked = 0;
    for (const { cards, level, combos } of corpus) {
      if (cards.length === 0 || !cards.every((card) => isWild(card, level))) continue;
      checked++;
      expect(combos, `${cards.length} 张逢人配`).toHaveLength(1);
    }
    expect(checked, "样本里应该有「全是逢人配」的手牌，否则这条没被验到").toBeGreaterThan(0);
  });
});

describe("性质：比大小（第四条 二）", () => {
  it("反对称：compare(a,b) 和 compare(b,a) 互为反面", () => {
    for (const { combos } of pairable) {
      for (const a of combos) {
        for (const b of combos) {
          expect(compare(b, a), `${a.type} vs ${b.type}`).toBe(REVERSED[compare(a, b)]);
        }
      }
    }
  });

  it("自反：任何解读和自己比都是「一样大」", () => {
    for (const { combos } of corpus) {
      for (const combo of combos) expect(compare(combo, combo), combo.type).toBe("equal");
    }
  });

  it("传递：a>b 且 b>c ⇒ a>c", () => {
    for (const { combos } of pairable) {
      for (const a of combos) {
        for (const b of combos) {
          if (compare(a, b) !== "greater") continue;
          for (const c of combos) {
            if (compare(b, c) !== "greater") continue;
            expect(compare(a, c), `${a.type} > ${b.type} > ${c.type}`).toBe("greater");
          }
        }
      }
    }
  });

  it("「一样大」蕴含同牌型、同张数", () => {
    for (const { combos } of pairable) {
      for (const a of combos) {
        for (const b of combos) {
          if (compare(a, b) !== "equal") continue;
          expect(a.type).toBe(b.type);
          expect(a.cards).toHaveLength(b.cards.length);
        }
      }
    }
  });

  it("炸弹压普通牌型，普通牌型压不了炸弹", () => {
    for (const { combos } of pairable) {
      for (const a of combos) {
        for (const b of combos) {
          if (isBombType(a) && !isBombType(b)) expect(compare(a, b), `${a.type} vs ${b.type}`).toBe("greater");
          if (!isBombType(a) && isBombType(b)) expect(compare(a, b), `${a.type} vs ${b.type}`).toBe("less");
        }
      }
    }
  });
});
