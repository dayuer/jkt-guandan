// 诊断用的自检（只在 `node build.mjs --dev --selftest` 打出来的包里）：
// 把报错、触摸、场景状态发到本机 127.0.0.1:18675 的日志接收端，启动后自动切一遍 Tab。
const SINK = "http://127.0.0.1:18675/";
function send(kind: string, data: unknown) {
  try {
    wx.request({ url: SINK, method: "POST", data: JSON.stringify({ kind, data }), header: { "content-type": "text/plain" } });
  } catch { /* ignore */ }
}
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
  wx.onTouchEnd = (fn: any) => origEnd((e: any) => {
    try { fn(e); } catch (err: any) { send("touchend-throw", { message: err?.message, stack: err?.stack }); throw err; }
  });
}

export function runSelftest() {
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
