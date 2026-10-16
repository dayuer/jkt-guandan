// 合法出法（#87 第 4 步）。规则口径按 2022《竞技掼蛋竞赛规则（试行）》，条款号写在旁边；
// 对照用例见仓库根 testdata/rules/moves.json（格式见那里的 README.md）。
//
// 和服务端的 server/internal/rules/{moves,moves_gen}.go 是同一套语义，两边对着改。
//
// 每一步要回答两个问题：
//
//   - **领出**：手里的牌能打出哪些牌型？（第三条 1–10 的全部合法解读）
//   - **跟牌**：上一手是谁的什么牌，我能不能压住？能压住的都有哪些打法？
//
// 做法是「按牌型造候选 → 交给 `recognize` 认一遍」：先按牌型在手里挑出**可能的牌组**
// （每个候选都是一小组具体的牌，≤ 10 张），再交回 `recognize` 得出解读（含逢人配的顶法枚举）。
// **不为出法另写一套判牌型的代码** —— 口径不会跑偏。
//
// 为什么不直接枚举子集：27 张手牌的子集有 2^27 个，光是 5 张以内的组合就有十几万，每走一步
// 都跑一遍不现实。按牌型造候选把规模压到几百个。
import {
  ACE,
  BIG_JOKER,
  CLUB,
  DIAMOND,
  HEART,
  rankName,
  SMALL_JOKER,
  SPADE,
  TWO,
  type Card,
  type Rank,
  type Suit,
} from "./card";
import {
  asKey,
  checkDistinctCards,
  COMBO_TYPES,
  compare,
  isBomb,
  recognize,
  sortCards,
  type Combo,
  type ComboType,
} from "./combo";
import { isWild } from "./power";

/**
 * 列出一手牌的合法出法。
 *
 * `lead` 不给表示**领出**（列出全部能打的牌型）；给了表示**跟牌**，只留能压住它的
 * （第四条 二：同牌型同张数比大小；炸弹、同花顺、四大天王压普通牌型；炸弹之间按档次和张数）。
 *
 * 每一手都是**具体的牌**（`cards` 是打出去的牌、`wilds` 是逢人配顶成了什么），调用方拿起来
 * 就能打出去。同一个（牌型、牌面、张数、顶法）只留一套牌 —— 取牌 ID 最小的那一套，
 * 所以两端的集合能逐条比（见 testdata/rules/moves.json）。
 *
 * **「要不起就过」不在这个集合里**：跟牌时过牌永远合法，由调用方自己加。
 */
export function moves(hand: readonly Card[], level: Rank, lead?: Combo): Combo[] {
  if (hand.length === 0) throw new Error("求合法出法：手里没有牌");
  if (level < TWO || level > ACE) throw new Error(`求合法出法：级数 ${rankName(level)} 超出 2–A`);
  checkDistinctCards(hand);
  if (lead && lead.cards.length === 0) throw new Error("求合法出法：上一手没有牌");

  const played = sortCards(hand);
  const { wilds, solids } = splitWilds(played, level);

  // 跟牌时只造「压得住上一手」的牌型：要不起的牌型不用生成（跟牌是最常见的路径）。
  const want = lead ? shapesBeating(lead) : allShapes();

  // 每个（牌型、牌面、张数、顶法）只留牌 ID 最小的那一套。
  const best = new Map<string, Combo>();
  for (const set of candidateSets(solids, wilds, want)) {
    for (const combo of recognize(set, level)) {
      const key = comboKey(combo);
      const old = best.get(key);
      if (!old || lessCards(combo.cards, old.cards)) best.set(key, combo);
    }
  }

  const out: Combo[] = [];
  for (const combo of best.values()) {
    if (lead && compare(combo, lead) !== "greater") continue;
    out.push(combo);
  }
  return sortMoves(out);
}

/** 手里几张牌能凑成一手：点数 + 要几张。 */
interface Slot {
  rank: Rank;
  n: number;
}

/** 「要生成哪些牌型」的开关。领出时全开；跟牌时只开需要的那些。 */
type ShapeMask = Set<ComboType>;

function allShapes(): ShapeMask {
  return new Set(COMBO_TYPES);
}

/**
 * 能压住 `lead` 的牌型：同牌型（跟牌要么同牌型同张数，要么用炸弹），加上炸弹、同花顺、
 * 四大天王（第四条 二、（二））。跟牌时只生成这些 —— 这一下省掉一大半工作，而跟牌是最常见的路径。
 */
function shapesBeating(lead: Combo): ShapeMask {
  const mask: ShapeMask = new Set<ComboType>(["bomb", "straight-flush", "joker-bomb"]);
  if (!isBomb(lead)) mask.add(lead.type);
  return mask;
}

// ---------------------------------------------------------------------------
// 造候选（逐牌型的槽位）
// ---------------------------------------------------------------------------

/**
 * 按牌型造候选牌组。每个候选都是「手里可能打得出去的一小组牌」，交给 `recognize` 认解读 ——
 * 这里只负责**挑牌**，不判合不合法（那是 `recognize` 的事）。
 *
 * 逐牌型的槽位（第三条 1–10）：
 *
 *   单张   [X,1] × 手里每个点数
 *   对子   [X,2]
 *   三同张 [X,3]
 *   炸弹   [X,n] n=4..10（同一点数最多 8 张 + 2 张逢人配）
 *   三带对 [A,3] + [B,2]，A≠B
 *   三连对 [X,2]+[X+1,2]+[X+2,2]（含 A 当 1 的绕圈窗口）
 *   三同连张 [X,3]+[X+1,3]
 *   顺子   [X,1]…[X+4,1]
 *   同花顺 同上，但只从同一个花色里挑
 *   四大天王 大小王各两张（逢人配顶不了王，所以只从实打实的牌里挑）
 *
 * 一组槽位不止一种挑法：同样的 [9,2]（凑一对 9）可以两张实牌，也可以一张实牌 + 一张逢人配 ——
 * 后者是**另一种打法**（用了逢人配），两头都要给出来。所以下面把逢人配的每一种取用方式都试一遍。
 */
function candidateSets(solids: readonly Card[], wilds: readonly Card[], want: ShapeMask): Card[][] {
  const byRank = new Map<Rank, Card[]>();
  for (const card of solids) {
    const list = byRank.get(card.rank);
    if (list) list.push(card);
    else byRank.set(card.rank, [card]);
  }
  // 同一个点数的实牌按 **ID** 排（不是按花色）—— 挑牌时取 ID 最小的那几张，这样「同一个打法
  // 留哪一套牌」的答案和 `moves` 的说明一致，也和暴力枚举对拍的结果一样。
  for (const list of byRank.values()) list.sort((x, y) => x.id - y.id);
  const ranks = [...byRank.keys()].sort((x, y) => x - y);

  // 逢人配自己也是牌：一张单出、两张一起出（只按它本来的点数算 —— 和 `recognize` 一个口径）。
  // 手里只有逢人配、没有实牌时，候选就这些。
  const out: Card[][] = [];
  for (const wild of wilds) out.push([wild]);
  if (wilds.length >= 2) out.push([wilds[0], wilds[1]]);
  if (ranks.length === 0) return out;

  // 槽位的点数各不相同时（顺子类）和槽位点数可能重复时（三带对），挑法用的是同一段代码 ——
  // 区别在槽位本身，不在怎么挑。
  const addSlots = (...slots: Slot[]): void => {
    out.push(...slotVariants(slots, byRank, wilds));
  };

  // 单张、对子、三同张、炸弹（含用逢人配补齐的同点数牌）。
  for (const rank of ranks) {
    for (const { type, n } of [
      { type: "single", n: 1 },
      { type: "pair", n: 2 },
      { type: "triple", n: 3 },
    ] as const) {
      if (want.has(type)) addSlots({ rank, n });
    }
    if (want.has("bomb")) {
      for (let n = 4; n <= 10; n++) addSlots({ rank, n });
    }
  }

  // 四大天王：大小王各两张。逢人配顶不了王，这里不试「用逢人配」那种取法。
  if (want.has("joker-bomb")) {
    const set = fillSlots([{ rank: BIG_JOKER, n: 2 }, { rank: SMALL_JOKER, n: 2 }], byRank, [], [0, 0]);
    if (set) out.push(set);
  }

  // 三带对：一个三同张 + 另一个点数的对子。
  if (want.has("triple-pair")) {
    for (const a of ranks) {
      for (const b of ranks) {
        if (a !== b) addSlots({ rank: a, n: 3 }, { rank: b, n: 2 });
      }
    }
  }

  // 三连对、三同连张、顺子、同花顺：按窗口挑。
  if (want.has("tube")) {
    for (const window of rankWindows(3)) addSlots(...window.map((rank) => ({ rank, n: 2 })));
  }
  if (want.has("plate")) {
    for (const window of rankWindows(2)) addSlots(...window.map((rank) => ({ rank, n: 3 })));
  }
  if (want.has("straight") || want.has("straight-flush")) {
    for (const window of rankWindows(5)) {
      const slots = window.map((rank) => ({ rank, n: 1 }));
      if (want.has("straight")) addSlots(...slots);
      // 同花顺：同一个窗口，四个花色各挑一遍。
      if (want.has("straight-flush")) {
        for (const suit of [SPADE, HEART, CLUB, DIAMOND] as const) out.push(...slotVariants(slots, byRank, wilds, suit));
      }
    }
  }
  return out;
}

/**
 * 枚举「同点数」牌型的每一种打法：逢人配的每个子集 × 每张逢人配补到哪个槽位。
 *
 * 为什么要把「用几张逢人配、补到哪个槽位」都枚举一遍：同样凑一对 9，可以是两张实牌 9，
 * 也可以是「一张 9 + 一张逢人配当 9」；三带对更是可以「逢人配当三同张那一档」或者「当对子
 * 那一档」—— 这些都是**不同的打法**（用的牌不同、`wilds` 也不同），牌例里要逐条列出来。
 *
 * 槽位最多 2 个（三带对）、逢人配最多 2 张，所以规模很小。
 */
function slotVariants(
  slots: readonly Slot[],
  byRank: Map<Rank, Card[]>,
  wilds: readonly Card[],
  suit?: Suit,
): Card[][] {
  const out: Card[][] = [];
  for (let mask = 0; mask < 1 << wilds.length; mask++) {
    const chosen = wilds.filter((_, i) => (mask & (1 << i)) !== 0);
    for (const counts of assignments(chosen.length, slots)) {
      const set = fillSlots(slots, byRank, chosen, counts, suit);
      if (set) out.push(set);
    }
  }
  return out;
}

/** 把 n 张逢人配分到每个槽位（每槽最多和它要的张数一样多），列出全部分法。 */
function assignments(n: number, slots: readonly Slot[]): number[][] {
  if (slots.length === 0) return n === 0 ? [[]] : [];
  const out: number[][] = [];
  const head = slots[0];
  for (let k = 0; k <= n && k <= head.n; k++) {
    for (const rest of assignments(n - k, slots.slice(1))) out.push([k, ...rest]);
  }
  return out;
}

/**
 * 按槽位挑牌：第 i 个槽位先放 `counts[i]` 张给定的逢人配（它们是这次打法点名要用的），
 * 剩下的用手里这个点数的实牌补（按 ID 从小到大、够用就停）。同一张牌不能用两次；
 * 凑不齐返回 `undefined`；`wilds` 里的逢人配没用完也算凑不齐（那是另一种打法，由别的
 * `counts` 表示）。
 */
function fillSlots(
  slots: readonly Slot[],
  byRank: Map<Rank, Card[]>,
  wilds: readonly Card[],
  counts: readonly number[],
  suit?: Suit,
): Card[] | undefined {
  const used = new Set<number>();
  const out: Card[] = [];
  let next = 0; // 下一张还没用的逢人配
  for (let i = 0; i < slots.length; i++) {
    let need = slots[i].n;
    for (let k = 0; k < counts[i]; k++) {
      if (next >= wilds.length) return undefined;
      const wild = wilds[next++];
      used.add(wild.id);
      out.push(wild);
      need--;
    }
    for (const card of byRank.get(slots[i].rank) ?? []) {
      if (need === 0) break;
      if (used.has(card.id)) continue;
      if (suit !== undefined && card.suit !== suit) continue;
      used.add(card.id);
      out.push(card);
      need--;
    }
    if (need > 0) return undefined; // 这个点数凑不齐（实牌不够，逢人配也用完了）
  }
  if (next !== wilds.length) return undefined; // 还有逢人配没地方放
  return sortCards(out);
}

/**
 * 生成 `count` 个连续点数的窗口（第三条 7 那些），含「A 当 1」绕一圈的：
 * count=5 时是 23456…10JQKA 共 10 个，再加上 A2345；count=3 时是 234…QKA 再加上 A23。
 */
function rankWindows(count: number): Rank[][] {
  const out: Rank[][] = [];
  for (let start = TWO; start + count - 1 <= ACE; start++) {
    out.push(Array.from({ length: count }, (_, i) => start + i));
  }
  // A 当 1：A + 2…count（count=2 时是 A2 —— 三同连张 AA222 在规则里是有的）。
  if (count >= 2) {
    const window: Rank[] = [ACE];
    for (let v = TWO; v <= count; v++) window.push(v);
    out.push(window);
  }
  return out;
}

// ---------------------------------------------------------------------------
// 去重和排序
// ---------------------------------------------------------------------------

/**
 * 「同一个打法」的判据：牌型 + 牌面 + **张数** + 顶法（顶法只写**顶成了什么**，不写「用的是哪张」）。
 *
 * 张数也要算进去：炸弹 4 张、5 张、7 张是**三种打法**（比大小的档次都不一样），少了这一项
 * 它们会被当成同一条、只留下最小的那种 —— 手里七个 3 就只剩「4 张炸弹」，跟别人 5 张炸弹时
 * 明明有 6 张、7 张能压，却报「无牌可跟」（#204 第 4 步踩过这个坑）。
 *
 * 顶法里**不带「用的是哪张逢人配」**（不带副号、也不带 ID），和「不比具体用了哪几张牌」是
 * 同一条原则：手里两张逢人配（♥7/0、♥7/1）单出，都是「单张级牌」**一个打法**，列一条就够，
 * 用哪一张由实现取 ID 最小的那套。带上它的话，牌例会并排出现两行「只差一个副号」的记录 ——
 * 而 README 定的比对规矩是「顶法里的逢人配只比点数花色」，那两行本来就相等（#237 复审定的口径）。
 */
export function comboKey(combo: Combo): string {
  let key = `${combo.type}/${rankName(combo.rank)}/${combo.cards.length}`;
  for (const wild of combo.wilds) key += `/${asKey(wild.as)}`;
  return key;
}

/**
 * 比两套牌，用来在同一个打法里挑「牌 ID 最小的那一套」。
 *
 * ⚠️ 比的是**按 ID 排好之后**的列表，不是 `cards` 原本的顺序（那是按点数、花色排的）：
 * 不然「最小的那一套」会取决于排序习惯 —— 同样是对子 Q，`♣Q/0 + ♥Q/1`（ID 37、78）和
 * `♣Q/0 + ♣Q/1`（ID 37、91）哪个小，两种排法给出的答案不一样。
 */
function lessCards(a: readonly Card[], b: readonly Card[]): boolean {
  const [ai, bi] = [idsAsc(a), idsAsc(b)];
  for (let i = 0; i < ai.length; i++) {
    if (i >= bi.length) return false;
    if (ai[i] !== bi[i]) return ai[i] < bi[i];
  }
  return ai.length < bi.length;
}

function idsAsc(cards: readonly Card[]): number[] {
  return cards.map((card) => card.id).sort((x, y) => x - y);
}

/**
 * 排出确定的顺序：先牌型（README 那张表从上到下）、再牌面、再顶法、最后按牌 ID。
 * 两端比对不能靠 `Map` 的遍历顺序。
 */
function sortMoves(combos: Combo[]): Combo[] {
  return combos.sort((a, b) => {
    const byType = COMBO_TYPES.indexOf(a.type) - COMBO_TYPES.indexOf(b.type);
    if (byType !== 0) return byType;
    if (a.rank !== b.rank) return a.rank - b.rank;
    if (a.wilds.length !== b.wilds.length) return a.wilds.length - b.wilds.length;
    for (let k = 0; k < a.wilds.length; k++) {
      const [wa, wb] = [a.wilds[k], b.wilds[k]];
      if (wa.as.rank !== wb.as.rank) return wa.as.rank - wb.as.rank;
      if (wa.as.suit !== wb.as.suit) return wa.as.suit - wb.as.suit;
      if (wa.card.id !== wb.card.id) return wa.card.id - wb.card.id;
    }
    if (lessCards(a.cards, b.cards)) return -1;
    if (lessCards(b.cards, a.cards)) return 1;
    return 0;
  });
}

/** 把手牌分成逢人配和实打实的牌（和 `recognize` 里同一套切法）。 */
function splitWilds(cards: readonly Card[], level: Rank): { wilds: Card[]; solids: Card[] } {
  const wilds: Card[] = [];
  const solids: Card[] = [];
  for (const card of cards) (isWild(card, level) ? wilds : solids).push(card);
  return { wilds, solids };
}
