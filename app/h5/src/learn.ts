/**
 * 学牌（#106，设计稿 E1 / E2）—— H5 这一侧的入口。
 *
 * #253 P6 起**课本正文与查询搬去了 `app/shared/src/learn.ts`**（小游戏不用再反向 import H5）。
 * 这个文件留一层薄壳：把学习进度的存储入口按 **H5 的默认值**（`window.localStorage`）绑好再导出，
 * 所以 H5 这边的调用点（`pages/Learn.tsx`、`pages/Lesson.tsx`、`title.ts`）一行都不用动。
 */

import { doneLessons as readDone, markDone as writeDone } from "../../shared/src/learn";

export * from "../../shared/src/learn";

/** 无痕模式等拿不到 localStorage 时返回 null：进度存不下就当没学过，不报错。 */
function safeStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function doneLessons(storage: Storage | null = safeStorage()): Set<string> {
  return readDone(storage);
}

export function markDone(id: string, storage: Storage | null = safeStorage()): void {
  writeDone(id, storage);
}
