// 进贡、还贡、抗贡（#87 第 6 步）的测试。
//
// 照 server/internal/rules/tribute_test.go 一条条搬过来的（README「移植清单」：随机和定向两类都要搬），
// 顺手多加了 `tribute.json` 那 10 条共用牌例（testdata.test.ts）。
//
// 两条主线：
//   - 按第十条一条一条钉：单贡贡最大的一张（红心级牌除外）、双贡大的给上游/小的给搭档/一样大按
//     顺时针、抗贡两人合计两张大王、还牌不超过 10（全都大于 10 时还最小的）、首圈谁领出；
//   - **被拒的动作不许改局面**：进贡/还牌和出牌一样是玩家给的牌，引擎只校验 —— 校验失败必须
//     哪都没动。
//
// 「不超过 10」按**牌面数字**算（读法 A，仓库所有者还没拍板，见 testdata/rules/tribute.json 顶部
// 与 README 的「待确认」一节）：打 7 时 ♠7、♥7 牌面是 7，能还。
import { describe, expect, it } from "vitest";
import { SEVEN, cardKey, type Card, type Rank } from "./card";
import { partnerSeat } from "./play";
import { Tribute, TributeError, clockwiseSeat, type TributeRejectReason } from "./tribute";
import { handOf } from "./testing/cards";

/** 上副牌的结果：order 是出完的先后、按座位号写；上游和搭档名次都由它推得。 */
function prev(order: number[]) {
  const headSeat = order[0];
  return { order: order.slice(), headSeat, partnerPlace: order.indexOf(partnerSeat(headSeat)) + 1 };
}

/** 四家的手牌，每家一串空格分隔的牌面写法（1 号座在前）。 */
function tribute(level: Rank, seat1: string, seat2: string, seat3: string, seat4: string, order: number[]): Tribute {
  const hands = [seat1, seat2, seat3, seat4].map((s) => handOf(s.split(" ").filter((x) => x !== "")));
  return new Tribute(level, hands, prev(order));
}

/** 按写法从手里取一张（和 Go 的 cardIn 一样）。 */
function cardIn(hand: readonly Card[], key: string): Card {
  const c = hand.find((x) => cardKey(x) === key);
  if (c === undefined) throw new Error(`手里没有 ${key}（手牌：${hand.map(cardKey).join(" ")}）`);
  return c;
}

/**
 * 不管贡完没贡完都取四家手牌。
 *
 * 读的是 `Tribute` 的私有字段 —— 只有测试这么干（Go 那边同样是同包直读 `tr.hands`）。
 * 公开的 `hands()` 按 Go 的口径要求贡完才给，这里要的是「被拒的动作有没有动到局面」，
 * 那多半发生在还没贡完的时候。
 */
function handsNow(tr: Tribute): Card[][] {
  return (tr as unknown as { handCards: Card[][] }).handCards;
}

function handsText(tr: Tribute): string {
  return handsNow(tr)
    .map((h, i) => `${i + 1}号:${h.map(cardKey).join(" ")}`)
    .join(" ");
}

/** 数总张数 + 每张牌出现几次（守恒用）。按 ID 排好再写出来：牌挪了位置顺序会变，不排就对不上了。 */
function counts(hands: readonly Card[][]): [number, string] {
  const seen = new Map<number, number>();
  let n = 0;
  for (const h of hands) {
    for (const c of h) {
      n++;
      seen.set(c.id, (seen.get(c.id) ?? 0) + 1);
    }
  }
  const text = [...seen.entries()].sort((a, b) => a[0] - b[0]).map(([id, k]) => `${id}×${k}`).join(" ");
  return [n, text];
}

/** 灌一个该被拒的动作：确实被拒了（而且原因对得上）、局面一张牌都没动。 */
function reject(
  what: string,
  before: string,
  tr: Tribute,
  reason: TributeRejectReason,
  act: () => void,
): void {
  let err: unknown;
  try {
    act();
  } catch (e) {
    err = e;
  }
  expect(err, `${what}：这个动作不合规，却被接受了`).toBeInstanceOf(TributeError);
  expect((err as TributeError).reason, what).toBe(reason);
  expect(handsText(tr), `${what}：被拒之后手牌变了`).toBe(before);
}

describe("单贡（第十条 二）", () => {
  it("贡的必须是手里最大的一张，红心级牌不算；还牌不超过 10", () => {
    const hands = [
      handOf(["♠K/0", "♥3/0", "♠3/0", "♠5/0"]),
      handOf(["♠2/0", "♥2/0", "♣3/0", "♦5/0"]),
      handOf(["♠4/0", "♥4/0", "♣4/0", "♦4/0"]),
      handOf(["♠A/0", "♥7/0", "♦5/1", "♣6/0"]),
    ];
    const tr = new Tribute(SEVEN, hands, prev([1, 2, 3, 4])); // 上游 1、搭档第 3 → 下游是 4
    expect(tr.kind()).toBe("single");
    expect(tr.givers()).toEqual([4]);

    const before = handsText(tr);
    // 红心级牌（逢人配）不能贡（第十条 二）。
    reject("贡红心级牌", before, tr, "wild-cannot-give", () => tr.give(4, cardIn(hands[3], "♥7/0")));
    // 不是手里最大的也不能贡：手里最大的是 ♠A。
    for (const key of ["♦5/1", "♣6/0"]) {
      reject(`贡非最大的 ${key}`, before, tr, "not-biggest", () => tr.give(4, cardIn(hands[3], key)));
    }
    // 不用进贡的人也不能贡。
    reject("不用进贡的人贡牌", before, tr, "not-a-giver", () => tr.give(2, cardIn(hands[1], "♠2/0")));

    expect(() => tr.give(4, cardIn(hands[3], "♠A/0"))).not.toThrow();
    expect(tr.done()).toBe(false);
    expect(tr.leader()).toBeUndefined();
    expect(tr.receiver(4)).toBe(1); // 单贡贡给上游
  });

  it("逢人配就算按牌力是最大的也不能贡（「红心级牌除外」单独成立）", () => {
    // 手里还有一张 ♦7/0：它不是逢人配，但牌力和逢人配一样大 —— 少了「红心级牌除外」这一条，
    // 贡它就会以「和最大那张一样大」的理由被放过去。
    const tr = tribute(SEVEN, "♠K/0", "♠2/0", "♠4/0", "♥7/0 ♦7/0 ♠5/0", [1, 2, 3, 4]);
    const hands = handsNow(tr);
    const before = handsText(tr);
    reject("手里最大的就是逢人配，也不能贡", before, tr, "wild-cannot-give", () =>
      tr.give(4, cardIn(hands[3], "♥7/0")),
    );
    // 实牌里最大的是 ♦7/0（和逢人配一样大，但它是实牌），贡它没问题。
    expect(() => tr.give(4, cardIn(hands[3], "♦7/0"))).not.toThrow();
  });

  it("还牌：不超过 10 的才能还；下游领出首圈", () => {
    const hands = [
      handOf(["♠K/0", "♠3/0", "♥7/0"]),
      handOf(["♠2/0"]),
      handOf(["♠4/0"]),
      handOf(["♠A/0", "♦9/0"]),
    ];
    const tr = new Tribute(SEVEN, hands, prev([1, 2, 3, 4]));
    expect(() => tr.give(4, cardIn(hands[3], "♠A/0"))).not.toThrow();

    const before = handsText(tr);
    // 1 号座手里有牌面不超过 10 的牌（♠3、♥7），只能还这种 —— ♠K 不能还。
    reject("还 ♠K", before, tr, "cannot-return", () => tr.returnCard(1, cardIn(hands[0], "♠K/0")));
    // 没收贡的人不能还。
    reject("没收贡的人还牌", before, tr, "not-a-receiver", () => tr.returnCard(2, cardIn(hands[1], "♠2/0")));
    // 手里没有的牌不能还。
    //
    // ⚠️ 这里要挑一张**此刻真不在 1 号座手里**的牌：♠A 已经贡进 1 号座手里了，拿它试出来的是
    // 下面那条 cannot-return（Go 的 tribute_test.go 这条用例就是那样写的 —— 名字写「手里没有的牌」，
    // 实际走的是 canReturn 那一路，`Return` 里 holds 这一支一直没人守）。♦9 在 4 号座手里。
    reject("还手里的牌之外的牌", before, tr, "card-not-in-hand", () =>
      tr.returnCard(1, cardIn(hands[3], "♦9/0")),
    );

    // 牌面是 7 的 ♥7（逢人配）按读法 A 也算「不超过 10」，能还。
    expect(() => tr.returnCard(1, cardIn(hands[0], "♥7/0"))).not.toThrow();
    expect(() => tr.returnCard(1, cardIn(hands[0], "♥7/0"))).toThrow(); // 同一家不能还两次
    expect(tr.done()).toBe(true);
    expect(tr.leader()).toBe(4); // 单贡后由进贡的 4 号座领出（第十条 二）

    const g = tr.start();
    expect(g.turn()).toBe(4);
    expect(g.hand(1).map(cardKey)).toContain("♠A/0"); // 拿到了贡来的 ♠A
    expect(g.hand(4).map(cardKey)).toContain("♥7/0"); // 拿回了还来的 ♥7
  });

  it("手里全大于 10 时还最小的（按当前级数下的大小）", () => {
    const hands = [handOf(["♠K/0", "♥A/0", "♦J/0"]), handOf(["♠2/0"]), handOf(["♠4/0"]), handOf(["♠A/0"])];
    const tr = new Tribute(SEVEN, hands, prev([1, 2, 3, 4]));
    expect(() => tr.give(4, cardIn(hands[3], "♠A/0"))).not.toThrow();

    const before = handsText(tr);
    for (const key of ["♠K/0", "♥A/0"]) {
      reject(`还 ${key}`, before, tr, "cannot-return", () => tr.returnCard(1, cardIn(hands[0], key)));
    }
    expect(() => tr.returnCard(1, cardIn(hands[0], "♦J/0"))).not.toThrow(); // J 最小
  });
});

describe("双贡（第十条 三）", () => {
  it("大的给上游、小的给搭档；还牌后由贡给上游的那位领出；牌守恒", () => {
    const hands = [
      handOf(["♠3/0", "♦4/0"]),
      handOf(["♠K/0", "♦6/0"]), // 2 号座：贡 ♠K（比 4 号座的小）
      handOf(["♠5/0", "♦7/0"]),
      handOf(["♠A/0", "♦8/0"]), // 4 号座：贡 ♠A（大）
    ];
    const tr = new Tribute(SEVEN, hands, prev([1, 3, 2, 4])); // 1、3 一方拿了头游二游 → 双下
    expect(tr.kind()).toBe("double");
    expect(tr.givers()).toEqual([2, 4]);

    expect(() => tr.give(2, cardIn(hands[1], "♠K/0"))).not.toThrow();
    expect(tr.receiver(2)).toBeUndefined(); // 还有人没贡完，收贡的人不该定下来
    expect(() => tr.give(4, cardIn(hands[3], "♠A/0"))).not.toThrow();
    expect(tr.receiver(4)).toBe(1); // ♠A 大，贡给上游
    expect(tr.receiver(2)).toBe(3); // ♠K 小，贡给上游的搭档

    expect(() => tr.returnCard(1, cardIn(hands[0], "♠3/0"))).not.toThrow();
    expect(tr.leader()).toBeUndefined(); // 还有人没收完
    expect(() => tr.returnCard(3, cardIn(hands[2], "♠5/0"))).not.toThrow();
    expect(tr.leader()).toBe(4); // 贡给上游的 4 号座领出（第十条 三）

    // 守恒：进贡还贡只是把牌挪了个位置，四家的牌一张不多一张不少。
    const [gotN, gotText] = counts(handsNow(tr));
    const [wantN, wantText] = counts(hands);
    expect(gotN).toBe(wantN);
    expect(gotText).toBe(wantText);
  });

  it("两张贡牌一样大时各给自己顺时针的邻座", () => {
    const hands = [handOf(["♠3/0"]), handOf(["♠A/0"]), handOf(["♠5/0"]), handOf(["♥A/0"])];
    const tr = new Tribute(SEVEN, hands, prev([1, 3, 2, 4]));
    expect(() => tr.give(2, cardIn(hands[1], "♠A/0"))).not.toThrow();
    expect(() => tr.give(4, cardIn(hands[3], "♥A/0"))).not.toThrow();
    // 座位 1→2→3→4 是逆时针，所以顺时针的邻座是 s−1：2 号座给 1 号座、4 号座给 3 号座。
    expect(tr.receiver(2)).toBe(clockwiseSeat(2));
    expect(tr.receiver(2)).toBe(1);
    expect(tr.receiver(4)).toBe(clockwiseSeat(4));
    expect(tr.receiver(4)).toBe(3);
    expect(() => tr.returnCard(1, cardIn(hands[0], "♠3/0"))).not.toThrow();
    expect(() => tr.returnCard(3, cardIn(hands[2], "♠5/0"))).not.toThrow();
    expect(tr.leader()).toBe(2);
  });
});

describe("抗贡（第十条 四）", () => {
  it("要进贡的两人合计两个大王就抗贡：不贡不还，上游领出", () => {
    const tr = tribute(SEVEN, "♠3/0", "大王/0 ♦6/0", "♠5/0", "大王/1 ♦8/0", [1, 3, 2, 4]);
    expect(tr.kind()).toBe("resisted");
    expect(tr.done()).toBe(true); // 不用贡也不用还，一上来就该算完
    expect(tr.leader()).toBe(1);
    expect(tr.givers()).toHaveLength(2); // 抗贡也要给出要进贡的是谁（界面要显示是谁抗的贡）

    const hands = handsNow(tr);
    const before = handsText(tr);
    reject("抗贡了还进贡", before, tr, "resisted", () => tr.give(2, cardIn(hands[1], "♦6/0")));
    reject("抗贡了还还牌", before, tr, "resisted", () => tr.returnCard(1, cardIn(hands[0], "♠3/0")));

    expect(tr.start().turn()).toBe(1);
  });

  it("一个人手里两个大王、另一个人没有：合计两个，也算抗贡", () => {
    const tr = tribute(SEVEN, "♠3/0", "大王/0 大王/1", "♠5/0", "♦8/0", [1, 3, 2, 4]);
    expect(tr.kind()).toBe("resisted");
  });

  it("单贡时下游只有一张大王：不抗贡，而且大王可以贡", () => {
    const hands = [handOf(["♠3/0"]), handOf(["♠2/0"]), handOf(["♠4/0"]), handOf(["大王/0", "♦6/0"])];
    const tr = new Tribute(SEVEN, hands, prev([1, 2, 3, 4]));
    expect(tr.kind()).toBe("single");
    expect(() => tr.give(4, cardIn(hands[3], "大王/0"))).not.toThrow();
  });
});

describe("调用方给错了（不是「这一手不合规则」，是普通 Error）", () => {
  it("上副牌结果不完整、级数不合法，都报错", () => {
    const hands = [handOf(["♠3/0"]), handOf(["♠2/0"]), handOf(["♠4/0"]), handOf(["♠5/0"])];
    for (const bad of [
      { order: [1, 2], headSeat: 1, partnerPlace: 2 }, // 名次不全
      { order: [1, 2, 3, 4], headSeat: 5, partnerPlace: 2 }, // 上游不是 1–4
      { order: [1, 2, 3, 4], headSeat: 1, partnerPlace: 1 }, // 搭档名次不对（1 是上游自己）
      { order: [1, 2, 3, 4], headSeat: 1, partnerPlace: 5 }, // 搭档名次越界
    ]) {
      expect(() => new Tribute(SEVEN, hands, bad), JSON.stringify(bad)).toThrow();
    }
    expect(() => new Tribute(99 as Rank, hands, prev([1, 2, 3, 4]))).toThrow();
  });

  it("还没贡完不能取手牌、不能开打；同一家不能贡两次", () => {
    const hands = [handOf(["♠3/0"]), handOf(["♠2/0"]), handOf(["♠4/0"]), handOf(["♠A/0"])];
    const tr = new Tribute(SEVEN, hands, prev([1, 2, 3, 4]));
    expect(() => tr.hands()).toThrow();
    expect(() => tr.start()).toThrow();
    expect(() => tr.give(4, cardIn(hands[3], "♠A/0"))).not.toThrow();
    expect(() => tr.give(4, cardIn(hands[3], "♠A/0"))).toThrow(); // 同一家不能贡两次
    expect(() => tr.returnCard(1, cardIn(hands[0], "♠3/0"))).not.toThrow();
    expect(tr.hands()).toHaveLength(4);
    expect(tr.hands()[0].map(cardKey)).toEqual(["♠A/0"]); // 1 号座：♠3 还出去了，♠A 进来了
  });
});
