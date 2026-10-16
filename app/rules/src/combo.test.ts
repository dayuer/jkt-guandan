// 牌型识别和比较的单测：**不读牌例**，管的是牌例没覆盖到的语义和边界
// （牌例那份在 testdata.test.ts，两端共用）。两个文件测同一份实现，各有各的用处。
import { describe, expect, it } from "vitest";
import { ACE, cardKey, rankName, SEVEN, TWO } from "./card";
import { asKey, COMBO_TYPES, comboLabel, comboRankName, compare, findCombo, parseComboType, recognize } from "./combo";
import { handOf } from "./testing/cards";

/** 一手牌的全部解读，写成 `牌型 牌面` 的一串，方便断言。 */
const readings = (written: string[], level = SEVEN): string[] =>
  recognize(handOf(written), level).map((c) => `${c.type} ${comboRankName(c)}`.trim());

describe("认牌型（第三条）", () => {
  it("单张、对子、三同张：同点数的 1/2/3 张", () => {
    expect(readings(["♠K/0"])).toEqual(["single K"]);
    expect(readings(["♠K/0", "♥K/1"])).toEqual(["pair K"]);
    expect(readings(["♠K/0", "♥K/1", "♦K/0"])).toEqual(["triple K"]);
  });

  it("三带对：只标三同张的点数（第四条 二、（一）2）", () => {
    expect(readings(["♠7/0", "♦7/0", "♣7/0", "♠8/0", "♥8/0"])).toEqual(["triple-pair 7"]);
  });

  it("三连对：三个牌点相邻的对子，顶张最大的那个", () => {
    expect(readings(["♠5/0", "♥5/0", "♠6/0", "♥6/0", "♠7/0", "♥7/0"])).toEqual(["tube 7"]);
  });

  it("三同连张：两个牌点相邻的三同张", () => {
    expect(readings(["♠5/0", "♦5/0", "♣5/0", "♠6/0", "♦6/0", "♣6/0"])).toEqual(["plate 6"]);
  });

  it("顺子：五个相邻的单张；同花色时**同时**给出同花顺（第三条 9）", () => {
    expect(readings(["♠5/0", "♥6/0", "♣7/0", "♦8/0", "♠9/0"])).toEqual(["straight 9"]);
    expect(readings(["♠5/0", "♠6/0", "♠7/0", "♠8/0", "♠9/0"])).toEqual(["straight 9", "straight-flush 9"]);
  });

  it("炸弹：4 到 10 张同点数；3 张不是", () => {
    expect(readings(["♠4/0", "♥4/0", "♦4/0", "♣4/0"])).toEqual(["bomb 4"]);
    expect(readings(["♠4/0", "♥4/0", "♦4/0", "♣4/0", "♠4/1", "♥4/1", "♦4/1", "♣4/1"])).toEqual(["bomb 4"]);
    expect(readings(["♠4/0", "♥4/0", "♦4/0"])).toEqual(["triple 4"]);
  });

  it("四大天王：大小王各两张，没有点数（牌例里写空串）", () => {
    const jokers = ["大王/0", "大王/1", "小王/0", "小王/1"];
    expect(readings(jokers)).toEqual(["joker-bomb"]);
    expect(comboRankName(recognize(handOf(jokers), SEVEN)[0])).toBe("");
  });

  it("什么牌型都不是 → 空数组，不是错误", () => {
    expect(readings(["♠3/0", "♠5/0"])).toEqual([]);
    expect(readings(["♠3/0", "♥4/0", "♣6/0"])).toEqual([]);
  });

  it("解读固定排序：先按牌型表的顺序，再按牌面", () => {
    // 同花色的 A2345：顺子（第三条 7）在同花顺（第三条 9）前面
    expect(readings(["♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0"])).toEqual(["straight 5", "straight-flush 5"]);
  });
});

describe("A 在顺子类里的两种读法（第四条 一、2）", () => {
  it("当最小的 1：A2345 的顶张是 5", () => {
    expect(readings(["♠A/0", "♥2/0", "♣3/0", "♦4/0", "♠5/0"])).toEqual(["straight 5"]);
  });

  it("当最大的 14：10JQKA 的顶张是 A", () => {
    expect(readings(["♠10/0", "♥J/0", "♣Q/0", "♦K/0", "♠A/0"])).toEqual(["straight A"]);
  });

  it("两头都接不行：QKA23 不是顺子", () => {
    expect(readings(["♠Q/0", "♥K/0", "♣A/0", "♦2/0", "♠3/0"])).toEqual([]);
  });

  it("三连对和钢板里也能当 1：AA2233、AAA222", () => {
    expect(readings(["♠A/0", "♥A/0", "♠2/0", "♥2/0", "♠3/0", "♥3/0"])).toEqual(["tube 3"]);
    expect(readings(["♠A/0", "♦A/0", "♣A/0", "♠2/0", "♦2/0", "♣2/0"])).toEqual(["plate 2"]);
  });

  it("王不参与顺子类", () => {
    expect(readings(["♠10/0", "♥J/0", "♣Q/0", "♦K/0", "小王/0"])).toEqual([]);
    expect(readings(["♠10/0", "♥J/0", "♣Q/0", "♦K/0", "大王/0"])).toEqual([]);
  });
});

describe("级牌在两边不一样（第四条 一、第五条 1）", () => {
  it("单张按当前级数算：打 7 时单张 7 压单张 A", () => {
    expect(recognize(handOf(["♠7/0"]), SEVEN)[0].value).toBeGreaterThan(recognize(handOf(["♠A/0"]), SEVEN)[0].value);
  });

  it("顺子类按牌面原位置算：打 7 时 34567 的顶张还是 7", () => {
    const [straight] = recognize(handOf(["♠3/0", "♥4/0", "♣5/0", "♦6/0", "♠7/0"]), SEVEN);
    expect(straight.rank).toBe(SEVEN);
    expect(straight.value).toBe(SEVEN); // 不是级牌那个 15
  });
});

describe("认牌型的入参错误", () => {
  it("没有牌", () => expect(() => recognize([], SEVEN)).toThrow(/没有牌/));
  it("同一张牌两次", () => {
    const card = handOf(["♠7/0"])[0];
    expect(() => recognize([card, card], SEVEN)).toThrow(/出现了两次/);
  });
  it("级数超出 2–A", () => {
    expect(() => recognize(handOf(["♠7/0"]), 1)).toThrow(/超出 2–A/);
    expect(() => recognize(handOf(["♠7/0"]), 15)).toThrow(/超出 2–A/);
  });
});

describe("比大小（第四条 二）", () => {
  const one = (written: string[], level = SEVEN) => recognize(handOf(written), level)[0];
  const straightFlush = (top: string[]) => recognize(handOf(top), SEVEN).find((c) => c.type === "straight-flush")!;

  it("同牌型同张数比点数", () => {
    expect(compare(one(["♠K/0", "♥K/1"]), one(["♠5/0", "♥5/1"]))).toBe("greater");
    expect(compare(one(["♠5/0", "♥5/1"]), one(["♠K/0", "♥K/1"]))).toBe("less");
  });

  it("同牌型不同张数：普通牌型没得比", () => {
    expect(compare(one(["♠K/0", "♥K/1", "♦K/0"]), one(["♠K/0", "♥K/1"]))).toBe("incomparable");
  });

  it("牌型不同、又不是炸弹压普通牌型：没得比", () => {
    expect(compare(one(["♠K/0", "♥K/1"]), one(["♠K/0"]))).toBe("incomparable");
  });

  it("炸弹压普通牌型，普通牌型压不了炸弹", () => {
    const bomb = one(["♠4/0", "♥4/0", "♦4/0", "♣4/0"]);
    const pair = one(["♠A/0", "♥A/0"]);
    expect(compare(bomb, pair)).toBe("greater");
    expect(compare(pair, bomb)).toBe("less");
  });

  it("炸弹档次：四张 < 五张 < 同花顺 < 六张及以上 < 四大天王", () => {
    const four = one(["♠4/0", "♥4/0", "♦4/0", "♣4/0"]);
    const five = one(["♠4/0", "♥4/0", "♦4/0", "♣4/0", "♠4/1"]);
    const six = one(["♠4/0", "♥4/0", "♦4/0", "♣4/0", "♠4/1", "♥4/1"]);
    const flush = straightFlush(["♠10/0", "♠J/0", "♠Q/0", "♠K/0", "♠A/0"]);
    const jokers = one(["大王/0", "大王/1", "小王/0", "小王/1"]);

    // 打 2 时四张 2 是炸弹里最大的点数，拿它跟别的档比，排除了点数的影响
    const tiers = [four, five, flush, six, jokers];
    for (let i = 1; i < tiers.length; i++) {
      expect(compare(tiers[i], tiers[i - 1]), `第 ${i + 1} 档该压第 ${i} 档`).toBe("greater");
      expect(compare(tiers[i - 1], tiers[i])).toBe("less");
    }
  });

  it("同档同张数比点数；同花顺比顶张", () => {
    expect(compare(one(["♠K/0", "♥K/0", "♦K/0", "♣K/0"]), one(["♠4/0", "♥4/0", "♦4/0", "♣4/0"]))).toBe("greater");
    expect(compare(straightFlush(["♠10/0", "♠J/0", "♠Q/0", "♠K/0", "♠A/0"]), straightFlush(["♠5/0", "♠6/0", "♠7/0", "♠8/0", "♠9/0"]))).toBe(
      "greater",
    );
  });

  it("四大天王碰上四大天王只有一种结果：一样大", () => {
    const jokers = one(["大王/0", "大王/1", "小王/0", "小王/1"]);
    expect(compare(jokers, jokers)).toBe("equal");
  });

  it("三带对只比三同张，不比所带的对子", () => {
    const big = one(["♠9/0", "♦9/0", "♣9/0", "♠2/0", "♥2/0"]);
    const small = one(["♠8/0", "♦8/0", "♣8/0", "♠A/0", "♥A/0"]);
    expect(compare(big, small)).toBe("greater");
  });
});

describe("牌型的名字", () => {
  it("十个取值，和 README 那张表一致", () => {
    expect(COMBO_TYPES).toEqual([
      "single",
      "pair",
      "triple",
      "triple-pair",
      "tube",
      "plate",
      "straight",
      "straight-flush",
      "bomb",
      "joker-bomb",
    ]);
    for (const type of COMBO_TYPES) expect(parseComboType(type)).toBe(type);
    expect(() => parseComboType("三带二")).toThrow(/认不出牌型/);
  });

  it("有中文名，报错和测试消息里用", () => {
    expect(comboLabel("straight-flush")).toBe("同花顺");
    expect(rankName(ACE)).toBe("A");
  });

  it("findCombo 按声明的牌型找解读；打不出来就是 undefined", () => {
    const cards = ["♠5/0", "♠6/0", "♠7/0", "♠8/0", "♠9/0"];
    expect(findCombo(handOf(cards), SEVEN, "straight")?.rank).toBe(9);
    expect(findCombo(handOf(cards), SEVEN, "straight-flush")?.rank).toBe(9);
    expect(findCombo(handOf(cards), TWO, "bomb")).toBeUndefined();
  });
});

// 逐条对着 README「逢人配」那一节。
describe("逢人配（第二条 14、第五条 2）", () => {
  const all = (written: string[], level = SEVEN) => recognize(handOf(written), level);
  const tops = (written: string[], level = SEVEN) => all(written, level).map((c) => `${c.type} ${comboRankName(c)}`.trim());
  const wildsOf = (written: string[], type: string, rank: string, level = SEVEN) => {
    const combo = all(written, level).find((c) => c.type === type && comboRankName(c) === rank);
    return combo ? combo.wilds.map((w) => `${cardKey(w.card)} 当 ${asKey(w.as)}`).sort() : undefined;
  };

  it("搭配牌型时枚举全部顶法：♠5678 + ♥7 既是顺子也是同花顺，顶张 8 或 9", () => {
    // 四张黑桃 + 一张逢人配：顶成 ♠9 是 56789 同花顺，顶成红桃/方块 9 只是顺子；
    // 顶成 ♠4 是 45678，顶成 ♠9 是 56789 —— 顺子的顶张有 8 和 9 两种。
    expect(tops(["♠5/0", "♠6/0", "♠7/0", "♠8/0", "♥7/0"])).toEqual([
      "straight 8",
      "straight 9",
      "straight-flush 8",
      "straight-flush 9",
    ]);
  });

  it("同一种（牌型 + 牌面）有好几种顶法时，只留顶法最小的那种", () => {
    // ♥7 当 ♠A 或当 ♥A 都是一对 A；按「点数、花色」最小挑，留 ♠A。
    expect(wildsOf(["♠A/0", "♥7/0"], "pair", "A")).toEqual(["♥7/0 当 ♠A"]);
  });

  it("只拿逢人配自己出（一张、两张）时只按本来的点数算，不枚举顶法", () => {
    expect(tops(["♥7/0"])).toEqual(["single 7"]);
    expect(tops(["♥7/0", "♥7/1"])).toEqual(["pair 7"]);
    // 顶成它自己，界面上显示就是「♥7/0」而不是「♥7/0 当 ♥7」
    expect(all(["♥7/0"])[0].wilds.map((w) => cardKey(w.card))).toEqual(["♥7/0"]);
  });

  it("`as` 不带副号（顶出来的不是手里那张牌）", () => {
    const [combo] = all(["♠7/0", "♥7/1"]);
    // 只有花色和牌面 —— 多出「副号」会让人以为手里真有那一张
    expect(Object.keys(combo.wilds[0].as).sort()).toEqual(["rank", "suit"]);
    expect(asKey(combo.wilds[0].as)).toBe("♠7");
  });

  it("不能顶王：♥7 + 大王 凑不成对子，两张逢人配也凑不出四大天王", () => {
    expect(tops(["♥7/0", "大王/0"])).toEqual([]);
    expect(tops(["大王/0", "小王/0", "♥7/0", "♥7/1"])).toEqual([]);
  });

  it("顶出来的牌不占真牌的名额：八张 8 加两张逢人配就是十张炸", () => {
    const eight = ["♠8/0", "♥8/0", "♣8/0", "♦8/0", "♠8/1", "♥8/1", "♣8/1", "♦8/1"];
    const [combo] = all([...eight, "♥7/0", "♥7/1"]);
    expect(combo.type).toBe("bomb");
    expect(combo.cards).toHaveLength(10);
    expect(comboRankName(combo)).toBe("8");
  });

  it("实打实的牌超过五个点数：怎么顶都凑不成牌型，直接判打不出去", () => {
    expect(tops(["♠2/0", "♠3/0", "♠4/0", "♠5/0", "♠6/0", "♠9/0", "♥7/0"])).toEqual([]);
  });

  it("比大小不看顶法，看牌面和张数（♥7 凑的同花顺就是同花顺）", () => {
    const wildFlush = all(["♠3/0", "♠4/0", "♠5/0", "♠6/0", "♥7/0"]).find((c) => c.type === "straight-flush")!;
    const fiveBomb = all(["♠A/0", "♥A/0", "♣A/0", "♦A/0", "♠A/1"])[0];
    expect(compare(wildFlush, fiveBomb)).toBe("greater");
  });
});
