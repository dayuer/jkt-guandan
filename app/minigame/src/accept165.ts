// #165 验收走查（只在 `--selftest` 包里、且 SELFTEST_SCENARIO=165 时编进来）。
//
// 在**开发者工具模拟器**里把 S1–S12 自动走一遍，每一步把画布截图发到本机接收端
// （selftest.ts 的 SINK）。驱动方式：按钮都是每帧登记到 `app.hits` 的热区，按 id 找到
// 再调 `onTap()` —— 和 #189 那套无头 harness 是同一个思路，只是这次跑的是真模拟器。
//
// 为什么要自动化：`miniprogram-automator` **驱动不了小游戏**（能 launch，但 evaluate /
// screenshot / systemInfo 全部超时，包里也没有任何 game 相关代码）。所以驱动只能放在
// 游戏内部做，截图也由游戏自己 `toTempFilePathSync` 导出 —— 见 #165 验收记录。
import { setToken, wxLogin } from "./lib/api";
import { boot, wxCode } from "./lib/session";

declare const __API_BASE__: string;

interface Hits { id: string; x: number; y: number; w: number; h: number; onTap: (() => void) | null }

export interface AcceptCtx {
  send: (kind: string, data: unknown) => void;
  shot: (label: string) => void;
  sink: string;
}

// 四个身份用自己造的 dev 账号（开发模式 `dev:<openid>` 直接当 openid）。
//
// 固定五个 dev 账号。**不要每轮新建**：服务端对同一个 IP 的「创建身份」有频率限制
// （`rate_limited` 创建身份太频繁），连着跑几轮就只回 `{"need_nickname":true}` ——
// 建不出人、建不出局，现象是「整页只有 nav-back」，很难往这个方向想。
//
// 改用固定账号之后，每轮开头要**把他们手里的座位退掉**：一个人同时最多占 5 个位置，
// 不退的话跑几轮一样会 409 quota_exceeded（现象是「四人局只进来两个」）。
const OPENID = {
  host: "acc-laoWang", // 1 号 · 红方
  lily: "acc-lily", // 2 号 · 黑方
  me: "acc-me", // 3 号 · 红方（本机身份）
  zhou: "acc-xiaoZhou", // 4 号 · 黑方
  guest: "acc-guest", // 不在座位上的旁观者
};

// 汇报口：模块级函数（login）用得到，而 send 是 runAccept165 里才有的。
let report: (kind: string, data: unknown) => void = () => {};

const req = (method: string, path: string, body?: unknown, token?: string): Promise<any> =>
  new Promise((resolve, reject) => {
    wx.request({
      url: __API_BASE__ + path,
      method: method as any,
      data: body as any,
      header: { "content-type": "application/json", ...(token ? { authorization: "Bearer " + token } : {}) },
      success: (r: any) => {
        let d: any = r.data;
        if (typeof d === "string") { try { d = JSON.parse(d) } catch { /* 原样 */ } }
        resolve({ status: r.statusCode, body: d });
      },
      fail: (e: any) => reject(new Error("request 失败：" + JSON.stringify(e))),
    });
  });

const login = async (openid: string, nickname: string): Promise<string> => {
  const r = await req("POST", "/api/wx/login", { code: "dev:" + openid, nickname });
  const token = r.body?.device_token;
  // 建不出身份时服务端回 {"need_nickname":true}（新账号 + IP 频率限制）。不喊出来的话，
  // 后面只会在 `made.body.game.short_code` 上抛一个看不懂的 undefined 错。
  if (!token) report("login-fail", { openid, status: r.status, body: JSON.stringify(r.body).slice(0, 120) });
  return token || "";
};

export function runAccept165(ctx: AcceptCtx) {
  const g: any = typeof GameGlobal !== "undefined" ? GameGlobal : globalThis;
  const app = g.__app, nav = g.__nav;
  const { send, shot } = ctx;
  report = send;

  const state = (label: string) => {
    shot(label);
    send("state", {
      label,
      stack: app.stack.map((s: any) => s.constructor.name),
      sheets: app.sheets.length,
      hits: app.hits.map((h: Hits) => h.id).slice(0, 40),
    });
  };

  // 按 id 点一下。找不到 / 被禁用（onTap=null）都记下来 —— 静默失败会让后面的步骤全错位。
  const tap = (id: string) => {
    const list: Hits[] = app.hits || [];
    const hit = list.slice().reverse().find((h) => h.id === id);
    if (!hit) { send("tap-miss", { id, have: list.map((h) => h.id).slice(0, 30) }); return false }
    if (!hit.onTap) { send("tap-disabled", { id }); return false }
    // ⚠️ 点完**必须自己触发一次重绘**：真实触摸走的是 app 的 onTouchEnd，那里会 redraw；
    // 直接调 onTap() 绕过了那一步 —— 状态变了但画面还是上一帧，于是「点了没反应」，
    // 后面的 waitHit 也就永远等不到那个热区变成可用（卡在第三版的就是这个）。
    try { hit.onTap(); app.redraw(); return true } catch (e: any) { send("tap-throw", { id, message: e?.message }); return false }
  };

  // S15 的战报卡片：`shareReport()` 直接调 `wx.shareAppMessage({imageUrl: 渲染出来的卡片})`，
  // 没有弹层可以点。把这张卡截下来 —— 验收要看的正是它的版式。
  const origShare = (wx as any).shareAppMessage;
  (wx as any).shareAppMessage = (o: any) => {
    send("share", { title: o?.title, query: o?.query, hasImage: !!o?.imageUrl });
    if (o?.imageUrl) {
      try { wx.uploadFile({ url: ctx.sink + "?name=165-s15-report-card", filePath: o.imageUrl, name: "f" }) } catch (e: any) { send("card-throw", { message: e?.message }) }
    }
    if (origShare) origShare(o);
  };

  const code = g.__accept165Code || "";
  const steps: [string, () => any, string?][] = [];
  const step = (label: string, fn: () => any, waitFor?: string) => steps.push([label, fn, waitFor]);

  // 等到某个热区**出现且能用**再往下走。两个坑：
  //   1. 固定等 2 秒不够 —— 详情页、记分页、弹层都是异步加载的，机器一忙就截到「还在转圈」
  //      的那一帧（第一版一整轮截图全是空页面）；
  //   2. 只等「存在」也不够 —— 弹层里没选头游时，搭档那三个选项**也在 hits 里**，只是
  //      onTap=null（禁用）。那样会立刻返回、点在禁用态上，后面全错位（第二版就是这样）。
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

  // 弹层开着就点遮罩关掉（准备下一步之前用）。
  const closeSheetIfOpen = () => { if ((app.sheets || []).length) { tap("scrim"); return waitHit("bar:record", 5000) } return Promise.resolve() };

  // ── 准备：换成本机身份（3 号 阿杰），并把这一局的时间改到 20 分钟后 ──
  // S1 的入口条只在「满座 + 离开始 ≤ 30 分钟」时出现，种子局的时间不满足，所以走查自己造一局。
  const tokens: Record<string, string> = {};
  // 把这几个账号手里的座位全退掉，腾出「同时最多占 5 个位置」的配额。
  const freeSeats = async (token: string) => {
    const list = await req("GET", "/api/games", null, token);
    let left = 0;
    for (const g of list.body?.games || []) {
      if (g.my_role && g.my_role !== "none") { await req("DELETE", `/api/games/${g.short_code}/seat`, null, token); left++ }
    }
    return left;
  };

  step("prepare", async () => {
    tokens.me = await login(OPENID.me, "阿杰"); // 账号已存在时是幂等的；新账号才要带称呼
    tokens.host = await login(OPENID.host, "老王");
    tokens.lily = await login(OPENID.lily, "Lily");
    tokens.zhou = await login(OPENID.zhou, "小周");
    tokens.guest = await login(OPENID.guest, "路人");
    for (const [who, t] of [["我", tokens.me], ["老王", tokens.host], ["Lily", tokens.lily], ["小周", tokens.zhou]] as [string, string][]) {
      const left = await freeSeats(t);
      if (left) send("free", { who, left });
    }
    // 自己是 3 号：换身份要**同时清内存里那份 token**。api.ts 的 token 是模块级变量
    // （import 时从 storage 读一次），只 removeStorageSync 的话 boot() 还会拿旧身份去 getMe()、
    // 直接返回，人根本没换过来 —— 第一版就是这么错的，后面所有热区全 MISS。
    setToken("");
    wx.setStorageSync("gd_dev_openid", OPENID.me);
    let bootErr = null;
    try { await boot() } catch (e: any) { bootErr = e?.message || String(e) }
    // 静默登录失败被 boot() 吞掉了（它设计成不报错），所以这里自己再要一次教训：
    // ⚠️ 这个变量名**不能叫 code** —— 外面那个 `code` 是这一局的 short_code，
    // 第一版把它遮蔽了，于是详情页去开一个叫 "dev:acc-aJie" 的局，整页只有 nav-back。
    let diagCode = "", raw: any = null;
    try { diagCode = await wxCode() } catch (e: any) { diagCode = "wxCode 抛了：" + (e?.message || e) }
    try { raw = await wxLogin(diagCode) } catch (e: any) { raw = { err: e?.message || String(e) } }
    send("who", { openid: wx.getStorageSync("gd_dev_openid"), bootErr, diagCode, raw: JSON.stringify(raw).slice(0, 300) });
    tokens.me = wx.getStorageSync("gd_device_token"); // boot() 之后 token 就落在这儿
    // 造一局：host 开、四个都进来、20 分钟后开始
    const starts = Math.floor(Date.now() / 1000) + 20 * 60;
    const made = await req("POST", "/api/games", { starts_at: starts, area: "SCBD", venue: "Pacific Place · 鼎泰丰包间", kind: "classic" }, tokens.host);
    g.__accept165Code = code || made.body.game.short_code;
    // 报名结果**要报回来**：静默失败过一次（四人局只进来两个 → 详情页没有入口条 →
    // 后面每个热区 MISS），当时没有任何线索，查了好几轮。
    for (const [who, t] of [["我", tokens.me], ["Lily", tokens.lily], ["小周", tokens.zhou]] as [string, string][]) {
      const r = await req("POST", `/api/games/${g.__accept165Code}/seat`, null, t);
      send("join", { who, status: r.status, err: r.body?.error?.code });
    }
    send("game", { code: g.__accept165Code, status: made.status });
  });

  // ── S1：详情页的入口条 ──
  step("s1-entry", () => nav.detail(g.__accept165Code), "score-entry");
  // ── S2：开始记分弹层（两张 chip + 主按钮）──
  step("s2-start-sheet", () => { tap("score-entry") }, "start:0");
  // ── S3：记分页（级牌 + 升级路线）──
  step("s3-score", () => { tap("start:0") }, "bar:record");
  // ── S4：记一局弹层（第二步还没启用）──
  step("s4-record-sheet", () => { tap("bar:record") }, "rs-head:1");
  // ── S5：选了头游 → 第二步启用 ──
  step("s5-head", () => { tap("rs-head:1") }, "rs-place:2");
  // ── S6：选了搭档名次 → 预览 ──
  step("s6-preview", () => { tap("rs-place:2") }, "rs-ok");
  // ── S7：记下第 1 局 → 回到记分页，路线动了 ──
  step("s7-recorded", () => { tap("rs-ok") });

  // 再来两局：红方双上、黑方三游，让级数岔开，本局打的级换边
  step("more-1", () => { tap("bar:record") }, "rs-head:1");
  step("more-1-a", () => { tap("rs-head:3") }, "rs-place:2");
  step("more-1-b", () => { tap("rs-place:2") }, "rs-ok");
  step("more-1-c", () => { tap("rs-ok") });
  step("more-2", () => { tap("bar:record") }, "rs-head:1");
  step("more-2-a", () => { tap("rs-head:2") }, "rs-place:2");
  step("more-2-b", () => { tap("rs-place:4") }, "rs-ok");
  step("more-2-c", () => { tap("rs-ok") });

  // ── S11：别人记了一局 → 顶部深色条（3 秒后自己收起，要马上截）──
  step("s11-strip", async () => {
    const cur = await req("GET", `/api/games/${g.__accept165Code}/score`, null, tokens.me);
    if (!cur.body?.rounds) { send("no-score", { status: cur.status }); return }
    const no = cur.body.rounds.length + 1;
    await req("POST", `/api/games/${g.__accept165Code}/score/rounds`, { expected_round_no: no, head_seat: 4, partner_place: 3 }, tokens.lily);
  });

  // ── S9：每局记录 ──
  step("s9-records", () => { tap("bar:records") }, "round:1");
  // ── S10：改一局（连带变化）──
  step("s10-edit", () => { tap("round:1") }, "rs-head:4");
  step("s10-edit-head", () => { tap("rs-head:4") }, "rs-place:4");
  step("s10-edit-place", () => { tap("rs-place:4") }, "rs-ok");
  step("s10-edited", () => { tap("rs-ok") }, "round:1");

  // ── S12：两人同时记同一局（409）──
  // 先让 Lily 记下第 N 局，再让「我」在界面上记同一局 —— 服务端返回 round_taken，
  // 界面弹冲突框。这是真实路径：不是伪造响应，是真发两个请求。
  step("s12-back-to-score", async () => { await closeSheetIfOpen(); tap("nav-back") }, "bar:record");
  step("s12-open", () => { tap("bar:record") }, "rs-head:1");
  // ⚠️ 两下拆两步：点完第一下时 `app.hits` 还是**上一帧**的，第二下必然点在禁用态上。
  step("s12-prepare", async () => {
    const cur = await req("GET", `/api/games/${g.__accept165Code}/score`, null, tokens.me);
    g.__accept165Round = cur.body.rounds.length + 1;
    await req("POST", `/api/games/${g.__accept165Code}/score/rounds`, { expected_round_no: g.__accept165Round, head_seat: 2, partner_place: 2 }, tokens.lily);
    await req("GET", `/api/games/${g.__accept165Code}/score`, null, tokens.me); // 让前端拿到 Lily 那局
  });
  step("s12-pick-head", () => { tap("rs-head:1") }, "rs-place:3");
  step("s12-pick-place", () => { tap("rs-place:3") }, "rs-ok");
  step("s12-submit", () => { tap("rs-ok") }, "cf-theirs");
  step("s12-use-theirs", () => { tap("cf-theirs") });

  // ── 快进到过 A（本场结束 S8），再导出战报（S15）──
  // 红方一路双上：2→5→8→10→K→A→过 A。直接走 API，模拟的就是四个人的每局事实。
  step("fastforward", async () => {
    for (let i = 0; i < 12; i++) {
      const cur = await req("GET", `/api/games/${g.__accept165Code}/score`, null, tokens.me);
      if (cur.body?.state?.match_over) break; // 字段是 state.match_over（match 是场次号）
      const no = cur.body.rounds.length + 1;
      const r = await req("POST", `/api/games/${g.__accept165Code}/score/rounds`, { expected_round_no: no, head_seat: 1, partner_place: 2 }, tokens.me);
      if (r.status !== 200) { send("ff-stop", { no, status: r.status }); break }
    }
  });
  step("s8-ended", async () => { await closeSheetIfOpen(); nav.detail(g.__accept165Code); nav.score(g.__accept165Code) }, "bar:report");
  step("s15-report", () => { tap("bar:report") });

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
