/**
 * 试用数据：埋点和前端错误上报（#117，server-design §6「埋点」）。
 *
 * - `track(name, props)` 先进队列，攒够 20 条、或者 5 秒后、或者页面要被收起（pagehide /
 *   切到后台）时一批发出去。收起时用 `fetch(keepalive)`，不支持再退回 `sendBeacon`——
 *   普通 fetch 在页面卸载时会被浏览器掐掉。
 * - 只发白名单里的事件名和两个 props 键（`game` 短码、`channel`），不采集个人信息；
 *   服务端也会再筛一遍。
 * - 埋点失败不重试、不打扰用户：它是锦上添花，不能拖慢或弄坏页面。
 * - 全局 `error` / `unhandledrejection` 报到 `/api/client-errors`，每次打开页面最多 3 条、
 *   同一条只报一次；`url` 只留路径（查询串里可能有认领 token）。
 */
import { inAppBrowser, isStandalone } from "./push";

export type EventName =
  | "detail_view"
  | "join_click"
  | "join_ok"
  | "share_open"
  | "share_channel"
  | "push_prompt"
  | "push_ok"
  | "jumpout";

export type ShareChannel = "whatsapp" | "wechat" | "copy" | "system" | "poster";

export interface EventProps {
  game?: string;
  channel?: ShareChannel;
}

export type Env = "wechat" | "whatsapp" | "browser" | "pwa";

/** 服务端只认这四种；Instagram / Facebook / LINE 这些内置浏览器归到 browser。 */
export function detectEnv(userAgent: string = navigator.userAgent, win: Window = window): Env {
  const app = inAppBrowser(userAgent);
  if (app === "wechat" || app === "whatsapp") return app;
  if (isStandalone(win)) return "pwa";
  return "browser";
}

const MAX_BATCH = 20;
const FLUSH_MS = 5000;
const MAX_ERRORS = 3;
/** 被限流（429）后安静地歇这么久再发。 */
const BACKOFF_MS = 60_000;
/** 歇着的时候最多攒这么多条，再多就丢最早的：埋点不值得占内存。 */
const MAX_QUEUE = 60;

interface Queued {
  name: EventName;
  props?: EventProps;
}

let queue: Queued[] = [];
let timer: ReturnType<typeof setTimeout> | null = null;
let env: Env | null = null;
let pausedUntil = 0;

function currentEnv(): Env {
  if (!env) {
    try {
      env = detectEnv();
    } catch {
      env = "browser";
    }
  }
  return env;
}

/** 记一条埋点。 */
export function track(name: EventName, props?: EventProps): void {
  queue.push(props ? { name, props } : { name });
  if (queue.length > MAX_QUEUE) queue = queue.slice(-MAX_QUEUE);
  if (queue.length >= MAX_BATCH) {
    flush();
    return;
  }
  if (!timer) timer = setTimeout(() => flush(), FLUSH_MS);
}

/** 把队列发出去。`unloading` 为真时走 keepalive / sendBeacon。 */
export function flush(unloading = false): void {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  // 刚被限流：页面还在就等歇够了再发；页面要走了就算了，不硬发。
  const wait = pausedUntil - Date.now();
  if (wait > 0) {
    if (unloading) queue = [];
    else if (queue.length > 0) timer = setTimeout(() => flush(), wait);
    return;
  }
  while (queue.length > 0) {
    const batch = queue.slice(0, MAX_BATCH);
    queue = queue.slice(MAX_BATCH);
    send("/api/events", JSON.stringify({ env: currentEnv(), events: batch }), unloading);
  }
}

function send(url: string, body: string, unloading: boolean): void {
  const onResponse = (resp: Response) => {
    if (resp.status === 429) pausedUntil = Date.now() + BACKOFF_MS;
  };
  const init: RequestInit = {
    method: "POST",
    // CSRF 中间件要求会改数据的请求带 JSON 头（§6）。
    headers: { "content-type": "application/json" },
    body,
    keepalive: unloading,
  };
  try {
    void fetch(url, init).then(onResponse, () => {});
  } catch {
    // 老内核不认 keepalive 时 fetch 会同步抛错：页面要走了，交给 sendBeacon。
    if (unloading && typeof navigator.sendBeacon === "function") {
      navigator.sendBeacon(url, new Blob([body], { type: "application/json" }));
    }
  }
}

let errorsSent = 0;
const seen = new Set<string>();

/** 报一条前端错误。 */
export function reportError(message: string, stack?: string): void {
  const key = message.slice(0, 300);
  if (!key || seen.has(key) || errorsSent >= MAX_ERRORS) return;
  seen.add(key);
  errorsSent++;
  send(
    "/api/client-errors",
    JSON.stringify({
      env: currentEnv(),
      message: key,
      stack: (stack ?? "").slice(0, 2000),
      url: window.location.pathname,
    }),
    false,
  );
}

let installed = false;

/** 页面启动时调一次：收起页面时把埋点发掉，全局错误报上去。 */
export function installTelemetry(): void {
  if (installed) return;
  installed = true;
  window.addEventListener("pagehide", () => flush(true));
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flush(true);
  });
  window.addEventListener("error", (e: ErrorEvent) => {
    // 图片、脚本加载失败也会冒成 error 事件，但没有 message；那些不是代码错误。
    if (!e.message) return;
    reportError(e.message, e.error instanceof Error ? e.error.stack : undefined);
  });
  window.addEventListener("unhandledrejection", (e: PromiseRejectionEvent) => {
    const reason = e.reason;
    if (reason instanceof Error) reportError(reason.message, reason.stack);
    else reportError(String(reason));
  });
}

/** 仅测试用。 */
export function resetTelemetry(): void {
  if (timer) clearTimeout(timer);
  timer = null;
  queue = [];
  env = null;
  pausedUntil = 0;
  errorsSent = 0;
  seen.clear();
}
