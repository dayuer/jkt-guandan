// 出牌状态机的单测和性质测试。两端共用的那些用例在 testdata.test.ts（play.json）；
// 这里管牌例没覆盖到的：入参、拒绝之后局面不变、以及**随机对局的恒等式**。
import { describe, expect, it } from "vitest";
import { ACE, cardKey, newDeck, SEVEN, type Card, type Rank } from "./card";
import { moves } from "./moves";
import { Game, nextSeat, partnerSeat, REJECT_REASONS, RuleError } from "./play";
import { handOf } from "./testing/cards";
import { seededRandom, type Rng } from "./testing/random";

/** 走一手；返回被拒的原因（没被拒就是 undefined）。 */
function rejectReason(fn: () => void): string | undefined {
  try {
    fn();
    return undefined;
  } catch (error) {
    if (error instanceof RuleError) return error.reason;
    throw error;
  }
}

describe("座位", () => {
  it("逆时针 1→2→3→4，对家是隔一个", () => {
    expect([1, 2, 3, 4].map(nextSeat)).toEqual([2, 3, 4, 1]);
    expect([1, 2, 3, 4].map(partnerSeat)).toEqual([3, 4, 1, 2]);
  });
});

describe("开局", () => {
  const hand = handOf(["♠9/0"]);

  it("级数超范围、领出座位不是 1–4、手牌家数不对，都报错", () => {
    expect(() => new Game(15, [hand, hand, hand, hand], 1)).toThrow(/级数/);
    expect(() => new Game(SEVEN, [hand, hand, hand, hand], 5)).toThrow(/领出的座位/);
    expect(() => new Game(SEVEN, [hand, hand], 1)).toThrow(/四家|2 家/);
  });

  it("某家没牌、或者同一张牌出现在两家手里，都报错", () => {
    expect(() => new Game(SEVEN, [[], hand, hand, hand], 1)).toThrow(/没有牌/);
    expect(() => new Game(SEVEN, [hand, hand, hand, hand], 1)).toThrow(/同时在/);
  });

  it("开局：轮到的就是领出的人，且是领出状态", () => {
    const game = new Game(SEVEN, [hand, handOf(["♠10/0"]), handOf(["♠J/0"]), handOf(["♠Q/0"])], 3);
    expect(game.turn()).toBe(3);
    expect(game.leading()).toBe(true);
    expect(game.over()).toBe(false);
    expect(game.result()).toBeUndefined();
  });
});

describe("出牌的校验", () => {
  const game = () =>
    new Game(SEVEN, [handOf(["♠9/0"]), handOf(["♠10/0"]), handOf(["♠J/0"]), handOf(["♠Q/0"])], 1);

  it("一张牌都不给：no-cards", () => {
    expect(rejectReason(() => game().play(1, [], "single"))).toBe("no-cards");
  });

  it("打了手里没有的牌：cards-not-in-hand", () => {
    expect(rejectReason(() => game().play(1, handOf(["♠3/0"]), "single", 3))).toBe("cards-not-in-hand");
  });

  // 和 Go 的 `TestPlayRejectsTamperedCard` 对着（#245 复审 B1 之后补的）。
  //
  // 光核 `card.id` 不够：TS 里 `id` 是个普通字段，不是从 rank/suit 推出来的，下面这三张
  // 「ID 偷自手里那张 ♠3、字段却是别的」的牌都能写出来。收下它的后果是 —— `recognize`
  // 按 rank/suit 认成一张 A，拿掉的时候却按 ID 把 ♠3 删了：**打出去的和手里少掉的不是同一张牌**。
  // 这种牌牌例里写不出来（牌例按 cardKey 写、两端都走 parseKey，造不出一张 ID 和字段打架的牌），
  // 所以只有这里守得住。
  it("改过字段的牌不能出：ID 对得上也不行", () => {
    // 原样的牌照常能出 —— 别把好的也一起拦了。
    const good = new Game(SEVEN, [handOf(["♠3/0", "♠4/0"]), handOf(["♠5/0"]), handOf(["♠6/0"]), handOf(["♠7/0"])], 1);
    good.play(1, handOf(["♠3/0"]), "single", 3);
    expect(good.hand(1).map(cardKey)).toStrictEqual(["♠4/0"]);

    const stolenId = handOf(["♠3/0"])[0]!.id;
    const cases: Array<[string, Card, Rank]> = [
      ["点数改成 A", { ...handOf(["♠A/0"])[0]!, id: stolenId }, ACE],
      ["花色改成 ♥", { ...handOf(["♥3/0"])[0]!, id: stolenId }, 3],
      ["副号改成 1", { ...handOf(["♠3/0"])[0]!, id: stolenId, deck: 1 }, 3],
    ];
    for (const [what, card, rank] of cases) {
      const g = new Game(SEVEN, [handOf(["♠3/0", "♠4/0"]), handOf(["♠5/0"]), handOf(["♠6/0"]), handOf(["♠7/0"])], 1);
      const before = [g.turn(), g.hand(1).map(cardKey), g.moves().length];
      expect(rejectReason(() => g.play(1, [card], "single", rank)), what).toBe("cards-not-in-hand");
      // 被拒之后局面一点没动：手里还是那两张，什么都没记进 moveLog。
      expect([g.turn(), g.hand(1).map(cardKey), g.moves().length], what).toStrictEqual(before);
    }
  });

  it("声明的那一种解读不成立：bad-combo", () => {
    // 一张牌当不成对子
    expect(rejectReason(() => game().play(1, handOf(["♠9/0"]), "pair"))).toBe("bad-combo");
  });

  it("没轮到、领出时过牌、已经结束：各是各的原因", () => {
    expect(rejectReason(() => game().play(2, handOf(["♠10/0"]), "single", 10))).toBe("not-your-turn");
    expect(rejectReason(() => game().pass(1))).toBe("leading-cannot-pass");

    const over = game();
    over.play(1, handOf(["♠9/0"]), "single", 9);
    expect(rejectReason(() => over.play(1, handOf(["♠9/0"]), "single", 9))).toBe("not-your-turn");
  });

  it("拒绝之后局面一点没变（第八条 一、1）", () => {
    const g = game();
    const before = [g.turn(), g.leading(), g.finished(), g.moves().length];
    expect(rejectReason(() => g.play(3, handOf(["♠J/0"]), "single", 11))).toBe("not-your-turn");
    expect(rejectReason(() => g.pass(1))).toBe("leading-cannot-pass");
    expect([g.turn(), g.leading(), g.finished(), g.moves().length]).toStrictEqual(before);
  });

  it("逢人配要按声明的牌面挑解读", () => {
    // 打 7，手里 ♠A + ♥7（逢人配）：既能当一对 A，也能把 ♥7 当单张 7 出
    const g = new Game(SEVEN, [handOf(["♠A/0", "♥7/0"]), handOf(["♠3/0"]), handOf(["♠4/0"]), handOf(["♠5/0"])], 1);
    g.play(1, handOf(["♥7/0"]), "single", 7);
    expect(g.hand(1).map(cardKey)).toStrictEqual(["♠A/0"]);
    expect(g.lastPlay()?.combo.wilds).toHaveLength(1); // 顶成它自己
  });

  it("拒绝的原因都在封闭取值里", () => {
    expect([...REJECT_REASONS].sort()).toStrictEqual(
      ["bad-combo", "cards-not-in-hand", "does-not-beat", "game-over", "leading-cannot-pass", "no-cards", "not-your-turn"],
    );
  });
});

// 随机打完整副牌，看恒等式站不站得住。用固定种子的伪随机数，红了能复现。
describe("性质：随机对局", () => {
  /** 随便挑一个合法出法（跟牌时挑压得住的），挑不出来就过牌。 */
  function playOneRandomly(game: Game, rng: Rng): void {
    const seat = game.turn();
    const last = game.lastPlay();
    const legal = moves(game.hand(seat), game.playingLevel, last?.combo);
    if (legal.length > 0 && (game.leading() || rng.int(10) < 8)) {
      const pickOne = legal[rng.int(legal.length)];
      game.play(seat, pickOne.cards, pickOne.type, pickOne.rank);
      return;
    }
    game.pass(seat);
  }

  it("牌数守恒、轮到的永远是还在场的人、出完的名单不重复", () => {
    for (let round = 0; round < 30; round++) {
      const rng = seededRandom(20261019 + round);
      const hands = gameHands(rng);
      const total = hands.reduce((n, hand) => n + hand.length, 0);
      const game = new Game(SEVEN, hands, 1 + rng.int(4));

      for (let step = 0; step < 2000 && !game.over(); step++) {
        playOneRandomly(game, rng);

        // 牌数守恒：四家手里的 + 已经打出去的，永远等于开局那些
        const left = [1, 2, 3, 4].reduce((n, seat) => n + game.hand(seat).length, 0);
        const played = game.moves().reduce((n, move) => n + (move.pass ? 0 : (move.combo?.cards.length ?? 0)), 0);
        expect(left + played, `第 ${step + 1} 步之后`).toBe(total);

        // 出完的人不再出现在名册里两次
        expect(new Set(game.finished()).size).toBe(game.finished().length);
        // 轮到的要么是还在场的人，要么这一副结束了（0）
        const turn = game.turn();
        if (turn !== 0) expect(game.hand(turn).length, `${turn} 号座还有牌`).toBeGreaterThan(0);
      }

      expect(game.over(), "2000 步之内该打完了").toBe(true);
      const result = game.result();
      expect(result, "结束了就该有结果").toBeDefined();
      expect(result?.order, "四家都要有名次").toHaveLength(4);
      expect(new Set(result?.order).size).toBe(4);
      expect(result?.headSeat).toBe(result?.order[0]);
    }
  }, 30000);
});

/** 随机发四手牌：把整副牌打乱按 27 张切四份。 */
function gameHands(rng: Rng): Card[][] {
  const deck = newDeck();
  for (let i = deck.length - 1; i > 0; i--) {
    const j = rng.int(i + 1);
    [deck[i], deck[j]] = [deck[j], deck[i]];
  }
  return [0, 1, 2, 3].map((i) => deck.slice(i * 27, (i + 1) * 27));
}
