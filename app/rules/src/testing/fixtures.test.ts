// 读取器自己的单测：**不读牌例文件**，所以 testdata/rules/ 在不在都能跑。
// 每条拒绝都有用例盯着 —— 这些是 #213 开 PR 前审计时手试出来的，固化成测试，
// 以后改读取器不会悄悄放松。
import { describe, expect, it } from "vitest";
import { readCombos, readCompare, readDeal, readDeck, readMoves, readPower } from "./fixtures";

/** 去掉一个字段（比赋值 undefined 更接近「根本没写」）。 */
const without = (obj: object, key: string): Record<string, unknown> => {
  const copy = { ...obj } as Record<string, unknown>;
  delete copy[key];
  return copy;
};

// 最小的合法牌例：读取器只管结构。长度、内容对不对是 testdata.test.ts 的事。
const deckOk = { _comment: ["说明"], rule: ["第二条 5"], order: ["♠2/0"] };
const powerOk = { _comment: ["说明"], vectors: [{ name: "打 2", rule: ["第四条 一"], level: "2", order: ["大王"] }] };
const dealOk = {
  _comment: ["说明"],
  vectors: [{ name: "不洗牌", rule: ["第七条 一、5"], shuffle: [0], hands: [["♠2/0"]] }],
};

describe("读取器：合法的牌例", () => {
  it("最小的牌例都读得过", () => {
    expect(readDeck(deckOk).order).toEqual(["♠2/0"]);
    expect(readPower(powerOk)).toEqual([{ name: "打 2", rule: ["第四条 一"], level: "2", order: ["大王"] }]);
    expect(readDeal(dealOk)).toEqual([{ name: "不洗牌", rule: ["第七条 一、5"], shuffle: [0], hands: [["♠2/0"]] }]);
  });

  it("_comment 可以不写（两端都不读它）", () => {
    expect(() => readDeck(without(deckOk, "_comment"))).not.toThrow();
    expect(() => readPower(without(powerOk, "_comment"))).not.toThrow();
    expect(() => readDeal(without(dealOk, "_comment"))).not.toThrow();
  });
});

// 牌例被清空时一条用例都不生成，整体还是绿的 —— 什么都查不出来。Go 端有同样的「没有用例」检查。
describe("读取器：空用例列表要拒绝", () => {
  it("power / deal 的 vectors 不能是空的", () => {
    expect(() => readPower({ ...powerOk, vectors: [] })).toThrow(/没有用例/);
    expect(() => readDeal({ ...dealOk, vectors: [] })).toThrow(/没有用例/);
  });
});

describe("读取器：缺字段和类型不对要分开报", () => {
  it("缺字段说「缺字段 X（必填…）」", () => {
    expect(() => readDeck(without(deckOk, "rule"))).toThrow(/deck\.json：缺字段 rule（必填/);
    expect(() => readDeck(without(deckOk, "order"))).toThrow(/缺字段 order（必填/);
    expect(() => readPower({ vectors: [without(powerOk.vectors[0], "rule")] })).toThrow(/缺字段 rule（必填/);
    expect(() => readDeal({ vectors: [without(dealOk.vectors[0], "hands")] })).toThrow(/缺字段 hands（必填/);
  });

  it("类型不对说「应该是……」", () => {
    expect(() => readDeck({ ...deckOk, order: "♠2/0" })).toThrow(/order：应该是数组/);
    expect(() => readDeck({ ...deckOk, order: [7] })).toThrow(/order\[0\]：应该是字符串/);
    expect(() => readPower({ vectors: [{ ...powerOk.vectors[0], level: 2 }] })).toThrow(/level：应该是字符串/);
    expect(() => readPower({ vectors: [{ ...powerOk.vectors[0], order: [3] }] })).toThrow(/order\[0\]：应该是字符串/);
    expect(() => readDeal({ vectors: [{ ...dealOk.vectors[0], shuffle: [0.5] }] })).toThrow(/shuffle\[0\]：应该是整数/);
    expect(() => readDeal({ vectors: [{ ...dealOk.vectors[0], shuffle: ["0"] }] })).toThrow(/shuffle\[0\]：应该是整数/);
    expect(() => readDeal({ vectors: [{ ...dealOk.vectors[0], hands: ["♠2/0"] }] })).toThrow(/hands\[0\]：应该是数组/);
    expect(() => readDeal({ vectors: [{ ...dealOk.vectors[0], hands: [[7]] }] })).toThrow(/hands\[0\]\[0\]：应该是字符串/);
  });

  it("_comment 写了就要是字符串数组", () => {
    expect(() => readDeck({ ...deckOk, _comment: 3 })).toThrow(/_comment：应该是数组/);
    expect(() => readDeck({ ...deckOk, _comment: [3] })).toThrow(/_comment\[0\]：应该是字符串/);
  });
});

describe("读取器：条款号（rule）", () => {
  it("一条都没写", () => {
    expect(() => readDeck({ ...deckOk, rule: [] })).toThrow(/没写 rule（条款号）/);
    expect(() => readDeal({ vectors: [{ ...dealOk.vectors[0], rule: [] }] })).toThrow(/没写 rule（条款号）/);
  });

  it("数组里是空串或只有空白", () => {
    expect(() => readDeck({ ...deckOk, rule: [""] })).toThrow(/rule 里有空字符串/);
    expect(() => readPower({ vectors: [{ ...powerOk.vectors[0], rule: ["  "] }] })).toThrow(/rule 里有空字符串/);
  });

  // 正反例表**逐条对着 Go 端的 `TestRuleSyntax`**（server/internal/rules/deck_test.go）：
  // 牌例是逐字符串比对的，两端对「这个写法对不对」的判断必须一样 —— 两边的表一起改、一起跑。
  const WRITABLE = [
    "第二条 14", // 条 + 条目
    "第四条 一", // 条 + 款 —— power.json 全 13 条用例用的是这个
    "第四条 一、2", // 条 + 款 + 条目
    "第四条 二、（一）", // 只引到「（乙）」
    "第四条 二、（一）4", // 条 + 款 +（乙）+ 条目
    "第七条 一、5", // 条 + 款 + 条目 —— deal.json
    "第七条", // 只写整条
  ];
  for (const rule of WRITABLE) {
    it(`认得的写法：${rule}`, () => {
      expect(() => readDeck({ ...deckOk, rule: [rule] })).not.toThrow();
    });
  }

  // Go 那张表的反例里还有一条空串，这边由上面的「空字符串」用例挡住（更靠前的检查）。
  const TYPOS = [
    "第二条14", // 条和条目之间要有空格
    "第二条 14 ", // 尾随空格
    "第二条 14、", // 多余的「、」
    "第四条  一", // 两个空格
    "第四条 一、", // 款后面挂了「、」却没有条目
    "第四条 1、2", // 款不能用阿拉伯数字
    "见第四条 一", // 前面有别的字
    "第四条 一。", // 句号
    "第4条 1", // 条号要用中文数字
    "第四条 二、（一）、4", // 「（乙）」后面直接跟条目，不再加「、」（#213 第三轮正则的原始 bug）
    "第四条二（二）2", // 旧写法：没空格、款项格式也不对
    "第七条一，5", // 旧写法：中文逗号
  ];
  for (const rule of TYPOS) {
    it(`认不出的写法：${rule}`, () => {
      expect(() => readDeck({ ...deckOk, rule: [rule] })).toThrow(/的写法不对/);
    });
  }
});

describe("读取器：combos.json（一手牌 + 全部解读）", () => {
  const ok = {
    _comment: ["说明"],
    vectors: [{ name: "单张", rule: ["第三条 1"], level: "7", cards: ["♠K/0"], combos: [{ kind: "single", rank: "K" }] }],
  };
  const at = (patch: Record<string, unknown>) => ({ vectors: [{ ...ok.vectors[0], ...patch }] });

  it("最小的牌例读得过（combos 可以是空数组：什么牌型都不是）", () => {
    expect(readCombos(ok)).toEqual(ok.vectors);
    expect(readCombos(at({ combos: [] }))[0].combos).toEqual([]);
  });

  it("空用例列表要拒绝", () => expect(() => readCombos({ ...ok, vectors: [] })).toThrow(/没有用例/));

  it("缺字段说「缺字段 X（必填…）」", () => {
    expect(() => readCombos(at({ cards: undefined }))).toThrow(/缺字段 cards（必填/);
    expect(() => readCombos(at({ combos: [without(ok.vectors[0].combos[0], "rank")] }))).toThrow(/缺字段 rank（必填/);
  });

  it("未知字段要拒绝：顶层、用例里、解读里", () => {
    expect(() => readCombos({ ...ok, seats: 4 })).toThrow(/不认识的字段 "seats"/);
    expect(() => readCombos(at({ winner: 1 }))).toThrow(/不认识的字段 "winner"/);
    expect(() => readCombos(at({ combos: [{ kind: "single", rank: "K", extra: 1 }] }))).toThrow(/不认识的字段 "extra"/);
  });

  it("类型不对说「应该是……」", () => {
    expect(() => readCombos(at({ cards: "♠K/0" }))).toThrow(/cards：应该是数组/);
    expect(() => readCombos(at({ combos: ["single"] }))).toThrow(/combos\[0\]：应该是对象/);
    expect(() => readCombos(at({ combos: [{ kind: 1, rank: "K" }] }))).toThrow(/kind：应该是字符串/);
  });
});

describe("读取器：compare.json（a 能不能压 b）", () => {
  const ok = {
    _comment: ["说明"],
    vectors: [
      {
        name: "K 压 5",
        rule: ["第四条 二、（一）"],
        level: "7",
        a: { kind: "pair", cards: ["♠K/0", "♥K/1"] },
        b: { kind: "pair", cards: ["♠5/0", "♥5/1"] },
        result: "a",
      },
    ],
  };
  const at = (patch: Record<string, unknown>) => ({ vectors: [{ ...ok.vectors[0], ...patch }] });

  it("最小的牌例读得过", () => {
    expect(readCompare(ok)).toEqual(ok.vectors);
  });

  it("空用例列表要拒绝", () => expect(() => readCompare({ ...ok, vectors: [] })).toThrow(/没有用例/));

  it("缺字段说「缺字段 X（必填…）」", () => {
    expect(() => readCompare(at({ result: undefined }))).toThrow(/缺字段 result（必填/);
    expect(() => readCompare(at({ a: without(ok.vectors[0].a, "cards") }))).toThrow(/缺字段 cards（必填/);
  });

  it("未知字段要拒绝：顶层、用例里、比较的边上", () => {
    expect(() => readCompare({ ...ok, seats: 4 })).toThrow(/不认识的字段 "seats"/);
    expect(() => readCompare(at({ winner: "a" }))).toThrow(/不认识的字段 "winner"/);
    expect(() => readCompare(at({ a: { kind: "pair", cards: ["♠K/0"], extra: 1 } }))).toThrow(/不认识的字段 "extra"/);
  });

  it("类型不对说「应该是……」", () => {
    expect(() => readCompare(at({ result: 1 }))).toThrow(/result：应该是字符串/);
    expect(() => readCompare(at({ a: ["♠K/0"] }))).toThrow(/a：应该是对象/);
  });
});

describe("读取器：moves.json（合法出法）", () => {
  const ok = {
    _comment: ["说明"],
    vectors: [
      {
        name: "领出",
        rule: ["第三条 1"],
        level: "7",
        hand: ["♠K/0", "♥7/0"],
        moves: [
          { kind: "single", rank: "K", size: 1 },
          { kind: "single", rank: "7", size: 1, wilds: [{ card: "♥7/0", as: "♥7" }] },
        ],
      },
    ],
  };
  const at = (patch: Record<string, unknown>) => ({ vectors: [{ ...ok.vectors[0], ...patch }] });

  it("最小的牌例读得过；lead 可写可不写", () => {
    expect(readMoves(ok)).toEqual(ok.vectors);
    const lead = { kind: "pair", cards: ["♠K/0", "♥K/1"], rank: "K" };
    expect(readMoves(at({ lead }))[0].lead).toEqual(lead);
  });

  it("lead 的 rank 空串等于没写（和 Go 的 `side.Rank != \"\"` 一个口径）", () => {
    const lead = { kind: "pair", cards: ["♠K/0", "♥K/1"], rank: "" };
    expect(readMoves(at({ lead }))[0].lead).toEqual({ kind: "pair", cards: ["♠K/0", "♥K/1"] });
  });

  it("空用例列表要拒绝", () => expect(() => readMoves({ ...ok, vectors: [] })).toThrow(/没有用例/));

  it("缺字段说「缺字段 X（必填…）」", () => {
    expect(() => readMoves(at({ hand: undefined }))).toThrow(/缺字段 hand（必填/);
    expect(() => readMoves(at({ moves: [without(ok.vectors[0].moves[0], "size")] }))).toThrow(/缺字段 size（必填/);
    expect(() => readMoves(at({ moves: [without(ok.vectors[0].moves[0], "rank")] }))).toThrow(/缺字段 rank（必填/);
  });

  it("未知字段要拒绝：顶层、用例里、出法里、lead 里", () => {
    expect(() => readMoves({ ...ok, seats: 4 })).toThrow(/不认识的字段 "seats"/);
    expect(() => readMoves(at({ winner: 1 }))).toThrow(/不认识的字段 "winner"/);
    expect(() => readMoves(at({ moves: [{ kind: "single", rank: "K", size: 1, extra: 1 }] }))).toThrow(
      /不认识的字段 "extra"/,
    );
    expect(() => readMoves(at({ lead: { kind: "pair", cards: ["♠K/0"], extra: 1 } }))).toThrow(/不认识的字段 "extra"/);
  });

  it("类型不对说「应该是……」", () => {
    expect(() => readMoves(at({ hand: "♠K/0" }))).toThrow(/hand：应该是数组/);
    expect(() => readMoves(at({ moves: [{ kind: "single", rank: "K", size: 1.5 }] }))).toThrow(/size：应该是整数/);
    expect(() => readMoves(at({ moves: [{ kind: "single", rank: "K", size: "1" }] }))).toThrow(/size：应该是整数/);
    expect(() => readMoves(at({ moves: [{ kind: "single", rank: "K", size: 1, wilds: [{ card: "♥7/0" }] }] }))).toThrow(
      /缺字段 as（必填/,
    );
  });
});

// README：两端都必须拒绝未知字段 —— 所以加字段是破坏性改动，要先改 README、再两端一起改。
describe("读取器：未知字段要拒绝", () => {
  it("顶层的未知字段", () => {
    expect(() => readDeck({ ...deckOk, level: "7" })).toThrow(/不认识的字段 "level"/);
    expect(() => readPower({ ...powerOk, rules: [] })).toThrow(/不认识的字段 "rules"/);
    expect(() => readDeal({ ...dealOk, seats: 4 })).toThrow(/不认识的字段 "seats"/);
  });

  it("用例里的未知字段", () => {
    expect(() => readPower({ vectors: [{ ...powerOk.vectors[0], suit: "♠" }] })).toThrow(/不认识的字段 "suit"/);
    expect(() => readDeal({ vectors: [{ ...dealOk.vectors[0], seat: 0 }] })).toThrow(/不认识的字段 "seat"/);
  });
});
