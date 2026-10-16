// 把仓库根 testdata/rules/ 的牌例读进来（#87 第 1 步）。
//
// 只有这个文件碰文件系统（走 import），校验全在 fixtures.ts 里 —— 那边不依赖牌例文件，
// 单测跑得起来。这个文件在 #213 把 testdata/rules/ 合进 main 之前，类型检查是过不了的。
import dealRaw from "../../../../testdata/rules/deal.json";
import deckRaw from "../../../../testdata/rules/deck.json";
import powerRaw from "../../../../testdata/rules/power.json";
import { readDeal, readDeck, readPower } from "./fixtures";

export const deckFixture = readDeck(deckRaw);
export const powerFixture = readPower(powerRaw);
export const dealFixture = readDeal(dealRaw);
