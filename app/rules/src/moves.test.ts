// 合法出法的单测和性质测试。牌例那份（两端共用）在 testdata.test.ts；这里管牌例没覆盖到的
// 边界，以及**和暴力枚举对拍** —— 那条是 #204 第 4 步验证过、抓出过真 bug 的（见下面）。
import { describe, expect, it } from "vitest";
import { SEVEN, TWO, type Card, type Rank } from "./card";
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
    // 两张逢人配时 `single 7` 只有**一条**：单出 ♥7/0 和单出 ♥7/1 是同一个打法（去重键里
    // 不带「用的是哪张」），用哪张由实现取 ID 最小的那套 —— 和「不比具体用了哪几张牌」一致。
    // 这一条是 #237 复审定的口径，`moves.json` 里补了用例钉死。
    expect(listed(moves(handOf(["♥7/0", "♥7/1"]), SEVEN))).toEqual(["single 7", "pair 7"]);
  });

  it("同一个打法只留一套牌：两张实牌 9 和「一张 9 + 逢人配当 9」是两种打法，各留一条", () => {
    const pairs = moves(handOf(["♠9/0", "♥9/0", "♥7/0"]), SEVEN).filter((c) => c.type === "pair");
    expect(pairs.map((c) => c.wilds.length).sort()).toEqual([0, 1]);
  });

  // 顺子类的窗口要含「A 当 1」那一圈（第四条 一、2）。这条是变异测试逼出来的：
  // 把 `rankWindows` 里那个 A 开头的窗口删掉，280 条测试**一条都不红** —— 随机手牌太小，
  // 抽不到 A2345 这种组合。所以这里点名补上，别让它再漏。
  it("顺子类的窗口含「A 当 1」：A2345、AA2233、AAA222 都要能列出来", () => {
    expect(listed(moves(handOf(["♠A/0", "♥2/0", "♣3/0", "♦4/0", "♠5/0"]), SEVEN))).toContain("straight 5");
    expect(listed(moves(handOf(["♠A/0", "♥A/0", "♠2/0", "♥2/0", "♠3/0", "♥3/0"]), SEVEN))).toContain("tube 3");
    expect(listed(moves(handOf(["♠A/0", "♦A/0", "♣A/0", "♠2/0", "♦2/0", "♣2/0"]), SEVEN))).toContain("plate 2");
  });

  it("逢人配能补进「A 当 1」那个窗口", () => {
    // ♠A ♣3 ♦4 ♠5 + ♥7 当 ♥2 → A2345
    expect(listed(moves(handOf(["♠A/0", "♣3/0", "♦4/0", "♠5/0", "♥7/0"]), SEVEN))).toContain("straight 5");
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

  // #238 复审发现的：排序是 牌型 → 牌面 → **张数** → 顶法，张数漏了的话「用了几张逢人配」
  // 会插到张数前面，把 4 张和 5 张交错开（变成 4、5、4w、5w，而应该是 4、4w、5、5w）。
  it("炸弹按张数排，同张数的再按用了几张逢人配", () => {
    const hand = ["♠3/0", "♥3/0", "♣3/0", "♦3/0", "♠3/1", "♥3/1", "♣3/1", "♥7/0"];
    const bombs = moves(handOf(hand), SEVEN).filter((c) => c.type === "bomb");
    expect(bombs.map((c) => `${c.cards.length}张${c.wilds.length}配`)).toEqual([
      "4张0配",
      "4张1配",
      "5张0配",
      "5张1配",
      "6张0配",
      "6张1配",
      "7张0配",
      "7张1配",
      "8张1配",
    ]);
  });

  it("四大天王的牌面是空串（README：它没有点数）", () => {
    const jokers = moves(handOf(["大王/0", "大王/1", "小王/0", "小王/1"]), SEVEN);
    const jokerBomb = jokers.find((c) => c.type === "joker-bomb");
    expect(jokerBomb).toBeDefined();
    expect(comboRankName(jokerBomb!)).toBe("");
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

/**
 * 对拍用的手牌：**点名几手「A 当 1」的**（随机手牌太小，抽不到 A2345 这种组合 ——
 * 变异测试验过：把那个窗口删掉，光靠随机手牌一条都不红），再加一批固定种子的随机手牌。
 * 都 ≤ 8 张，才好穷举子集（256 个）。
 */
const SMALL: { cards: Card[]; level: Rank }[] = [
  { cards: handOf(["♠A/0", "♥2/0", "♣3/0", "♦4/0", "♠5/0"]), level: SEVEN }, // A2345
  { cards: handOf(["♠A/0", "♥A/0", "♠2/0", "♥2/0", "♠3/0", "♥3/0"]), level: SEVEN }, // AA2233
  { cards: handOf(["♠A/0", "♦A/0", "♣A/0", "♠2/0", "♦2/0", "♣2/0"]), level: SEVEN }, // AAA222
  { cards: handOf(["♠A/0", "♣3/0", "♦4/0", "♠5/0", "♥7/0"]), level: SEVEN }, // 逢人配补出 A2345
  // 随机那批保留各自的级数（级数决定谁是逢人配、也比大小用）
  ...sampleHands(400, 20261017)
    .filter((s) => s.cards.length <= 8)
    .slice(0, 36),
];

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

/**
 * 边界手牌：照搬 Go 的 `edgeHands`（14 手，含 A 当 1 的绕圈、两张逢人配、七个 3、四大天王），
 * 再加一手**单花色 34567** —— 那一手是 auditor-240 用变异测试找出来的：把 `shapesBeating` 里的
 * `straight-flush` 去掉（跟牌不生成同花顺），294 条测试**一条都不红**。Go 那边有这条对拍，TS 没移植。
 */
const EDGE_HANDS: { name: string; cards: string[]; level: Rank }[] = [
  { name: "A 当 1 的顺子", cards: ["♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0"], level: SEVEN },
  { name: "A 当 1 的顺子（两个窗口挨着）", cards: ["♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0", "♠6/0"], level: SEVEN },
  { name: "A 当 1 的顺子 + 逢人配", cards: ["♠2/0", "♠3/0", "♠4/0", "♠5/0", "♥7/0"], level: SEVEN },
  { name: "A 当 1 的三连对", cards: ["♠A/0", "♥A/0", "♠2/0", "♥2/0", "♠3/0", "♥3/0"], level: SEVEN },
  { name: "A 当 1 的三同连张", cards: ["♠A/0", "♥A/0", "♦A/0", "♠2/0", "♥2/0", "♦2/0"], level: SEVEN },
  { name: "绕圈另一头：三连对 QKA", cards: ["♠Q/0", "♥Q/0", "♠K/0", "♥K/0", "♠A/0", "♥A/0"], level: SEVEN },
  { name: "绕圈另一头：Q 和 A 不连着，不该有三同连张", cards: ["♠Q/0", "♥Q/0", "♦Q/0", "♠A/0", "♥A/0", "♦A/0"], level: SEVEN },
  { name: "两张逢人配", cards: ["♥7/0", "♥7/1"], level: SEVEN },
  { name: "两张逢人配 + 顺子", cards: ["♠3/0", "♠4/0", "♠5/0", "♥7/0", "♥7/1"], level: SEVEN },
  { name: "七个 3：炸弹张数", cards: ["♠3/0", "♥3/0", "♣3/0", "♦3/0", "♠3/1", "♥3/1", "♣3/1"], level: SEVEN },
  {
    name: "七个 3 + 一张逢人配：炸弹张数×顶法",
    cards: ["♠3/0", "♥3/0", "♣3/0", "♦3/0", "♠3/1", "♥3/1", "♣3/1", "♥7/0"],
    level: SEVEN,
  },
  { name: "四大天王", cards: ["大王/0", "大王/1", "小王/0", "小王/1"], level: SEVEN },
  { name: "两张逢人配都要用上：三带对 9 的三种打法", cards: ["♠9/0", "♥9/0", "♣9/0", "♠5/0", "♥7/0", "♥7/1"], level: SEVEN },
  { name: "级牌自己：打 2 时的 A 当 1", cards: ["♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0"], level: TWO },
  { name: "单花色 34567：是顺子也是同花顺", cards: ["♠3/0", "♠4/0", "♠5/0", "♠6/0", "♠7/0"], level: SEVEN },
];

describe("性质：耗时上限（守「不能退化成枚举子集」，不抠毫秒）", () => {
  // 上限取得比实测宽一个量级：它守的是「万一有人改成枚举子集」——那会从毫秒掉到秒级；
  // 两台机器差个一两倍不在它管的范围里。
  //
  // ⚠️ **手牌要挑重的**。起初我用 `deal(identity)[0]`（按顺序发的 27 张），实测只有 10ms ——
  // 那手牌点数分布规整、候选很少，根本不是最坏一档（复审指出来的）。
  //
  // 下面这手 27 张：**两张逢人配** + 12 个点数各两张 + 一张余牌。两张逢人配是关键 ——
  // 每个点数只有两张实牌，但配上两张逢人配就能凑出四张炸，候选不再是"凑不齐就跳过"，
  // 每条都要真的走一遍 `recognize`（内部还要枚举顶法）。本机实测：领出 664 手 **约 230ms**、
  // 跟一对 **约 75ms**。
  //
  // 这个手牌比 Go 那条基准（随机 27 张，领出 140ms / 跟一对 0.7ms）**更重**，不是同一档，
  // 所以别拿两边的数字直接比 —— Go 用的随机手牌里，两张实牌配两张逢人配凑炸的情形少得多。
  const hand = handOf([
    "♥7/0", "♥7/1",
    "♠2/0", "♥2/0", "♠3/0", "♥3/0", "♠4/0", "♥4/0", "♠5/0", "♥5/0", "♠6/0", "♥6/0",
    "♠8/0", "♥8/0", "♠9/0", "♥9/0", "♠10/0", "♥10/0", "♠J/0", "♥J/0", "♠Q/0", "♥Q/0",
    "♠K/0", "♥K/0", "♠A/0", "♥A/0", "♣2/0",
  ]);

  it("27 张领出 / 跟一对，都在宽松上限内", () => {
    const pair = comboOf(["♠9/0", "♥9/0"], "pair");

    const leadStart = Date.now();
    const leads = moves(hand, SEVEN);
    const leadMs = Date.now() - leadStart;

    const followStart = Date.now();
    const follows = moves(hand, SEVEN, pair);
    const followMs = Date.now() - followStart;

    expect(leads.length, "领出不该是空的").toBeGreaterThan(0);
    expect(follows.length, "跟一对不该是空的").toBeGreaterThan(0);
    expect(leadMs, `领出 ${leads.length} 手用了 ${leadMs}ms`).toBeLessThan(2000);
    expect(followMs, `跟一对用了 ${followMs}ms`).toBeLessThan(600);
  });
});

describe("性质：边界手牌和暴力枚举对拍（照搬 Go 的 EdgeHands）", () => {
  it("领出一致；再把手里每一种解读当上一手，跟牌也要一致", () => {
    let cases = 0;
    for (const edge of EDGE_HANDS) {
      const cards = handOf(edge.cards);
      const want = bruteMoves(cards, edge.level);
      expect(treatments(moves(cards, edge.level)), `${edge.name}（领出）`).toStrictEqual(treatments(want));
      cases++;
      // 拿**每一种解读**当上一手各对一次 —— 跟牌那条路就是在这里被压到的。
      for (const lead of want) {
        const wantFollow = want.filter((c) => compare(c, lead) === "greater");
        const what = `${edge.name}（跟 ${lead.type} ${comboRankName(lead)}）`;
        expect(treatments(moves(cards, edge.level, lead)), what).toStrictEqual(treatments(wantFollow));
        cases++;
      }
    }
    expect(cases, "一手都没对拍，用例没起作用").toBeGreaterThan(100);
  }, 60000);

  // auditor-240 报的那两条：上一手不是手里能打出的牌，所以上面那组（拿自己手里的解读当上一手）
  // 压不到这个形状，单列出来。注意**上面那组也会红**（"A 当 1 的顺子"那种全是黑桃的手牌，
  // 拿自己的顺子当上一手时，压得住它的正是同花顺）—— 两条各管一半，别以为单列的这条是多余的。
  it("跟普通牌型和跟炸弹，都要把同花顺列出来（同花顺是炸弹，压得住两者）", () => {
    const cards = handOf(["♠3/0", "♠4/0", "♠5/0", "♠6/0", "♠7/0"]); // 单花色 34567
    const pairK = comboOf(["♠K/0", "♥K/1"], "pair", 9);
    const bomb8 = comboOf(["♠8/0", "♥8/0", "♣8/0", "♦8/0"], "bomb", 9);
    expect(listed(moves(cards, 9, pairK))).toEqual(["straight-flush 7"]);
    expect(listed(moves(cards, 9, bomb8))).toEqual(["straight-flush 7"]);
  });
});
