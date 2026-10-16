// 两端共用的牌例（#87 第 1–2 步）：这里和服务端的 server/internal/rules/fixtures_test.go
// 读仓库根 testdata/rules/ 下同一批文件。规则口径改在哪一头，这里都会红。
//
// 结构校验（`rule` 非空、拒绝未知字段、不能空用例列表……）由 testing/fixtures.ts 统一把关
// 并有自己的单测，这里只查「算出来的结果和牌例写的一样」。
import { describe, expect, it } from "vitest";
import { cardKey, newDeck, parseKey, parseRank, type Card, type Rank } from "./card";
import { asKey, comboRankName, comboSummary, compare, parseComboType, pick, recognize, type Combo, type Order } from "./combo";
import { deal, HANDS_EACH, permShuffler, SEATS } from "./deal";
import { moves } from "./moves";
import { Game, RuleError } from "./play";
import { power } from "./power";
import { handOf } from "./testing/cards";
import { compareFixture, combosFixture, dealFixture, deckFixture, movesFixture, playFixture, powerFixture } from "./testing/testdata";
import type { CompareSide, PlayStep } from "./testing/fixtures";

const keys = (cards: readonly Card[]): string[] => cards.map(cardKey);

/** 一条解读里的顶法，写成「哪张牌 当 什么」的一串；**按集合比**（README「逢人配」第 5 条）。 */
function wildsOf(combo: Combo): string[] {
  return combo.wilds.map((w) => `${cardKey(w.card)} 当 ${asKey(w.as)}`).sort();
}

/**
 * `compare.json` 的一边：按声明的解读认这手牌。牌面写了就一起比；没写表示这一手在那个牌型下
 * 只有一种解读 —— 有两种及以上时 `pick` 不猜，认不出来就是牌例或实现对不上。
 */
function declaredCombo(side: CompareSide, level: Rank, what: string): Combo {
  const combos = recognize(handOf(side.cards), level);
  const rank = side.rank === undefined ? undefined : parseRank(side.rank);
  const combo = pick(combos, parseComboType(side.kind), rank);
  if (!combo) {
    const declared = `${side.kind}${side.rank === undefined ? "" : ` ${side.rank}`}`;
    throw new Error(`${what}：这手牌里没有唯一的「${declared}」解读 —— 实际有 ${comboSummary(combos)}`);
  }
  return combo;
}

describe("deck.json：整副牌的顺序是两端的约定", () => {
  it("和 newDeck() 逐张一样", () => {
    expect(deckFixture.order).toStrictEqual(keys(newDeck()));
  });

  it("写法能读回同一张牌（ID 由位置定，不写在写法里）", () => {
    const deck = newDeck();
    deckFixture.order.forEach((written, i) => {
      const face = parseKey(written);
      expect(face, written).toStrictEqual({ rank: deck[i].rank, suit: deck[i].suit, deck: deck[i].deck });
    });
  });
});

describe("power.json：每个级数下牌点由大到小（第四条 一）", () => {
  it("13 个级数全列了，没有重复", () => {
    const levels = powerFixture.map((v) => parseRank(v.level));
    expect(levels).toHaveLength(13);
    expect(new Set(levels).size).toBe(13);
  });

  for (const vector of powerFixture) {
    it(vector.name, () => {
      const level = parseRank(vector.level);
      expect(vector.order, "应该列 15 个牌点（2–A + 大小王）").toHaveLength(15);
      const ranks = vector.order.map(parseRank);
      expect(new Set(ranks).size, "同一个牌点不该出现两次").toBe(15);
      for (let i = 1; i < ranks.length; i++) {
        expect(
          power(ranks[i - 1], level),
          `打 ${vector.level} 时 ${vector.order[i - 1]} 应比 ${vector.order[i]} 大`,
        ).toBeGreaterThan(power(ranks[i], level));
      }
    });
  }
});

describe("deal.json：发牌顺序 → 四手牌（第七条 一、5）", () => {
  for (const vector of dealFixture) {
    it(vector.name, () => {
      const hands = deal(permShuffler(vector.shuffle));

      // 结构上的硬条件，和牌例写的是什么无关：每名运动员 27 张（第二条 6），
      // 四手合起来正好是整副 108 张、没有一张重复。
      expect(hands).toHaveLength(SEATS);
      for (const hand of hands) expect(hand).toHaveLength(HANDS_EACH);
      expect(new Set(hands.flat().map((card) => card.id)).size).toBe(108);

      expect(hands.map(keys)).toStrictEqual(vector.hands);
    });
  }
});

describe("combos.json：一手牌的全部解读（第三条）", () => {
  for (const vector of combosFixture) {
    it(vector.name, () => {
      const level = parseRank(vector.level);
      const combos = recognize(handOf(vector.cards), level);

      // 主断言只比牌型和牌面（顺序也一起比：README 定了「先按牌型表、再按牌面」）。
      // `wilds`（哪张逢人配顶成了什么）是**可选**的，由下面单独查 —— 大多数用例顶法唯一，
      // 没必要每条都写。和 Go 的 TestCombosFixture 一个形状（#217 定的）。
      expect(combos.map((c) => ({ kind: c.type, rank: comboRankName(c) }))).toStrictEqual(
        vector.combos.map((c) => ({ kind: c.kind, rank: c.rank })),
      );

      // 牌例声明了顶法的，要求实现挑的**正是那一种**：两头都得按「点数、花色最小」挑，
      // 不能一边挑最小、一边随便挑。
      for (const declared of vector.combos) {
        if (!declared.wilds) continue;
        const found = combos.find((c) => c.type === declared.kind && comboRankName(c) === declared.rank);
        if (!found) continue; // 牌型/牌面对不上由上面的主断言报，这里只管顶法
        expect(wildsOf(found), `${declared.kind} ${declared.rank} 的顶法`).toStrictEqual(
          declared.wilds.map((w) => `${w.card} 当 ${w.as}`).sort(),
        );
      }
    });
  }
});

describe("compare.json：谁压得住谁（第四条 二）", () => {
  const WANT: Record<string, Order> = { a: "greater", b: "less", equal: "equal", incomparable: "incomparable" };
  // 牌例只写一个方向的结论，这里两个方向都断言（README：`a` 那条同时也要求 b 比 a 小）。
  const REVERSED: Record<Order, Order> = { greater: "less", less: "greater", equal: "equal", incomparable: "incomparable" };

  for (const vector of compareFixture) {
    it(vector.name, () => {
      const level = parseRank(vector.level);
      const a = declaredCombo(vector.a, level, `${vector.name}（a）`);
      const b = declaredCombo(vector.b, level, `${vector.name}（b）`);

      const want = WANT[vector.result];
      if (!want) throw new Error(`${vector.name}：认不出 result ${JSON.stringify(vector.result)}`);

      expect(compare(a, b), "a 比 b").toBe(want);
      expect(compare(b, a), "b 比 a").toBe(REVERSED[want]);
    });
  }
});

describe("moves.json：合法出法（第三条、第四条 二）", () => {
  /** 一手出法写成牌例里的样子；`wilds` 只在真用了逢人配时才写（顶法唯一时牌例也不写）。 */
  const moveSpec = (combo: Combo): { kind: string; rank: string; size: number; wilds?: { card: string; as: string }[] } => {
    const spec = { kind: combo.type, rank: comboRankName(combo), size: combo.cards.length };
    return combo.wilds.length > 0
      ? { ...spec, wilds: combo.wilds.map((w) => ({ card: cardKey(w.card), as: asKey(w.as) })) }
      : spec;
  };

  for (const vector of movesFixture) {
    it(vector.name, () => {
      const level = parseRank(vector.level);
      const lead = vector.lead ? declaredCombo(vector.lead, level, `${vector.name}（上一手）`) : undefined;
      expect(moves(handOf(vector.hand), level, lead).map(moveSpec)).toStrictEqual(vector.moves);
    });
  }
});

describe("play.json：出牌状态机（第八条、第九条）", () => {
  /** 这一刻的局面，写成牌例里的那套字段。 */
  const stateOf = (game: Game): Record<string, unknown> => {
    const last = game.lastPlay();
    const state: Record<string, unknown> = {
      turn: game.turn(),
      leading: game.leading(),
      last_seat: last ? last.seat : null,
      finished: game.finished(),
    };
    if (last) {
      state.last_kind = last.combo.type;
      state.last_rank = comboRankName(last.combo);
    }
    const result = game.result();
    if (result) {
      state.result = { order: result.order, head_seat: result.headSeat, partner_place: result.partnerPlace };
    }
    return state;
  };

  /**
   * 局面快照：**被拒之后必须一点没变**。和 Go 的 `playSnapOf` 对着来。
   *
   * 为什么不拿 `stateOf`（公开读法）当快照：`passes` 没有公开读法，而它正是「先记一笔再报错」
   * 这种坏法最藏身的地方 —— 存档里也没有它，所以只看可观测状态会漏。Go 那边同包直接读
   * `g.passes`，这里只能从私有字段里取：是测试，不是生产代码。
   */
  const snapOf = (game: Game): unknown => ({
    turn: game.turn(),
    leading: game.leading(),
    lastSeat: (game as unknown as { lastSeat: number }).lastSeat,
    last: game.lastPlay() ?? null,
    passes: (game as unknown as { passes: number }).passes,
    finished: game.finished(),
    moves: game.moves(),
    hands: Array.from({ length: SEATS }, (_, i) => keys(game.hand(i + 1))),
  });

  /** 走一步。出牌时按牌例里的写法去整副牌里取那一张（ID 才和手里的对得上）。 */
  const applyStep = (game: Game, step: PlayStep): void => {
    if (step.pass) {
      game.pass(step.seat);
      return;
    }
    const rank = step.rank === undefined ? undefined : parseRank(step.rank);
    game.play(step.seat, handOf(step.cards ?? []), parseComboType(step.kind ?? ""), rank);
  };

  /** 这一步被拒的原因；没被拒、或者抛的不是 `RuleError`，都返回能看懂的字符串。 */
  const rejectReasonOf = (fn: () => void): string => {
    try {
      fn();
      return "（没被拒）";
    } catch (error) {
      return error instanceof RuleError ? error.reason : `（不是 RuleError：${String(error)}）`;
    }
  };

  for (const vector of playFixture) {
    it(vector.name, () => {
      const level = parseRank(vector.level);
      const game = new Game(level, vector.hands.map(handOf), vector.leader);

      vector.steps.forEach((step, i) => {
        const what = `第 ${i + 1} 步`;
        const before = snapOf(game);

        if (step.reject !== undefined) {
          expect(rejectReasonOf(() => applyStep(game, step)), what).toBe(step.reject);
          // 「被拒」的全部含义就是局面没动 —— 先记一笔再报错，存档就坏了。
          // 没有这一条，七个取值里只有会改可观测状态的那几个测得出（#247 复审 B1）。
          expect(snapOf(game), `${what}：被拒之后局面不该变`).toStrictEqual(before);
        } else {
          applyStep(game, step);
          // 成功的那一步要写 after，不然这一步过了什么都没查（Go 的 TestPlayFixture 一样）。
          expect(step.after, `${what}：成功的那一步要写 after`).toBeDefined();
        }
        if (step.after) expect(stateOf(game), what).toMatchObject(step.after);
      });

      // 和 Go 同一对检查：turn: 0 和 result 是一对，写了一个就得写另一个。
      if (vector.expect.turn === 0) {
        expect(vector.expect.result, "expect 说这一副结束了（turn: 0），却没写 result").toBeDefined();
      }
      if (vector.expect.result) {
        expect(vector.expect.turn, "expect 写了 result，却没写 turn: 0").toBe(0);
      }
      expect(stateOf(game)).toMatchObject(vector.expect);
    });
  }
});
