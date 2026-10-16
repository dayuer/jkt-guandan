// 诊断用的自检（只在 `node build.mjs --dev --selftest` 打出来的包里）：
// 把报错、触摸、场景状态发到本机 127.0.0.1:18675 的日志接收端，启动后自动切一遍 Tab。
// 端口由构建时定（build.mjs 的 SELFTEST_SINK_PORT），默认 18675 —— 两个会话同时跑自检时
// 各自换一个，免得把对方的日志和截图收走。
declare const __SELFTEST_SINK__: string;
const SINK = typeof __SELFTEST_SINK__ !== "undefined" ? __SELFTEST_SINK__ : "http://127.0.0.1:18675/";
function send(kind: string, data: unknown) {
  try {
    wx.request({ url: SINK, method: "POST", data: JSON.stringify({ kind, data }), header: { "content-type": "text/plain" } });
  } catch { /* ignore */ }
}
import { runAccept165 } from "./accept165";
import { runAccept210 } from "./accept210";
import { runAccept211 } from "./accept211";

const g: any = typeof GameGlobal !== "undefined" ? GameGlobal : globalThis;


/** 必须在 new App() 之前调：要先包住 wx.onTouchStart。 */
export function installSelftest() {
  send("env", {
    window: typeof g.window, sameAsGlobal: g.window === g, ls: (() => { try { return typeof g.window?.localStorage; } catch (e) { return "throws:" + e; } })(),
    intl: typeof Intl, sys: (() => { try { const s = wx.getSystemInfoSync(); return { platform: s.platform, SDKVersion: s.SDKVersion, w: s.windowWidth, h: s.windowHeight }; } catch (e) { return String(e); } })(),
  });
  wx.onError?.((e: any) => send("onError", { message: e?.message ?? String(e), stack: e?.stack }));
  const origErr = console.error;
  console.error = (...a: any[]) => { send("console.error", a.map(String)); origErr.apply(console, a); };
  // 包一层触摸回调：记下坐标、命中的热区和异常
  const origStart = wx.onTouchStart, origEnd = wx.onTouchEnd;
  wx.onTouchStart = (fn: any) => origStart((e: any) => {
    const t = e.touches?.[0];
    const app = g.__app;
    let hit: string | null = null;
    try { if (app && t) for (let i = app.hits.length - 1; i >= 0; i--) { const h = app.hits[i]; if (t.clientX >= h.x && t.clientX <= h.x + h.w && t.clientY >= h.y && t.clientY <= h.y + h.h) { hit = h.id; break; } } } catch { /* ignore */ }
    send("touchstart", { x: t?.clientX, y: t?.clientY, keys: t ? Object.keys(t) : [], hit });
    try { fn(e); } catch (err: any) { send("touchstart-throw", { message: err?.message, stack: err?.stack }); throw err; }
  });
  // 键盘：小游戏里没法直接改画布上的输入框，只能像真机那样派发输入事件。
  // 回调记在 GameGlobal.__kb 上（installSelftest 在 new App() 之前跑，所以先包住、后注册）。
  const kb: any = {};
  g.__kb = kb;
  const origKbInput = wx.onKeyboardInput, origKbConfirm = wx.onKeyboardConfirm;
  if (origKbInput) wx.onKeyboardInput = (fn: any) => { kb.input = fn; origKbInput(fn) };
  if (origKbConfirm) wx.onKeyboardConfirm = (fn: any) => { kb.confirm = fn; origKbConfirm(fn) };
  wx.onTouchEnd = (fn: any) => origEnd((e: any) => {
    try { fn(e); } catch (err: any) { send("touchend-throw", { message: err?.message, stack: err?.stack }); throw err; }
  });
}

declare const __SELFTEST_SCENARIO__: string;

export function runSelftest() {
  // 走查场景：`SELFTEST_SCENARIO=165`（#165 验收 S1–S12）、`=210`（#210 改称呼）、`=211`（#211 我的牌局）构建时编进来，
  // 否则跑原来的切 Tab 自检。截图都从游戏自己导，文件名带场景号，两个场景可以共用同一个接收端。
  if (typeof __SELFTEST_SCENARIO__ !== "undefined" && __SELFTEST_SCENARIO__ !== "") {
    const scene = __SELFTEST_SCENARIO__;
    const shotOf = (label: string) => {
      try {
        const path = g.__app.canvas.toTempFilePathSync({ fileType: "png" });
        wx.uploadFile({ url: SINK + "?name=" + encodeURIComponent(scene + "-" + label.replace(/\W+/g, "-")), filePath: path, name: "f" });
      } catch (err: any) { send("shot-throw", { label, message: err?.message }); }
    };
    if (scene === "165") { runAccept165({ send, shot: shotOf, sink: SINK }); return }
    if (scene === "210") { runAccept210({ send, shot: shotOf }); return }
    if (scene === "211") { runAccept211({ send, shot: shotOf }); return }
    send("scenario-unknown", { scene });
    return;
  }
  const app = g.__app, nav = g.__nav;
  const shot = (label: string) => {
    try {
      const path = app.canvas.toTempFilePathSync({ fileType: "png" });
      wx.uploadFile({ url: SINK + "?name=" + encodeURIComponent(label.replace(/\W+/g, "-")), filePath: path, name: "f" });
    } catch (err: any) { send("shot-throw", { label, message: err?.message }); }
  };
  const state = (label: string) => { shot(label); send("state", {
    label, stack: app.stack.map((s: any) => s.constructor.name), sheets: app.sheets.length,
    hits: app.hits.map((h: any) => h.id).slice(0, 40),
  }); };
  const steps: [string, () => void][] = [
    ["start", () => {}],
    ["tab learn", () => nav.tab("learn")],
    ["tab friends", () => nav.tab("friends")],
    ["tab me", () => nav.tab("me")],
    ["tap tab:learn hit", () => app.hits.find((h: any) => h.id === "tab:learn")?.onTap()],
    ["tab games", () => nav.tab("games")],
  ];
  let i = 0;
  const next = () => {
    if (i >= steps.length) return;
    const [label, fn] = steps[i++];
    try { fn(); } catch (err: any) { send("step-throw", { label, message: err?.message, stack: err?.stack }); }
    setTimeout(() => { state(label); next(); }, 1500);
  };
  setTimeout(next, 2500);
}
