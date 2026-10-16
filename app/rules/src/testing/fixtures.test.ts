// 读取器自己的单测：**不读牌例文件**，所以 testdata/rules/ 在不在都能跑。
// 每条拒绝都有用例盯着 —— 这些是 #213 开 PR 前审计时手试出来的，固化成测试，
// 以后改读取器不会悄悄放松。
import { describe, expect, it } from "vitest";
import { readDeal, readDeck, readPower } from "./fixtures";

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

  // 写法的格式校验（README 的四种写法 + 常见笔误的正反例表）和 Go 端一起加 ——
  // #213 第三轮那版正则和 README 对不上（`第四条 二、（一）4` 过不了），正在改，
  // 以 README 为准。格式没定死之前单方面加，会出现「同一条牌例一端过、另一端挂」。
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
