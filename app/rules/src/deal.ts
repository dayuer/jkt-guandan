import { newDeck, type Card } from "./card";

/**
 * 外部注入的随机源：给定 n，返回 0 到 n-1 的一个排列，每个数正好出现一次。
 *
 * 引擎自己不掷骰子：随机从外面来，测试才能复现，复盘和断线恢复才有意义（#204 的「随机源注入」）。
 * 生产上接一个真随机源，牌例和回归测试用一个写死的排列。
 */
export type Shuffler = (n: number) => readonly number[];

/**
 * 把一个现成的顺序当成随机源：**第 i 张发出去的是 deck[perm[i]]**。
 * 也就是 `perm[i]` 是「第 i 张发出的牌的 ID」，**不是**「ID 为 i 的牌排在第几个发」。
 * 方向见 testdata/rules/README.md「发牌」一节 —— 两端不许各写一种。
 *
 * 返回的随机源不改调用方的数组（同一个排列可以反复发牌），长度对不上时返回空数组，
 * 由 `deal` 报「随机源给的排列不对」。
 */
export function permShuffler(perm: readonly number[]): Shuffler {
  return (n) => (perm.length === n ? perm.slice() : []);
}

/** 每人 27 张（第二条 6：每名运动员 27 张）。 */
export const HANDS_EACH = 27;

/** 四家。 */
export const SEATS = 4;

/**
 * 洗牌、发牌：用 shuffle 排出 108 张的发牌顺序，从**首抓者**起**逆时针**轮流发给四家，
 * 每人每次一张，发完为止（第七条 一、5）。
 *
 * 返回值的下标是从首抓者起、逆时针数第几家（0–3），**不是座位号** —— 首抓者坐哪个座位由
 * 局面决定（掷骰、抽牌之类），调用方拿到之后自己按首抓者的座位旋转（testdata/rules/README.md
 * 「发牌」一节）。手里的牌按发到的先后排，不排序（排序是界面的事，引擎按 ID 认牌）。
 *
 * 随机源不合法（没有、长度为 0、不是排列、有重复或越界）时抛错，而不是悄悄发一副坏牌 ——
 * 复盘时对不上要立刻知道。
 */
export function deal(shuffle: Shuffler | null | undefined): Card[][] {
  if (typeof shuffle !== "function") {
    throw new Error("发牌：没有随机源");
  }
  const deck = newDeck();
  const perm = checkedPerm(shuffle(deck.length), deck.length);

  const hands: Card[][] = Array.from({ length: SEATS }, () => []);
  perm.forEach((index, i) => {
    hands[i % SEATS].push(deck[index]);
  });
  return hands;
}

/** 确认 perm 是 0..n-1 的一个排列。 */
function checkedPerm(perm: readonly number[] | null | undefined, n: number): readonly number[] {
  if (!Array.isArray(perm)) {
    throw new Error("发牌：随机源没有返回排列");
  }
  if (perm.length !== n) {
    throw new Error(`发牌：随机源给了 ${perm.length} 个位置，应该有 ${n} 个`);
  }
  const seen: boolean[] = new Array<boolean>(n).fill(false);
  for (const index of perm) {
    if (!Number.isInteger(index) || index < 0 || index >= n) {
      throw new Error(`发牌：随机源给的位置 ${index} 超出 0–${n - 1}`);
    }
    if (seen[index]) {
      throw new Error(`发牌：随机源把第 ${index} 张牌给了两次`);
    }
    seen[index] = true;
  }
  return perm;
}
