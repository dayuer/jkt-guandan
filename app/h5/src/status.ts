/**
 * 状态提示（#114）—— H5 这一侧的入口。
 *
 * #253 P6.5 起**逻辑在 `app/shared/src/status.ts`**，而且那边的存储是**调用方给的**；
 * 这个文件只把「快照存在哪」按 H5 的默认值（`window.localStorage`）绑好再导出，
 * 所以 H5 这边的调用点和 `status.test.tsx` 的写法不用动。
 */

import {
  acknowledge as sharedAcknowledge,
  noticesFor as sharedNoticesFor,
  observe as sharedObserve,
  type GameView,
  type Notice,
  type NoticeKind,
} from "../../shared/src/status";
import type { KeyValueStore } from "../../shared/src/store";

export * from "../../shared/src/status";

/** 无痕模式等拿不到 localStorage 时返回 null：快照存不下就少一次提示，不报错。 */
function safeStorage(): KeyValueStore | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function noticesFor(
  v: GameView,
  meID: string | undefined,
  store: KeyValueStore | null = safeStorage(),
): Notice[] {
  return sharedNoticesFor(v, meID, store);
}

export function acknowledge(
  v: GameView,
  meID: string | undefined,
  kinds?: NoticeKind[],
  store: KeyValueStore | null = safeStorage(),
  now?: number,
): void {
  // now 不在这里兜底：不传就是 undefined，共享层那边的默认值（当前时间）接上。
  sharedAcknowledge(v, meID, kinds, store, now);
}

export function observe(
  v: GameView,
  meID: string | undefined,
  store: KeyValueStore | null = safeStorage(),
): Notice[] {
  return sharedObserve(v, meID, store);
}
