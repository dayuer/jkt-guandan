// 试用埋点（#117）：和 H5 同一套事件名，env 记为 minigame。失败不重试、不打扰用户。

import { getToken, request } from "./api";

export type EventName = "detail_view" | "join_click" | "join_ok" | "share_open" | "share_channel" | "push_prompt" | "push_ok";
export type ShareChannel = "wechat" | "copy" | "poster" | "whatsapp" | "system";

let queue: { name: EventName; props?: Record<string, string> }[] = [];
let timer: any = null;

export function track(name: EventName, props?: { game?: string; channel?: ShareChannel }): void {
  queue.push(props ? { name, props: props as Record<string, string> } : { name });
  if (queue.length > 60) queue = queue.slice(-60);
  if (queue.length >= 20) flush();
  else if (!timer) timer = setTimeout(flush, 5000);
}

export function flush(): void {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  // 小游戏没有 Origin，没登录时写请求会被 CSRF 拦下（#84）：先攒着，登录后再发
  if (!queue.length || !getToken()) return;
  const events = queue;
  queue = [];
  request("POST", "/api/events", { body: { env: "minigame", events } }).catch(() => {});
}

let errors = 0;
const seen = new Set<string>();
export function reportError(message: string, stack?: string) {
  const key = message.slice(0, 200);
  if (errors >= 3 || seen.has(key) || !getToken()) return;
  errors++;
  seen.add(key);
  request("POST", "/api/client-errors", {
    body: { env: "minigame", message: key, stack: (stack || "").slice(0, 2000), url: "minigame" },
  }).catch(() => {});
}

wx.onHide(() => flush());
if (wx.onError) wx.onError((e: any) => reportError(String(e?.message ?? e), e?.stack));
if (wx.onUnhandledRejection) wx.onUnhandledRejection((e: any) => reportError(String(e?.reason?.message ?? e?.reason ?? "rejection"), e?.reason?.stack));
