// 测试用的小工具：把牌例里的写法变成整副牌里的那一张（带 ID）。
//
// 牌例里写的是「花色+牌面/副号」，**不含 ID** —— ID 由 newDeck 的位置定（见 testdata/rules
// 的 README「牌的写法」），所以拿写法去索引同一副牌就行。
import { newDeck, parseKey, type Card, type CardFace } from "../card";

export function faceId(face: CardFace): string {
  return `${face.rank}/${face.suit}/${face.deck}`;
}

const BY_FACE = new Map<string, Card>(newDeck().map((card) => [faceId(card), card]));

/** 牌例里的一手牌 → 那一组牌（带 ID）。写法不在整副牌里就抛错，别静默当成别的牌。 */
export function handOf(written: readonly string[]): Card[] {
  return written.map((key) => {
    const card = BY_FACE.get(faceId(parseKey(key)));
    if (!card) throw new Error(`牌例里的 ${key} 不在整副牌里`);
    return card;
  });
}
