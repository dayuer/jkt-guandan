// #210 走查：改称呼前后「我的」页（只在 `--selftest` 包里、且 SELFTEST_SCENARIO=210 时编进来）。
//
// 驱动方式和 accept165 一样：热区按 id 找到再调 `onTap()`，画布由游戏自己导出。
//
// 打字那一步是**真的走键盘回调**：selftest.ts 把 `wx.onKeyboardInput` 的回调记在
// `GameGlobal.__kb` 上，这里像真机那样派发一次输入事件。不这么做（直接改内部变量）
// 就绕过了 onChange → 保存 这条链，截图好看但测不到东西。
import { getToken, setToken, wxLogin } from "./lib/api";
import { boot, wxCode } from "./lib/session";

declare const __API_BASE__: string;

interface Hits { id: string; x: number; y: number; w: number; h: number; onTap: (() => void) | null }

export interface AcceptCtx {
  send: (kind: string, data: unknown) => void;
  shot: (label: string) => void;
}

const g: any = typeof GameGlobal !== "undefined" ? GameGlobal : globalThis;

// 用固定账号：服务端对同一个 IP 的「创建身份」有频率限制（rate_limited），每轮新建会建不出来。
const OPENID = "acc-me";

export function runAccept210(ctx: AcceptCtx) {
  const app = g.__app, nav = g.__nav;
  const { send, shot } = ctx;
  const kb: any = g.__kb || {};

  const state = (label: string) => {
    shot(label);
    send("state", {
      label,
      stack: app.stack.map((s: any) => s.constructor.name),
      sheets: app.sheets.length,
      hits: app.hits.map((h: Hits) => h.id).slice(0, 40),
    });
  };

  const tap = (id: string) => {
    const hit = (app.hits || []).slice().reverse().find((h: Hits) => h.id === id);
    if (!hit) { send("tap-miss", { id, have: (app.hits || []).map((h: Hits) => h.id).slice(0, 30) }); return false }
    if (!hit.onTap) { send("tap-disabled", { id }); return false }
    try { hit.onTap(); app.redraw(); return true } catch (e: any) { send("tap-throw", { id, message: e?.message }); return false }
  };

  // 等一下热区出现且能用再往下（页面和弹层都是异步的）。
  const waitHit = (id: string, ms = 15000) =>
    new Promise<void>((res) => {
      const t0 = Date.now();
      const tick = () => {
        const h = (app.hits || []).slice().reverse().find((x: Hits) => x.id === id);
        if ((h && h.onTap) || Date.now() - t0 > ms) return res();
        setTimeout(tick, 250);
      };
      tick();
    });

  /** 像真机一样往当前输入框里打字（走 wx.onKeyboardInput 的回调）。 */
  const type = (text: string) => {
    if (!kb.input) { send("kb-missing", { have: Object.keys(kb) }); return }
    // 没点过输入框时 `editing` 是空的，这一下就**什么都不会发生**（键盘回调里有这个判断）。
    // 报出来，免得页面没变还当成功了。
    send("typed", { editing: app.editing?.key || null });
    kb.input({ value: text });
    app.redraw();
  };

  const steps: [string, () => any, string?][] = [];

  steps.push(["prepare", async () => {
    setToken("");
    try { wx.setStorageSync("gd_dev_openid", OPENID) } catch { /* ignore */ }
    const r = await wxLogin(await wxCode(), "阿杰");
    if (r.user && r.device_token) setToken(r.device_token);
    else send("login-fail", { body: JSON.stringify(r).slice(0, 160) });
    await boot();
    nav.tab("me");
  }, "edit-nick"]);

  // 改之前：「我的」页顶部那张卡片，右侧有「改」。
  steps.push(["me-before", () => {}, "edit-nick"]);
  // 点进去：和「绑定 WhatsApp」同一套弹层。
  steps.push(["me-sheet", () => { tap("edit-nick") }, "field:nick"]);
  // 点输入框：真机上这一步会把原生键盘拉起来（`app.edit()` 在这里登记 onChange）。
  // 不点就直接打字的话，键盘回调里 `editing` 还是空的，等于什么都没发生。
  steps.push(["me-focus", () => { tap("field:nick") }]);
  // 输入框里打字（值整个换掉）。
  steps.push(["me-typed", () => { type("老张") }, "sb:0"]);
  // 保存。
  steps.push(["me-saved", () => { tap("sb:0") }]);
  // 保存之后的「我的」页：顶部是新称呼。
  steps.push(["me-after", () => {}, "edit-nick"]);

  // 服务端也确认一遍（页面缓存刷新了，但真值在服务端）。
  steps.push(["server-check", async () => {
    const me = await new Promise<any>((resolve) => {
      wx.request({
        url: __API_BASE__ + "/api/me", method: "GET" as any,
        header: getToken() ? { authorization: "Bearer " + getToken() } : {},
        success: (r: any) => resolve(r.data), fail: (e: any) => resolve({ fail: JSON.stringify(e) }),
      });
    });
    send("server-nickname", { nickname: me?.user?.nickname, raw: JSON.stringify(me).slice(0, 120) });
  }]);

  let i = 0;
  const next = () => {
    if (i >= steps.length) { send("done", { steps: steps.length }); return }
    const [label, fn, waitFor] = steps[i++];
    Promise.resolve()
      .then(fn)
      .catch((e: any) => send("step-throw", { label, message: e?.message, stack: e?.stack }))
      .then(async () => {
        if (waitFor) await waitHit(waitFor);
        setTimeout(() => { state(label); next() }, 700);
      });
  };
  setTimeout(next, 3000);
}
