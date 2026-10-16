/**
 * 「能存字符串的本机存储」（#253 P6.5）：共享层里两个要记本地进度的模块
 * （`status.ts` 的局快照、`learn.ts` 的学习进度）用同一个类型。
 *
 * 为什么是类型、不是默认值：H5 用 `window.localStorage`；小游戏没有 `localStorage`，
 * 靠 `app/minigame/src/shim.ts` 用 wx 的存储垫了一层。**默认值留在各自的壳里**，
 * 共享层只声明自己要什么 —— 这样共享层里不会出现 `window` / `wx`，
 * 也不用 DOM 的 `Storage` 类型（`app/shared/tsconfig.json` 只用得到 ES 的 lib）。
 */

export interface KeyValueStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** 只读的那种：比对快照、读进度时用不到写。 */
export type StoreReader = Pick<KeyValueStore, "getItem">;
