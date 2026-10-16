// 学牌（#106）：课本正文与查询从 app/shared 拿（#253 P6，不再反向 import app/h5），
// 这里只绑学习进度的存储默认值 —— 小游戏没有 localStorage，shim.ts 用 wx 的存储垫了一层。

import { doneLessons as readDone, markDone as writeDone, type LearnStore } from "../../../shared/src/learn";

export * from "../../../shared/src/learn";

function safeStorage(): LearnStore | null {
  try {
    return (globalThis as any).window?.localStorage ?? null;
  } catch {
    return null;
  }
}

export function doneLessons(storage: LearnStore | null = safeStorage()): Set<string> {
  return readDone(storage);
}

export function markDone(id: string, storage: LearnStore | null = safeStorage()): void {
  writeDone(id, storage);
}
