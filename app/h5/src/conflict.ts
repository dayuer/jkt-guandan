/**
 * 局头改牌局时的版本冲突（§5.1、§11.3，原型 K4 / K5 / K6）—— H5 这一侧的入口。
 *
 * #253 之后**逻辑搬去了 `app/shared/src/conflict.ts`**（小游戏也不用再反向 import H5）。
 * 这个文件留一层薄封装：把共享入口按 **H5 的写法**（`./format` 的 `dayLabel` / `timeHM` /
 * `kindLabel`）绑好再导出，所以 H5 这边的调用点和 `conflict.test.ts` 一行都不用动。
 *
 * 时间的写法是各端自己的事：H5 用 `Intl` + Asia/Jakarta，小游戏用 `src/lib/fmt.ts`
 * （安卓微信不一定有 `Intl`），共享层只接收渲染好的文字。
 */

import { dayLabel, kindLabel, timeHM } from "./format";
import {
  describeConflict as describeShared,
  type ConflictGame,
  type FieldLabels,
  type GamePatchLike,
} from "../../shared/src/conflict";

export {
  conflictingFields,
  patchedFields,
  type ConflictGame,
  type FieldDiff,
  type FieldLabels,
  type GamePatchLike,
} from "../../shared/src/conflict";

/** H5 这边的写法：时间带「今天 / 明天 / 10月18日」。 */
const labels: FieldLabels = {
  startsAt: (unixSeconds: number) => `${dayLabel(unixSeconds)} ${timeHM(unixSeconds)}`,
  kind: kindLabel,
};

/** K4 的对比表（H5 的写法）。 */
export function describeConflict(
  patch: GamePatchLike,
  baseline: ConflictGame,
  latest: ConflictGame,
): ReturnType<typeof describeShared> {
  return describeShared(patch, baseline, latest, labels);
}
