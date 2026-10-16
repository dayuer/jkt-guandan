/**
 * 浏览器侧的接线（#253）：`RealtimeClient` 本体搬进了 `app/shared/src/realtime.ts`，这里只留
 * **只有浏览器才有**的那几件 —— 真的 `WebSocket`、`window` 的定时器、`document` 的前后台事件
 * —— 和全站共用的那一个单例。
 *
 * 页面的用法不变（还是 `import { realtime } from "./realtime"`），所以调用点一行都不用动；
 * 小游戏不用这一份，它用 `wx.connectSocket` / `wx.onShow`，在
 * `app/minigame/src/lib/realtime.ts` 里自己接。
 *
 * 没连上时的细节（退避、降级轮询、按版本号去重）全在共享层，见那个文件的头注释。
 */

import { RealtimeClient, type RealtimeDeps, type SocketLike } from "../../shared/src/realtime";

export * from "../../shared/src/realtime";

function defaultDeps(): RealtimeDeps {
  return {
    createSocket: (url) => new WebSocket(url) as unknown as SocketLike,
    setTimeout: (fn, ms) => window.setTimeout(fn, ms),
    clearTimeout: (id) => window.clearTimeout(id as number),
    setInterval: (fn, ms) => window.setInterval(fn, ms),
    clearInterval: (id) => window.clearInterval(id as number),
    random: Math.random,
    isVisible: () => document.visibilityState === "visible",
    onVisibilityChange: (fn) => {
      document.addEventListener("visibilitychange", fn);
      return () => document.removeEventListener("visibilitychange", fn);
    },
  };
}

/** 同源推导：https 用 wss，其余用 ws。 */
function defaultURL(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

let shared: RealtimeClient | null = null;

/** 全站共用的那一个连接。第一次调用时建立。 */
export function realtime(): RealtimeClient {
  if (!shared) {
    shared = new RealtimeClient({ url: defaultURL(), deps: defaultDeps() });
    shared.start();
  }
  return shared;
}
