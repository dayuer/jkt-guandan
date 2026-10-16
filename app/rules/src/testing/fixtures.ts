// testdata/rules/*.json 的**严格**读取器（#87 第 1 步）。格式说明书是仓库根 testdata/rules/README.md
// —— 那份文档是两端的接口说明，服务端的 Go 版（server/internal/rules）读同一批文件，
// 对应它的 fixtures_test.go。
//
// 「严格」是 README 要求的：**不认识的字段一律报错**。所以「加字段」是两端的破坏性改动 ——
// 先改 README，再两端一起改，不要只改一边：老的一端会直接报错，而不是默默忽略。
//
// 这里**不读文件**（JSON 的 import 在 testdata.ts 里），校验逻辑单独放，才好单测 ——
// 读取器的每条拒绝都该有测试盯着（见 fixtures.test.ts）。

const README = "testdata/rules/README.md";

type Json = unknown;

/**
 * 取必填字段。**缺字段和类型不对要分开报**：前者是「牌例少了东西」，后者是「牌例写错了」，
 * 排错时看的信息不一样。
 */
function required(obj: Record<string, Json>, key: string, what: string): Json {
  const value = obj[key];
  if (value === undefined) {
    throw new Error(`${what}：缺字段 ${key}（必填，见 ${README}）`);
  }
  return value;
}

function asObject(value: Json, what: string): Record<string, Json> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${what}：应该是对象`);
  }
  return value as Record<string, Json>;
}

function asArray(value: Json, what: string): Json[] {
  if (!Array.isArray(value)) {
    throw new Error(`${what}：应该是数组`);
  }
  return value;
}

function asString(value: Json, what: string): string {
  if (typeof value !== "string") {
    throw new Error(`${what}：应该是字符串`);
  }
  return value;
}

function asStringList(value: Json, what: string): string[] {
  return asArray(value, what).map((item, i) => asString(item, `${what}[${i}]`));
}

function asStringRows(value: Json, what: string): string[][] {
  return asArray(value, what).map((row, i) => asStringList(row, `${what}[${i}]`));
}

function asIntegerList(value: Json, what: string): number[] {
  return asArray(value, what).map((item, i) => {
    if (typeof item !== "number" || !Number.isInteger(item)) {
      throw new Error(`${what}[${i}]：应该是整数`);
    }
    return item;
  });
}

/**
 * 多出来的字段一律报错（README「两端都必须拒绝未知字段」）。
 * 只查「多没多」，不查「少没少」—— 少了的字段由 `required` 报成「缺字段 X」。
 */
function rejectUnknownKeys(obj: Record<string, Json>, allowed: readonly string[], what: string): void {
  for (const key of Object.keys(obj)) {
    if (!allowed.includes(key)) {
      throw new Error(`${what}：不认识的字段 ${JSON.stringify(key)}（加字段是两端的破坏性改动，先改 README）`);
    }
  }
}

/** `_comment` 只是给人读的说明，两端都不读它：写了就查一下类型，不写也行。 */
function checkComment(obj: Record<string, Json>, what: string): void {
  if (obj._comment !== undefined) {
    asStringList(obj._comment, `${what}._comment`);
  }
}

/**
 * 用例列表。**空数组要拒绝**：牌例被清空时一条用例都不生成，测试照样是绿的，什么都查不出来
 * （Go 端 fixtures_test.go 有同样的「没有用例」检查）。
 */
function requiredVectors(obj: Record<string, Json>, what: string): Json[] {
  const vectors = asArray(required(obj, "vectors", what), `${what}.vectors`);
  if (vectors.length === 0) {
    throw new Error(`${what}：没有用例`);
  }
  return vectors;
}

/**
 * 查一条用例的 `rule` 字段：至少一条、每条非空。写法见 README「条款号的写法」一节。
 *
 * 写错一个字的条款号比不写更糟 —— 看着像有依据，翻原文找不到，所以最终要用正则卡死写法。
 * **但格式校验这里先不加**：#213 第三轮加过一版（`fixtures_test.go` 的 `ruleRe`），
 * 它和 README 的写法对不上 —— README 的 `第四条 二、（一）4` 过不了，`第四条 二、（一）、4`
 * 反而能过。jkt 在改，以 README 为准。等 #213 合入、格式定死之后，这里按**最终版的正则和
 * 它的正反例表**一起加，和 Go 端逐条一致：牌例是两端共用的，一边有一边没有，同一条牌例
 * 就会在一端过、在另一端挂（#213 开 PR 前审计的 S2 说的就是这个）。
 */
function requiredRule(obj: Record<string, Json>, what: string): string[] {
  const rules = asStringList(required(obj, "rule", what), `${what}.rule`);
  if (rules.length === 0) {
    throw new Error(`${what}：没写 rule（条款号），写法见 ${README}`);
  }
  for (const rule of rules) {
    if (rule.trim() === "") {
      throw new Error(`${what}：rule 里有空字符串 —— 要么写条款号，要么整条不要写`);
    }
  }
  return rules;
}

export interface DeckFixture {
  /** 条款号（第二条 5：两副牌共 108 张）。 */
  rule: string[];
  /** 108 张牌的顺序，**下标就是牌的 ID**。 */
  order: string[];
}

export function readDeck(raw: Json): DeckFixture {
  const what = "deck.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "rule", "order"], what);
  checkComment(obj, what);
  return {
    rule: requiredRule(obj, what),
    order: asStringList(required(obj, "order", what), `${what}.order`),
  };
}

export interface PowerVector {
  name: string;
  rule: string[];
  /** 级数，用牌面写："2"…"10"、"J"、"Q"、"K"、"A"。 */
  level: string;
  /** 这个级数下从大到小的 15 个牌点，只写牌面、不带花色。 */
  order: string[];
}

export function readPower(raw: Json): PowerVector[] {
  const what = "power.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "vectors"], what);
  checkComment(obj, what);
  return requiredVectors(obj, what).map((item, i) => {
    const at = `${what}.vectors[${i}]`;
    const vector = asObject(item, at);
    rejectUnknownKeys(vector, ["name", "rule", "level", "order"], at);
    return {
      name: asString(required(vector, "name", at), `${at}.name`),
      rule: requiredRule(vector, at),
      level: asString(required(vector, "level", at), `${at}.level`),
      order: asStringList(required(vector, "order", at), `${at}.order`),
    };
  });
}

export interface DealVector {
  name: string;
  rule: string[];
  /** 108 个数：**第 i 张发出去的牌的 ID**，方向见 README「发牌」。 */
  shuffle: number[];
  /** 四手牌各 27 张；下标 k 是从**首抓者**起逆时针第 k 家。 */
  hands: string[][];
}

export function readDeal(raw: Json): DealVector[] {
  const what = "deal.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "vectors"], what);
  checkComment(obj, what);
  return requiredVectors(obj, what).map((item, i) => {
    const at = `${what}.vectors[${i}]`;
    const vector = asObject(item, at);
    rejectUnknownKeys(vector, ["name", "rule", "shuffle", "hands"], at);
    return {
      name: asString(required(vector, "name", at), `${at}.name`),
      rule: requiredRule(vector, at),
      shuffle: asIntegerList(required(vector, "shuffle", at), `${at}.shuffle`),
      hands: asStringRows(required(vector, "hands", at), `${at}.hands`),
    };
  });
}
