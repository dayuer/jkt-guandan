// testdata/rules/*.json 的**严格**读取器（#87 第 1 步）。格式说明书是仓库根 testdata/rules/README.md
// —— 那份文档是两端的接口说明，服务端的 Go 版（server/internal/rules）读同一批文件，
// 对应它的 fixtures_test.go。
//
// 「严格」是 README 要求的：**不认识的字段一律报错**。所以「加字段」是两端的破坏性改动 ——
// 先改 README，再两端一起改，不要只改一边：老的一端会直接报错，而不是默默忽略。
//
// 这里**不读文件**（JSON 的 import 在 testdata.ts 里），校验逻辑单独放，才好单测 ——
// 读取器的每条拒绝都该有测试盯着（见 fixtures.test.ts）。

import { REJECT_REASONS, type RejectReason } from "../play";

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

function asBoolean(value: Json, what: string): boolean {
  if (typeof value !== "boolean") {
    throw new Error(`${what}：应该是布尔值`);
  }
  return value;
}

function asInteger(value: Json, what: string): number {
  if (typeof value !== "number" || !Number.isInteger(value)) {
    throw new Error(`${what}：应该是整数`);
  }
  return value;
}

function asIntegerList(value: Json, what: string): number[] {
  return asArray(value, what).map((item, i) => asInteger(item, `${what}[${i}]`));
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
 * 条款号的写法（README「条款号的写法」一节，表里六行）。六种：
 *
 *   第X条                 整条
 *   第X条 N               条 + 条目
 *   第X条 甲              条 + 款（整款）
 *   第X条 甲、N           条 + 款 + 条目
 *   第X条 甲、（乙）       只引到「（乙）」
 *   第X条 甲、（乙）N      条 + 款 + 再下一层 + 条目
 *
 * X、甲、乙是中文数字，N 是阿拉伯数字的条目号。款和条目之间是「、」；但写到再下一层时
 * **`（乙）` 后面直接跟条目号，不再多一个「、」** —— `第四条 二、（一）4` 对、
 * `第四条 二、（一）、4` 错（#213 第三轮的正则就是在这儿写错的，TS 端跟着也写错了一版）。
 *
 * **这个字符串和 Go 端 `fixtures_test.go` 的 `ruleRe` 逐字符相同**：牌例是两端共用的，
 * 一边有一边没有、或者写法不一样，同一条牌例就会在一端过、在另一端挂（#213 开 PR 前
 * 审计的 S2 说的就是这个）。改就两边一起改，两边的正反例表也一起跑
 * —— Go 是 `deck_test.go` 的 `TestRuleSyntax`，这边是 fixtures.test.ts。
 */
const RULE_RE =
  /^第[一二三四五六七八九十]+条( [一二三四五六七八九十]+(、（[一二三四五六七八九十]+）[0-9]*|、[0-9]+)?| [0-9]+)?$/;

/**
 * 查一条用例的 `rule` 字段：至少一条、每条非空、写法对。
 *
 * 写错一个字的条款号比不写更糟 —— 看着像有依据，翻原文找不到。
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
    if (!RULE_RE.test(rule)) {
      throw new Error(`${what}：条款号 ${JSON.stringify(rule)} 的写法不对（见 ${README} 的「条款号的写法」）`);
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

/** 牌例里声明的一种顶法：哪张逢人配顶成了什么。`as` 不带副号（README「逢人配」第 3 条）。 */
export interface WildSpec {
  card: string;
  as: string;
}

/** 一种解读：牌型名 + 当这个牌型打时的牌面 + （可选）哪张逢人配顶成了什么。 */
export interface ComboSpec {
  kind: string;
  rank: string;
  /**
   * 可选。写了就要求实现挑的**正是这一种顶法**（README「逢人配」第 1 条）：
   * 同一种（牌型 + 牌面）有好几种顶法时两端要给出同一个答案，所以留一种、按顶成的牌
   * 「点数、花色」最小那种。大多数用例的顶法是唯一的，没必要每条都写。
   */
  wilds?: WildSpec[];
}

export interface CombosVector {
  name: string;
  rule: string[];
  /** 级数，同 power.json。 */
  level: string;
  /** 这一手牌（顺序无所谓）。 */
  cards: string[];
  /** **全部**合法解读，**排好序**（先按 kind 在 README 那张表里从上到下，再按 rank 从小到大）。空数组表示什么牌型都不是。 */
  combos: ComboSpec[];
}

function readWildSpec(value: Json, what: string): WildSpec {
  const spec = asObject(value, what);
  rejectUnknownKeys(spec, ["card", "as"], what);
  return {
    card: asString(required(spec, "card", what), `${what}.card`),
    as: asString(required(spec, "as", what), `${what}.as`),
  };
}

function readComboSpec(value: Json, what: string): ComboSpec {
  const spec = asObject(value, what);
  rejectUnknownKeys(spec, ["kind", "rank", "wilds"], what);
  const out: ComboSpec = {
    kind: asString(required(spec, "kind", what), `${what}.kind`),
    rank: asString(required(spec, "rank", what), `${what}.rank`),
  };
  if (spec.wilds !== undefined) {
    const wilds = asArray(spec.wilds, `${what}.wilds`).map((item, i) => readWildSpec(item, `${what}.wilds[${i}]`));
    if (wilds.length > 0) out.wilds = wilds; // 空数组等于没写
  }
  return out;
}

export function readCombos(raw: Json): CombosVector[] {
  const what = "combos.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "vectors"], what);
  checkComment(obj, what);
  return requiredVectors(obj, what).map((item, i) => {
    const at = `${what}.vectors[${i}]`;
    const vector = asObject(item, at);
    rejectUnknownKeys(vector, ["name", "rule", "level", "cards", "combos"], at);
    return {
      name: asString(required(vector, "name", at), `${at}.name`),
      rule: requiredRule(vector, at),
      level: asString(required(vector, "level", at), `${at}.level`),
      cards: asStringList(required(vector, "cards", at), `${at}.cards`),
      combos: asArray(required(vector, "combos", at), `${at}.combos`).map((spec, j) =>
        readComboSpec(spec, `${at}.combos[${j}]`),
      ),
    };
  });
}

/** 比较的一边：声明的牌型 + 完整的一手牌。 */
export interface CompareSide {
  kind: string;
  cards: string[];
  /**
   * 可选：声明的牌面。同一种牌型有好几种牌面时必须写（比如带逢人配的那几张，顶法不同点数不同）；
   * 不写就表示这一手牌在那个牌型下只有一种解读。
   *
   * **空串等于没写**（和 Go 端 `if side.Rank != ""` 一个口径）：`"rank": ""` 不当成「声明的牌面
   * 是空」，不然同一份牌例在两端一头过一头挂。
   */
  rank?: string;
}

export interface CompareVector {
  name: string;
  rule: string[];
  level: string;
  a: CompareSide;
  b: CompareSide;
  /** `a` / `b` / `equal` / `incomparable`。两个方向都会断言。 */
  result: string;
}

function readCompareSide(value: Json, what: string): CompareSide {
  const side = asObject(value, what);
  rejectUnknownKeys(side, ["kind", "cards", "rank"], what);
  const out: CompareSide = {
    kind: asString(required(side, "kind", what), `${what}.kind`),
    cards: asStringList(required(side, "cards", what), `${what}.cards`),
  };
  if (side.rank !== undefined) {
    const rank = asString(side.rank, `${what}.rank`);
    if (rank !== "") out.rank = rank; // 空串等于没写（和 Go 一个口径）
  }
  return out;
}

export function readCompare(raw: Json): CompareVector[] {
  const what = "compare.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "vectors"], what);
  checkComment(obj, what);
  return requiredVectors(obj, what).map((item, i) => {
    const at = `${what}.vectors[${i}]`;
    const vector = asObject(item, at);
    rejectUnknownKeys(vector, ["name", "rule", "level", "a", "b", "result"], at);
    return {
      name: asString(required(vector, "name", at), `${at}.name`),
      rule: requiredRule(vector, at),
      level: asString(required(vector, "level", at), `${at}.level`),
      a: readCompareSide(required(vector, "a", at), `${at}.a`),
      b: readCompareSide(required(vector, "b", at), `${at}.b`),
      result: asString(required(vector, "result", at), `${at}.result`),
    };
  });
}

/** 一手出法：牌型 + 牌面 + 张数 + （可选）哪张逢人配顶成了什么。 */
export interface MoveSpec {
  kind: string;
  rank: string;
  /** 这一手打出去几张。炸弹 4–10 张不固定，其余牌型的 size 是定死的。 */
  size: number;
  wilds?: WildSpec[];
}

export interface MovesVector {
  name: string;
  rule: string[];
  level: string;
  hand: string[];
  /** **上一手**，写法同 `compare.json` 的 a／b。不写就是领出。 */
  lead?: CompareSide;
  /** 能打出去的全部打法，**排好序**；跟牌时只列能压住 lead 的。空数组表示要不起。 */
  moves: MoveSpec[];
}

function readMoveSpec(value: Json, what: string): MoveSpec {
  const spec = asObject(value, what);
  rejectUnknownKeys(spec, ["kind", "rank", "size", "wilds"], what);
  const out: MoveSpec = {
    kind: asString(required(spec, "kind", what), `${what}.kind`),
    rank: asString(required(spec, "rank", what), `${what}.rank`),
    size: asInteger(required(spec, "size", what), `${what}.size`),
  };
  if (spec.wilds !== undefined) {
    const wilds = asArray(spec.wilds, `${what}.wilds`).map((item, i) => readWildSpec(item, `${what}.wilds[${i}]`));
    if (wilds.length > 0) out.wilds = wilds;
  }
  return out;
}

export function readMoves(raw: Json): MovesVector[] {
  const what = "moves.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "vectors"], what);
  checkComment(obj, what);
  return requiredVectors(obj, what).map((item, i) => {
    const at = `${what}.vectors[${i}]`;
    const vector = asObject(item, at);
    rejectUnknownKeys(vector, ["name", "rule", "level", "hand", "lead", "moves"], at);
    const out: MovesVector = {
      name: asString(required(vector, "name", at), `${at}.name`),
      rule: requiredRule(vector, at),
      level: asString(required(vector, "level", at), `${at}.level`),
      hand: asStringList(required(vector, "hand", at), `${at}.hand`),
      moves: asArray(required(vector, "moves", at), `${at}.moves`).map((spec, j) =>
        readMoveSpec(spec, `${at}.moves[${j}]`),
      ),
    };
    if (vector.lead !== undefined) out.lead = readCompareSide(vector.lead, `${at}.lead`);
    return out;
  });
}

/** 某一刻的局面（`steps[].after` 和 `expect` 同一套字段）。字段名照 JSON 写，一眼看得出对应。 */
export interface PlayState {
  /** 轮到谁；0 = 这一副结束。 */
  turn?: number;
  leading?: boolean;
  /** 这一圈最近出牌的人；领出时是 `null`。 */
  last_seat?: number | null;
  last_kind?: string;
  last_rank?: string;
  finished?: number[];
  result?: { order: number[]; head_seat: number; partner_place: number };
}

export interface PlayStep {
  seat: number;
  pass?: boolean;
  cards?: string[];
  kind?: string;
  rank?: string;
  /** 这一步**成功之后**的局面（只断言写了的字段）。和 `reject` 互斥。 */
  after?: PlayState;
  /** 这一步**必须被拒**，值是封闭取值之一（见 README「play.json」那一节的表）。 */
  reject?: RejectReason;
}

export interface PlayVector {
  name: string;
  rule: string[];
  level: string;
  /** 首圈领出的座位号 1–4。 */
  leader: number;
  /** **四家**手牌，下标 0–3 就是座位 1–4（和 `deal.json` 不一样）。 */
  hands: string[][];
  steps: PlayStep[];
  expect: PlayState;
}

function readPlayState(value: Json, what: string): PlayState {
  const state = asObject(value, what);
  rejectUnknownKeys(state, ["turn", "leading", "last_seat", "last_kind", "last_rank", "finished", "result"], what);
  const out: PlayState = {};
  if (state.turn !== undefined) out.turn = asInteger(state.turn, `${what}.turn`);
  if (state.leading !== undefined) out.leading = asBoolean(state.leading, `${what}.leading`);
  if (state.last_seat !== undefined) {
    out.last_seat = state.last_seat === null ? null : asInteger(state.last_seat, `${what}.last_seat`);
  }
  if (state.last_kind !== undefined) out.last_kind = asString(state.last_kind, `${what}.last_kind`);
  if (state.last_rank !== undefined) out.last_rank = asString(state.last_rank, `${what}.last_rank`);
  if (state.finished !== undefined) out.finished = asIntegerList(state.finished, `${what}.finished`);
  if (state.result !== undefined) {
    const at = `${what}.result`;
    const result = asObject(state.result, at);
    rejectUnknownKeys(result, ["order", "head_seat", "partner_place"], at);
    out.result = {
      order: asIntegerList(required(result, "order", at), `${at}.order`),
      head_seat: asInteger(required(result, "head_seat", at), `${at}.head_seat`),
      partner_place: asInteger(required(result, "partner_place", at), `${at}.partner_place`),
    };
  }
  return out;
}

function readPlayStep(value: Json, what: string): PlayStep {
  const step = asObject(value, what);
  rejectUnknownKeys(step, ["seat", "pass", "cards", "kind", "rank", "after", "reject"], what);
  const out: PlayStep = { seat: asInteger(required(step, "seat", what), `${what}.seat`) };
  if (step.pass !== undefined) out.pass = asBoolean(step.pass, `${what}.pass`);
  if (step.cards !== undefined) out.cards = asStringList(step.cards, `${what}.cards`);
  if (step.kind !== undefined) out.kind = asString(step.kind, `${what}.kind`);
  if (step.rank !== undefined) out.rank = asString(step.rank, `${what}.rank`);
  if (step.after !== undefined) out.after = readPlayState(step.after, `${what}.after`);
  if (step.reject !== undefined) {
    const reason = asString(step.reject, `${what}.reject`);
    if (!(REJECT_REASONS as readonly string[]).includes(reason)) {
      throw new Error(`${what}.reject：${JSON.stringify(reason)} 不是封闭取值之一（${REJECT_REASONS.join(" / ")}）`);
    }
    out.reject = reason as RejectReason;
  }
  if (out.reject !== undefined && out.after !== undefined) {
    throw new Error(`${what}：reject 和 after 互斥（被拒的那一步局面不变，不需要 after）`);
  }
  return out;
}

export function readPlay(raw: Json): PlayVector[] {
  const what = "play.json";
  const obj = asObject(raw, what);
  rejectUnknownKeys(obj, ["_comment", "vectors"], what);
  checkComment(obj, what);
  return requiredVectors(obj, what).map((item, i) => {
    const at = `${what}.vectors[${i}]`;
    const vector = asObject(item, at);
    rejectUnknownKeys(vector, ["name", "rule", "level", "leader", "hands", "steps", "expect"], at);
    const hands = asArray(required(vector, "hands", at), `${at}.hands`).map((hand, j) =>
      asStringList(hand, `${at}.hands[${j}]`),
    );
    if (hands.length !== 4) throw new Error(`${at}.hands：要四家，写的是 ${hands.length} 家`);
    return {
      name: asString(required(vector, "name", at), `${at}.name`),
      rule: requiredRule(vector, at),
      level: asString(required(vector, "level", at), `${at}.level`),
      leader: asInteger(required(vector, "leader", at), `${at}.leader`),
      hands,
      steps: asArray(required(vector, "steps", at), `${at}.steps`).map((step, j) =>
        readPlayStep(step, `${at}.steps[${j}]`),
      ),
      expect: readPlayState(required(vector, "expect", at), `${at}.expect`),
    };
  });
}
