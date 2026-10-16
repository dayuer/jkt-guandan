// 读仓库根 testdata/rules/ 的牌例（#87 第 1 步）。格式说明书是那里的 README.md ——
// 那份文档是**两端的接口说明**，服务端的 Go 版（server/internal/rules）读同一批文件，
// 它那边对应的是 fixtures_test.go。
//
// 按 README 的要求做**严格**校验：不认识的字段一律报错。所以「加字段」是两端的破坏性改动：
// 先改 README，再两端一起改，不要只改一边 —— 老的一端会直接报错，而不是默默忽略。
//
// 只在测试里用。
import dealRaw from "../../../testdata/rules/deal.json";
import deckRaw from "../../../testdata/rules/deck.json";
import powerRaw from "../../../testdata/rules/power.json";

type Json = unknown;

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
 * 只查「多没多」，不查「少没少」：少了的话后面读它时会因为类型不对而当场报错。
 */
function rejectUnknownKeys(obj: Record<string, Json>, allowed: readonly string[], what: string): void {
  for (const key of Object.keys(obj)) {
    if (!allowed.includes(key)) {
      throw new Error(`${what}：不认识的字段 ${JSON.stringify(key)}（加字段是两端的破坏性改动，先改 README）`);
    }
  }
}

/** 每条用例都要写依据的条款号（#213 复审定的格式）。 */
function requiredRule(rule: string[], what: string): string[] {
  if (rule.length === 0) {
    throw new Error(`${what}：没写 rule（条款号），写法见 testdata/rules/README.md`);
  }
  return rule;
}

export interface DeckFixture {
  /** 条款号（第二条 5：两副牌共 108 张）。 */
  rule: string[];
  /** 108 张牌的顺序，**下标就是牌的 ID**。 */
  order: string[];
}

export function readDeck(raw: Json): DeckFixture {
  const obj = asObject(raw, "deck.json");
  rejectUnknownKeys(obj, ["_comment", "rule", "order"], "deck.json");
  return {
    rule: requiredRule(asStringList(obj.rule, "deck.json.rule"), "deck.json"),
    order: asStringList(obj.order, "deck.json.order"),
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
  const obj = asObject(raw, "power.json");
  rejectUnknownKeys(obj, ["_comment", "vectors"], "power.json");
  return asArray(obj.vectors, "power.json.vectors").map((item, i) => {
    const what = `power.json.vectors[${i}]`;
    const v = asObject(item, what);
    rejectUnknownKeys(v, ["name", "rule", "level", "order"], what);
    return {
      name: asString(v.name, `${what}.name`),
      rule: requiredRule(asStringList(v.rule, `${what}.rule`), what),
      level: asString(v.level, `${what}.level`),
      order: asStringList(v.order, `${what}.order`),
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
  const obj = asObject(raw, "deal.json");
  rejectUnknownKeys(obj, ["_comment", "vectors"], "deal.json");
  return asArray(obj.vectors, "deal.json.vectors").map((item, i) => {
    const what = `deal.json.vectors[${i}]`;
    const v = asObject(item, what);
    rejectUnknownKeys(v, ["name", "rule", "shuffle", "hands"], what);
    return {
      name: asString(v.name, `${what}.name`),
      rule: requiredRule(asStringList(v.rule, `${what}.rule`), what),
      shuffle: asIntegerList(v.shuffle, `${what}.shuffle`),
      hands: asStringRows(v.hands, `${what}.hands`),
    };
  });
}

/** `_comment` 只是给人读的说明，两端都不读它。 */
export const deckFixture = readDeck(deckRaw);
export const powerFixture = readPower(powerRaw);
export const dealFixture = readDeal(dealRaw);
