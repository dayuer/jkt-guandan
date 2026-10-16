// #211 走查：「我的」页的我的牌局、历史删除、设置页（只在 `--selftest` 包里、且 SELFTEST_SCENARIO=211 时编进来）。
//
// 数据先用 docs/verify/211-my-games/seed.sh 造好（「阿杰」三局要打的、三局历史）。
// 驱动方式和 accept210 一样：热区按 id 找到再调 `onTap()`，画布由游戏自己导出。
import { getToken, setToken, wxLogin } from "./lib/api";
import { boot, signOutHere, wxCode } from "./lib/session";

declare const __API_BASE__: string;

interface Hits { id: string; x: number; y: number; w: number; h: number; onTap: (() => void) | null }

export interface AcceptCtx {
  send: (kind: string, data: unknown) => void;
  shot: (label: string) => void;
}

const g: any = typeof GameGlobal !== "undefined" ? GameGlobal : globalThis;

export function runAccept211(ctx: AcceptCtx) {
  const app = g.__app, nav = g.__nav;
  const { send, shot } = ctx;

  const hits = (): Hits[] => (app.hits || []).slice().reverse();
  const state = (label: string) => {
    shot(label);
    send("state", {
      label,
      stack: app.stack.map((s: any) => s.constructor.name),
      sheets: app.sheets.length,
      hits: app.hits.map((h: Hits) => h.id).slice(0, 50),
    });
  };
  const tap = (pred: (id: string) => boolean, what: string) => {
    const hit = hits().find((h) => pred(h.id));
    if (!hit) { send("tap-miss", { what, have: hits().map((h) => h.id).slice(0, 40) }); return false }
    if (!hit.onTap) { send("tap-disabled", { what }); return false }
    try { hit.onTap(); app.redraw(); return true } catch (e: any) { send("tap-throw", { what, message: e?.message }); return false }
  };
  const until = (ok: () => boolean, ms = 15000) => new Promise<void>((res) => {
    const t0 = Date.now();
    const tick = () => (ok() || Date.now() - t0 > ms ? res() : setTimeout(tick, 250));
    tick();
  });
  /** 「我的」页的牌局读回来了（要打的、历史都不是 null）。 */
  const loaded = () => { const t = app.top; return t?.tab === "me" && t.upcoming !== null && t.history !== null };
  const scrollTo = (ratio: number) => { const s = app.top.scroll; s.pos = Math.round(s.max * ratio); app.redraw() };
  const login = async (openid: string, nickname: string) => {
    setToken("");
    try { wx.setStorageSync("gd_dev_openid", openid) } catch { /* ignore */ }
    const r = await wxLogin(await wxCode(), nickname);
    if (r.user && r.device_token) setToken(r.device_token);
    else send("login-fail", { body: JSON.stringify(r).slice(0, 160) });
    await boot();
  };
  const reenter = async () => { nav.tab("games"); await new Promise((r) => setTimeout(r, 300)); nav.tab("me"); await until(loaded) };
  const serverHistory = () => new Promise<any>((resolve) => {
    wx.request({
      url: __API_BASE__ + "/api/me/games/history", method: "GET" as any,
      header: getToken() ? { authorization: "Bearer " + getToken() } : {},
      success: (r: any) => resolve(r.data), fail: (e: any) => resolve({ fail: JSON.stringify(e) }),
    });
  });

  const steps: [string, () => any][] = [];
  steps.push(["prepare", async () => { await login("acc-me", "阿杰"); await reenter() }]);
  // 顶部：称呼卡片 + 我的牌局·要打的（我开的 / 已报名·已成局 / 候补第 1 位）
  steps.push(["me-top", () => {}]);
  // 往下：历史（升级局比分、已取消、不升级局比分），每条右上角「删除」
  steps.push(["me-history", () => scrollTo(0.62)]);
  // 点第一条历史的「删除」→ 确认弹层
  steps.push(["hide-sheet", () => { tap((id) => id.startsWith("hide:"), "第一条历史的删除") }]);
  // 删掉 → toast，历史少一条
  steps.push(["hide-after", async () => {
    tap((id) => id === "sb:0", "删掉");
    await until(() => !app.sheets.length && (app.top.history?.length ?? 0) === 2);
  }]);
  steps.push(["server-check", async () => {
    const h = await serverHistory();
    send("server-history", { codes: (h?.games || []).map((x: any) => x.short_code), has_more: h?.has_more });
  }]);
  // 页底：「设置」和「隐私与数据」
  steps.push(["me-bottom", () => scrollTo(1)]);
  // 设置页：认领码、WhatsApp、网页版身份、登录的设备
  steps.push(["settings", async () => { tap((id) => id === "settings", "设置"); await until(() => app.top?.constructor?.name === "Settings") }]);
  // 点一张要打的牌局 → 详情
  steps.push(["detail", async () => {
    nav.back(); await until(loaded); scrollTo(0);
    // 返回有过场动画，热区要等这一帧画完才登记
    await until(() => hits().some((h) => h.id.startsWith("mine:")));
    // 按登记顺序取第一张（tap() 是倒着找的，会拿到最后一张历史）
    const first = (app.hits || []).find((h: Hits) => h.id.startsWith("mine:"));
    tap((id) => id === first?.id, "第一张要打的");
    await until(() => app.top?.constructor?.name !== "Me");
  }]);
  // 没有局的人：空状态 + 去找局 / 开一局
  steps.push(["me-empty", async () => { nav.back(); await login("acc-empty", "新来的"); await reenter(); scrollTo(0) }]);
  // 没有身份：只有「填个称呼」、设置、隐私
  steps.push(["me-guest", async () => { signOutHere(); await reenter() }]);
  steps.push(["settings-guest", async () => { tap((id) => id === "settings", "设置"); await until(() => app.top?.constructor?.name === "Settings") }]);

  let i = 0;
  const next = () => {
    if (i >= steps.length) { send("done", { steps: steps.length }); return }
    const [label, fn] = steps[i++];
    Promise.resolve()
      .then(fn)
      .catch((e: any) => send("step-throw", { label, message: e?.message, stack: e?.stack }))
      .then(() => setTimeout(() => { state(label); next() }, 900));
  };
  setTimeout(next, 3000);
}
