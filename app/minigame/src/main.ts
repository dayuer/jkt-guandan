// 小游戏入口：一期约局的全部页面（#85），画在一张画布上。
import "./shim";
import { installSelftest, runSelftest } from "./selftest";
import { App, type Scene } from "./core/app";
import { C, Ctx, circle, font, text } from "./core/ui";
import { API_BASE } from "./lib/api";
import { flush as flushTelemetry } from "./lib/telemetry";
import { realtime, reconnectRealtime } from "./lib/realtime";
import { boot, currentUser, onSessionChange } from "./lib/session";
import { Create } from "./scenes/create";
import { Detail } from "./scenes/detail";
import { Friends } from "./scenes/friends";
import { Home, TAB_H } from "./scenes/home";
import { Learn, LessonScene } from "./scenes/learn";
import { Devices, Me, Privacy } from "./scenes/me";
import type { Nav, TabName } from "./scenes/nav";

if (__DEV__ && __SELFTEST__) installSelftest();
const app = new App();
const tabs: Partial<Record<TabName, Scene>> = {};

const nav: Nav = {
  detail: (code) => app.push(new Detail(app, code, nav)),
  create: () => app.push(new Create(app, nav)),
  created: (code) => {
    app.reset([app.stack[0], new Detail(app, code, nav)]);
  },
  tab: (name) => app.reset([tabScene(name)]),
  lesson: (id) => {
    const s = new LessonScene(app, id, nav);
    if (app.top instanceof LessonScene) { app.stack[app.stack.length - 1] = s; app.redraw(); }
    else app.push(s);
  },
  devices: () => app.push(new Devices(app, nav)),
  privacy: () => app.push(new Privacy(app, nav)),
  back: () => (app.stack.length > 1 ? app.pop() : nav.tab("games")),
};

function tabScene(name: TabName): Scene {
  if (!tabs[name]) {
    tabs[name] = name === "games" ? new Home(app, nav) : name === "learn" ? new Learn(app, nav) : name === "friends" ? new Friends(app, nav) : new Me(app, nav);
  }
  return tabs[name]!;
}

// ── 底部 Tab（#107）：牌局、学牌、约局（蛋）、牌友、我的 ──
const TABS: [TabName | "create", string, string][] = [
  ["games", "♠", "牌局"], ["learn", "♣", "学牌"], ["create", "", "约局"], ["friends", "♦", "牌友"], ["me", "♥", "我的"],
];
app.drawTabs = (ctx: Ctx, active: string) => {
  const W = app.W, y = app.H - TAB_H - app.safeBottom, tw = W / 5;
  ctx.fillStyle = C.card;
  ctx.fillRect(0, y, W, TAB_H + app.safeBottom);
  ctx.fillStyle = C.line;
  ctx.fillRect(0, y, W, 1);
  TABS.forEach(([key, ic, label], i) => {
    const cx = tw * i + tw / 2;
    if (key === "create") {
      const cy = y + 6, pressed = app.isPressed("tab:create");
      circle(ctx, cx, cy, 31, C.card);
      circle(ctx, cx, cy, pressed ? 25 : 27, pressed ? C.redDeep : C.red);
      text(ctx, "蛋", cx, cy + 1, font(24, "900", true), C.cream, "center");
      if (realtime.status === "online") circle(ctx, cx + 20, cy - 19, 5, C.green, C.card, 2);
      text(ctx, label, cx, y + 46, font(11), C.ink3, "center");
      app.hit("tab:create", cx - 34, cy - 34, 68, 90, () => nav.create());
      return;
    }
    const on = active === key;
    text(ctx, ic, cx, y + 20, font(22), on ? C.red : C.ink3, "center");
    text(ctx, label, cx, y + 42, font(11, on ? "700" : "normal"), on ? C.red : C.ink3, "center");
    app.hit("tab:" + key, tw * i, y, tw, TAB_H, () => { if (!on) nav.tab(key as TabName); });
  });
};

// ── 启动 ──
nav.tab("games");
realtime.start();

let lastUser = currentUser()?.id ?? "";
onSessionChange(() => {
  const id = currentUser()?.id ?? "";
  if (id !== lastUser) {
    lastUser = id;
    reconnectRealtime();
    // 身份变了：Tab 页按新身份重拉
    for (const s of app.stack) s.onResume?.();
    flushTelemetry(); // 没登录时攒下的埋点
  }
  app.redraw();
});
void boot();

// 从转发卡片进来：query 带牌局短码 g（冷启动和热启动都要处理）
// 切回前台时 onShow 还会带着上一次的 query，同一个短码只进一次。
let entered = "";
function enter(query: any) {
  const code = query && (query.g || query.code);
  if (code && code !== entered && /^[0-9A-Za-z]{3,12}$/.test(code)) {
    entered = code;
    app.reset([tabScene("games"), new Detail(app, code, nav)]);
  }
}
enter(wx.getLaunchOptionsSync().query);
wx.onShow((res: any) => enter(res && res.query));

// 右上角「···」转发：首页卡片
wx.showShareMenu({ withShareTicket: false, menus: ["shareAppMessage", "shareTimeline"] });
wx.onShareAppMessage(() => ({ title: "随时掼掼蛋 · 找人凑一桌，就差你一个" }));

if (__DEV__) {
  console.log("[guandan] API", API_BASE);
  // 只在开发版：无界面测试脚本（按热区 id 点按）要拿到它
  GameGlobal.__app = app;
  GameGlobal.__nav = nav;
  if (__SELFTEST__) runSelftest();
}
