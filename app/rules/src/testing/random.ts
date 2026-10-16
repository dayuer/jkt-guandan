// 测试用的**确定性**伪随机数：固定种子 → 每次跑同一批手牌，红了能照着种子复现。
//
// 不是引擎的一部分 —— 引擎的随机源从外面注入（见 `deal.ts` 的 `Shuffler`），不自己掷骰子。
// 这里只是给性质测试喂数据。
import { newDeck, type Card, type Rank } from "../card";
import { isWild } from "../power";

export interface Rng {
  /** [0, 1) */
  next(): number;
  /** [0, n) 的整数 */
  int(n: number): number;
}

/**
 * mulberry32：小、快、够用，同样的种子给出同样的序列。
 * 用 `Math.imul`（ES2015）保证 32 位乘法不丢精度 —— 换别的写法在不同引擎上可能不一样。
 */
export function seededRandom(seed: number): Rng {
  let state = seed >>> 0;
  const next = (): number => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  return { next, int: (n) => Math.floor(next() * n) };
}

/** 一批随机手牌：牌从整副牌里不重复地抽，级数也随机；隔一手强制带一张逢人配（红桃级牌）。 */
export function sampleHands(count: number, seed = 20261016): { cards: Card[]; level: Rank }[] {
  const rng = seededRandom(seed);
  const deck = newDeck();
  const levels: Rank[] = [2, 3, 4, 5, 6, 7, 9, 10, 11, 13, 14]; // 打 A、打 2 也覆盖到
  const out: { cards: Card[]; level: Rank }[] = [];

  for (let i = 0; i < count; i++) {
    const level = levels[rng.int(levels.length)];
    const size = 1 + rng.int(10); // 1–10 张
    const used = new Set<number>();
    const cards: Card[] = [];

    if (i % 2 === 0) {
      const wild = deck.find((card) => isWild(card, level));
      if (wild) {
        cards.push(wild);
        used.add(wild.id);
      }
    }
    while (cards.length < size) {
      const card = deck[rng.int(deck.length)]; // 有空位就再抽，最多 10 张，一定抽得满
      if (used.has(card.id)) continue;
      used.add(card.id);
      cards.push(card);
    }
    out.push({ cards, level });
  }
  return out;
}
