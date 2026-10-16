// K4 的对比表（#253）：逻辑在 app/shared/src/conflict.ts，这里把**小游戏的时间写法**注进去。
// 小游戏在安卓微信上不一定有 `Intl`，所以时间是用 `lib/fmt` 自己算的 —— 共享层只收渲染好的文字。
//
// 从 scenes/detail.ts 挪到这里是为了能测：场景模块 import 了 wx / core/ui 一大串，
// 测试没法单独把它拉起来；这一层只依赖 fmt 和共享层。

import {
  conflictingFields,
  describeConflict as describeShared,
  type ConflictGame,
  type FieldDiff,
  type FieldLabels,
  type GamePatchLike,
} from "../../../shared/src/conflict";
import { dayLabel, kindLabel, timeHM } from "./fmt";

export { conflictingFields };
export type { ConflictGame, FieldDiff, GamePatchLike };

const conflictLabels: FieldLabels = {
  startsAt: (unixSeconds: number) => `${dayLabel(unixSeconds)} ${timeHM(unixSeconds)}`,
  kind: kindLabel,
};

/** 小游戏这边的时间写法。 */
export function describeConflict(patch: GamePatchLike, baseline: ConflictGame, latest: ConflictGame): FieldDiff[] {
  return describeShared(patch, baseline, latest, conflictLabels);
}
