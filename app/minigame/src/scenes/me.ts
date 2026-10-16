// 我的（G1–G5）：称呼、我的牌局（#211）、隐私与注销；不常用的（认领码、网页版身份、WhatsApp、设备）收进「设置」。

import type { App, Scene, Sheet } from "../core/app";
import { C, Ctx, Scroller, card, circle, ellipsize, fillRR, font, initial, measure, text } from "../core/ui";
import { PAD, avatar, button, copy, disclaimer, emptyState, errorLine, field, list, nav, sheetButtons, sheetHeader, simpleSheet, textLink } from "../core/widgets";
import { ApiError, API_BASE, clearWaPhone, deleteMe, getMe, hideFromHistory, issueClaim, listMyGames, listMyHistory, setNickname, setWaPhone, signOutDevice, signOutOtherDevices, type ClaimTicket, type Device, type GameSummary, type HistoryGame } from "../lib/api";
import { cardDay, dayLabel, kindLabel, lastSeenLabel, timeHM, weekday } from "../lib/fmt";
import { afterHide, historyStatus, roleBadge, scoreLine, upcomingStatus, type Badge, type Tone } from "../lib/mygames";
import { currentUser, signOutHere, updateNickname } from "../lib/session";
import { maskWaPhone, normalizeWaPhone } from "../../../h5/src/wa";
import { TAB_H } from "./home";
import type { Nav } from "./nav";
import { linkSheet, requireIdentity } from "./sheets";

export class Me implements Scene {
  tab = "me";
  private scroll = new Scroller();
  /** 要打的（#211）。null 是还没读到。 */
  private upcoming: GameSummary[] | null = null;
  private history: HistoryGame[] | null = null;
  private hasMore = false;
  /** 已经读到的最后一条，翻下一页的游标。单独存：列表里的都删光了也还能接着往下翻。 */
  private cursor: HistoryGame | undefined;
  private loadingMore = false;
  private error = "";
  private top = 0;
  private bottom = 0;
  constructor(private app: App, private nav: Nav) {}

  onEnter() { this.refresh(); }
  onResume() { this.refresh(); }
  /**
   * 每次 refresh() 加一。请求回来时代数变了就丢掉结果（auditor-230）：
   * 不然「看更早的」读到一半页面刷新了，旧的下一页会拼到新列表后面。
   */
  private gen = 0;
  /** 没有身份 / 凭证失效：要打的、历史、翻页状态全清掉，不留「历史」标题和「看更早的」。 */
  private clear() {
    this.upcoming = []; this.history = []; this.hasMore = false; this.cursor = undefined;
  }
  private refresh() {
    const gen = ++this.gen;
    this.loadingMore = false;
    if (!currentUser()) { this.clear(); return; }
    Promise.all([listMyGames(), listMyHistory()]).then(([up, h]) => {
      if (gen !== this.gen) return;
      this.upcoming = up; this.history = h.games; this.hasMore = h.has_more; this.error = "";
      this.cursor = h.games[h.games.length - 1];
    }).catch((err) => {
      if (gen !== this.gen) return;
      if (err instanceof ApiError && err.status === 401) { this.clear(); return; }
      this.error = "读不到我的牌局，稍后再试";
      this.upcoming ??= []; this.history ??= [];
    }).finally(() => this.app.redraw());
  }

  private async more() {
    const last = this.cursor, gen = this.gen;
    if (!last || this.loadingMore) return;
    this.loadingMore = true; this.app.redraw();
    try {
      const page = await listMyHistory(last);
      if (gen !== this.gen) return;
      const seen = new Set((this.history ?? []).map((g) => g.short_code));
      this.history = [...(this.history ?? []), ...page.games.filter((g) => !seen.has(g.short_code))];
      this.hasMore = page.has_more;
      if (page.games.length) this.cursor = page.games[page.games.length - 1];
    } catch { if (gen === this.gen) this.app.toast("没读到更早的，稍后再试"); }
    finally { if (gen === this.gen) this.loadingMore = false; this.app.redraw(); }
  }

  /** 从我自己的历史里删掉：先确认，删成功了再从列表里拿掉。 */
  private confirmHide(g: HistoryGame) {
    const a = this.app;
    simpleSheet(a, {
      title: "从我的历史里删掉这一局？",
      sub: `${dayLabel(g.starts_at)} ${timeHM(g.starts_at)} · ${g.venue}。只是你自己看不到了，牌局本身、同局牌友的记录和比分都不受影响。删了不能恢复。`,
      buttons: (s) => [
        { label: "删掉", style: "danger", onTap: () => { a.closeSheet(s); void this.hide(g); } },
        { label: "算了", style: "soft", onTap: () => a.closeSheet(s) },
      ],
    });
  }
  private async hide(g: HistoryGame) {
    try {
      await hideFromHistory(g.short_code);
      const next = afterHide(this.history ?? [], g.short_code, this.hasMore);
      this.history = next.list;
      this.app.toast("删掉了");
      if (next.loadMore) void this.more();
    } catch (err) {
      this.app.toast(err instanceof ApiError && err.code === "not_history" ? "这一局还没开始，要退出请去详情页" : "没删掉，稍后再试");
    } finally { this.app.redraw(); }
  }

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - TAB_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, bottom = a.H - TAB_H - a.safeBottom, fw = W - PAD * 2;
    this.top = top; this.bottom = bottom;
    const me = currentUser();
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    if (me) {
      const nickId = "edit-nick";
      fillRR(ctx, PAD, y + 2, fw, 76, 18, C.line);
      fillRR(ctx, PAD, y, fw, 76, 18, a.isPressed(nickId) ? C.paper : C.card);
      avatar(ctx, PAD + 40, y + 38, 24, initial(me.nickname), "me");
      // 「改」比箭头直白：这里点进去就是改称呼（#210）
      text(ctx, me.nickname, PAD + 76, y + 28, font(18, "900", true), C.ink);
      text(ctx, "这个称呼就是你在牌局里的名字", PAD + 76, y + 52, font(12), C.ink3);
      fillRR(ctx, PAD + fw - 62, y + 26, 46, 24, 12, C.paper);
      text(ctx, "改", PAD + fw - 39, y + 38, font(13, "700"), C.red, "center");
      a.hit(nickId, PAD, y, fw, 76, () => nickSheet(a));
      y += 92;
      y = this.drawMyGames(ctx, y, fw, me.id);
    } else {
      y += emptyState(a, ctx, y, ["还没有身份。", "填个称呼就能报名、开一局。"]);
      button(a, ctx, "fill-nick", PAD, y, fw, 50, "填个称呼", "primary", () => void requireIdentity(a).then(() => this.refresh()));
      y += 64;
    }
    // 不常用的都收进「设置」（#211）；「隐私与数据」留在这里，注销也从这里进。
    y += list(a, ctx, PAD, y, fw, [
      { id: "settings", title: "设置", sub: "在别的设备上登录、绑定 WhatsApp、网页版身份、登录的设备", chevron: true, onTap: () => this.nav.settings() },
      { id: "privacy", title: "隐私与数据", sub: "我们存了什么、存多久、怎么注销", chevron: true, onTap: () => this.nav.privacy() },
    ]) + 6;
    y += disclaimer(a, ctx, y) + 12;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, "我的");
  }

  /** 我的牌局（#211）：要打的在上，历史在下。返回画完之后的 y。 */
  private drawMyGames(ctx: Ctx, y: number, fw: number, meID: string): number {
    const a = this.app, W = a.W;
    text(ctx, "我的牌局", PAD, y + 16, font(20, "900", true), C.ink);
    y += 36;
    if (this.error) y += errorLine(a, ctx, y, this.error);

    const up = this.upcoming;
    text(ctx, up && up.length ? `要打的 · ${up.length} 局` : "要打的", PAD, y + 10, font(13, "700"), C.ink3);
    y += 26;
    if (up === null) y += emptyState(a, ctx, y, ["正在看…"]);
    else if (up.length === 0) {
      y += emptyState(a, ctx, y, ["还没有要打的局。"]);
      textLink(a, ctx, "mine-find", W / 2 - 56, y + 4, "去找局 ›", () => this.nav.tab("games"), C.red);
      textLink(a, ctx, "mine-create", W / 2 + 56, y + 4, "开一局 ›", () => this.nav.create(), C.red);
      y += 40;
    }
    for (const g of up ?? []) {
      this.drawGame(ctx, g, PAD, y, fw, meID, false);
      y += ROW_H + ROW_GAP;
    }

    const hist = this.history;
    // 删光了但还有更早的：标题和「看更早的」留着（more() 正在接着读下一页）
    if (hist && (hist.length || this.hasMore)) {
      y += 8;
      text(ctx, "历史", PAD, y + 10, font(13, "700"), C.ink3);
      text(ctx, "删除只是你自己看不到", W - PAD, y + 10, font(11), C.ink4, "right");
      y += 26;
      for (const g of hist) {
        this.drawGame(ctx, g, PAD, y, fw, meID, true);
        y += ROW_H + ROW_GAP;
      }
      if (this.hasMore) {
        textLink(a, ctx, "mine-more", W / 2, y + 12, this.loadingMore ? "正在读…" : "看更早的 ›", () => void this.more());
        y += 36;
      }
    }
    return y + 14;
  }

  private drawGame(ctx: Ctx, g: GameSummary | HistoryGame, x: number, y: number, w: number, meID: string, past: boolean) {
    const a = this.app, id = "mine:" + g.short_code, h = ROW_H;
    if (y + h < this.top - 10 || y > this.bottom + 10) return;
    ctx.save();
    if (a.isPressed(id)) { ctx.translate(x + w / 2, y + h / 2); ctx.scale(0.98, 0.98); ctx.translate(-x - w / 2, -y - h / 2); }
    card(ctx, x, y, w, h, 16);
    // 左边：哪天、几点
    fillRR(ctx, x + 12, y + 12, 72, h - 24, 10, C.paper);
    const dim = past ? C.ink3 : C.ink;
    text(ctx, cardDay(g.starts_at), x + 48, y + 30, font(12, "700"), past ? C.ink3 : C.red, "center");
    text(ctx, timeHM(g.starts_at), x + 48, y + 53, font(22, "900", true), dim, "center");
    text(ctx, weekday(g.starts_at), x + 48, y + 75, font(11), C.ink3, "center");

    // 右边：片区、玩法、地点；我的身份；凑局状态 / 比分
    const rx = x + 96, rw = w - 96 - 12;
    const role = roleBadge(g, meID);
    let roleW = 0;
    if (!past && role) roleW = drawBadge(ctx, role, x + w - 12, y + 14, "right") + 8;
    const sub = [g.area, kindLabel(g.kind), past && role?.label === "我开的" ? "我开的" : ""].filter(Boolean).join(" · ");
    text(ctx, ellipsize(ctx, sub, rw - roleW - (past ? 40 : 0), font(12)), rx, y + 25, font(12), C.ink3);
    text(ctx, ellipsize(ctx, g.venue, rw, font(16, "700")), rx, y + 50, font(16, "700"), dim);

    const status = past ? historyStatus(g) : upcomingStatus(g);
    const sw = drawBadge(ctx, status, rx, y + 64, "left");
    if (past) {
      const line = scoreLine(g as HistoryGame);
      if (line) text(ctx, ellipsize(ctx, line, rw - sw - 10, font(12, "600")), rx + sw + 10, y + 77, font(12, "600"), C.ink2);
    } else {
      // 座位圆点：谁已经坐下了，一眼看出还差几个
      for (let no = 1; no <= 4; no++) {
        const cx = rx + sw + 22 + (no - 1) * 26, cy = y + 77;
        if (cx + 11 > x + w - 8) break;
        const seat = g.seat_list?.find((s) => s.seat_no === no);
        if (seat) {
          const host = seat.user.id === g.host.id, isMe = seat.user.id === meID;
          circle(ctx, cx, cy, 11, isMe && !host ? C.gold : host ? C.redTint : C.beige);
          text(ctx, initial(seat.user.nickname), cx, cy + 1, font(11, "900", true), host ? C.red : C.ink, "center");
        } else circle(ctx, cx, cy, 10.5, null, C.khaki, 1.2, [3, 3]);
      }
    }
    ctx.restore();

    const vy = Math.max(y, this.top), vh = Math.min(y + h, this.bottom) - vy;
    if (vh > 0) a.hit(id, x, vy, w, vh, () => this.nav.detail(g.short_code));
    if (past) {
      // 删除放在右上角，单独一块热区（后登记的先命中，盖在卡片热区上面）
      const did = "hide:" + g.short_code;
      text(ctx, "删除", x + w - 14, y + 25, font(12, "600"), a.isPressed(did) ? C.red : C.ink4, "right");
      const hy = Math.max(y, this.top), hh = Math.min(y + 44, this.bottom) - hy;
      if (hh > 0) a.hit(did, x + w - 64, hy, 64, hh, () => this.confirmHide(g as HistoryGame));
    }
  }
}

const ROW_H = 104, ROW_GAP = 10;

const TONE: Record<Tone, [string, string]> = {
  red: [C.redTint, C.red], green: [C.greenTint, C.green], sand: [C.sand, C.ink2], grey: [C.beige, C.ink3],
};
/** 画一个小标签，返回宽度。align 是 x 对齐的那一边。 */
function drawBadge(ctx: Ctx, b: Badge, x: number, y: number, align: "left" | "right"): number {
  const f = font(12, "700"), bw = measure(ctx, b.label, f) + 16, bx = align === "left" ? x : x - bw;
  const [bg, fg] = TONE[b.tone];
  fillRR(ctx, bx, y, bw, 24, 7, bg);
  text(ctx, b.label, bx + bw / 2, y + 12, f, fg, "center");
  return bw;
}

// ── 设置（#211）：从「我的」页挪过来的不常用条目，行为不变 ──

export class Settings implements Scene {
  private wa: string | undefined;
  private scroll = new Scroller();
  constructor(private app: App, private nav: Nav) {}

  onEnter() { this.refresh(); }
  onResume() { this.refresh(); }
  private refresh() {
    this.wa = undefined;
    if (!currentUser()) return;
    getMe().then((b) => { this.wa = b.wa_phone || undefined; this.app.redraw(); }).catch(() => {});
  }

  scrollerAt(_x: number, y: number) { return y > this.app.contentTop ? { s: this.scroll, axis: "y" as const } : null; }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, fw = W - PAD * 2;
    const me = currentUser();
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, a.H - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;
    if (me) {
      text(ctx, "换设备、找回身份", PAD + 4, y + 12, font(13, "700"), C.ink3);
      y += 28;
      y += list(a, ctx, PAD, y, fw, [
        { id: "claim", title: "在别的设备上登录", sub: "换手机、在浏览器里打开网页版时找回自己：给你一串认领码", chevron: true, onTap: () => claimSheet(a) },
        { id: "wa", title: "绑定 WhatsApp", sub: "可选。换了手机也能凭号码找回", value: this.wa ? maskWaPhone(this.wa) : "去绑定", chevron: true, onTap: () => this.waSheet() },
        { id: "link", title: "我在网页上报过名", sub: "用网页版给的认领码，把网页上的身份接到这个微信", chevron: true, onTap: () => void linkSheet(a).then(() => this.refresh()) },
      ]) + 14;
    }
    y += list(a, ctx, PAD, y, fw, [
      { id: "devices", title: "登录的设备", sub: "看看哪些设备认得这个身份，不认识的可以退掉", chevron: true, disabled: !me, onTap: () => this.nav.devices() },
    ]) + 14;
    if (!me) { text(ctx, "还没有身份，填个称呼之后这里才有东西。", W / 2, y + 12, font(12), C.ink4, "center"); y += 32; }
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - a.H);
    nav(a, ctx, "设置", () => this.nav.back());
  }

  /** 绑定 WhatsApp（#116） */
  private waSheet() {
    const app = this.app;
    let editing = !this.wa, value = "", busy = false, error = "";
    const save = async () => {
      if (busy) return;
      const phone = normalizeWaPhone(value);
      if (!phone) { error = "认不出这个号码。请带上国家码，比如 +62 812 3456 7890 或 +86 138 1234 5678"; app.redraw(); return; }
      busy = true; error = ""; app.redraw();
      try {
        this.wa = (await setWaPhone(phone)).wa_phone;
        app.closeSheet(s); app.toast("绑好了");
      } catch (err) {
        error = err instanceof ApiError && err.code === "invalid_input" ? err.message : "没绑上，稍后再试";
      } finally { busy = false; app.redraw(); }
    };
    const unbind = async () => {
      busy = true; app.redraw();
      try { await clearWaPhone(); this.wa = undefined; app.closeSheet(s); app.toast("已解绑"); }
      catch { error = "没解开，稍后再试"; }
      finally { busy = false; app.redraw(); }
    };
    const s: Sheet = {
      draw: (ctx, y, w) => {
        let cy = sheetHeader(ctx, y, w, "绑定 WhatsApp", "换手机或清了缓存，凭这个号码找回。只有你自己看得到，不会出现在牌友列表里。");
        if (editing) {
          cy += field(app, ctx, 20, cy, w - 40, { key: "wa", value, placeholder: "0812 3456 7890", maxLength: 24, error, onChange: (v) => { value = v; error = ""; }, onConfirm: () => void save() }) + 14;
          cy = sheetButtons(app, ctx, cy, w, [
            { label: busy ? "正在保存…" : this.wa ? "换成这个号码" : "绑定", style: "primary", disabled: busy || !value.trim(), onTap: () => void save() },
            { label: this.wa ? "算了" : "先不用", style: "link", onTap: () => { if (this.wa) editing = false; else app.closeSheet(s); } },
          ]);
        } else {
          text(ctx, `已绑定 ${maskWaPhone(this.wa!)}`, w / 2, cy + 14, font(16, "700"), C.ink, "center");
          cy += 40;
          if (error) { text(ctx, error, w / 2, cy, font(13), C.red, "center"); cy += 24; }
          cy = sheetButtons(app, ctx, cy, w, [
            { label: "换个号码", style: "ghost", disabled: busy, onTap: () => { editing = true; } },
            { label: busy ? "正在解绑…" : "解绑", style: "danger", disabled: busy, onTap: () => void unbind() },
            { label: "关闭", style: "link", onTap: () => app.closeSheet(s) },
          ]);
        }
        return cy - y + 8;
      },
    };
    app.openSheet(s);
  }
}

function nickSheet(app: App) {
  const me = currentUser();
  if (!me) return;
  let value = me.nickname, busy = false, error = "";
  const save = async () => {
    const name = value.trim();
    if (busy || !name) return;
    if (name === me.nickname) { app.closeSheet(s); return; } // 没改，别白跑一次（也算一次限频）
    busy = true; error = ""; app.redraw();
    try {
      const r = await setNickname(name);
      updateNickname(r.user.nickname); // 「我的」页、牌局里都是读这份缓存
      app.closeSheet(s);
      app.toast("改好了");
    } catch (err) {
      // 服务端的说明已经够用户看懂（「称呼需要 1–12 个字符」「这个称呼不能用」）；
      // 内容安全那条回的是 422，把原文写成自己的话。
      error = err instanceof ApiError
        ? (err.code === "sensitive_word" ? "这个名字过不了微信的内容检测，换一个吧"
          : err.code === "rate_limited" ? "这一小时改得太多次了，过一会儿再来"
          : err.message)
        : "没改成，稍后再试";
    } finally {
      busy = false; app.redraw();
    }
  };
  const s: Sheet = {
    draw: (ctx, y, w) => {
      let cy = sheetHeader(ctx, y, w, "改称呼", "牌局里的座位、牌友列表都会跟着变。改动会发给微信做内容检测。");
      cy += field(app, ctx, 20, cy, w - 40, {
        key: "nick", value, placeholder: "比如：老王", maxLength: 12, error,
        onChange: (v) => { value = v; error = ""; }, onConfirm: () => void save(),
      }) + 14;
      cy = sheetButtons(app, ctx, cy, w, [
        { label: busy ? "正在保存…" : "保存", style: "primary", disabled: busy || !value.trim(), onTap: () => void save() },
        { label: "算了", style: "link", onTap: () => app.closeSheet(s) },
      ]);
      return cy - y + 8;
    },
  };
  app.openSheet(s);
}

/** 在别的设备上登录：认领码（主路径）和登录链接（次要，明说别外发）。 */
function claimSheet(app: App) {
  let ticket: ClaimTicket | null = null, error = "";
  issueClaim().then((t) => { ticket = t; app.redraw(); }).catch((err) => {
    error = err instanceof ApiError && err.status === 429 ? "这一小时里拿得太多次了，过一会儿再来" : "没拿到认领码，稍后再试";
    app.redraw();
  });
  const s: Sheet = {
    draw: (ctx, y, w) => {
      let cy = sheetHeader(ctx, y, w, "在别的设备上登录", "在网页版或新手机上输入这串数字，就是你本人");
      if (ticket) {
        fillRR(ctx, 20, cy, w - 40, 128, 16, C.paper);
        text(ctx, "你的认领码", w / 2, cy + 22, font(13), C.ink3, "center");
        const digits = ticket.code.split(""), dw = 38, gap = 8, total = digits.length * dw + (digits.length - 1) * gap;
        digits.forEach((d, i) => {
          const dx = (w - total) / 2 + i * (dw + gap);
          fillRR(ctx, dx, cy + 40, dw, 48, 10, C.card);
          text(ctx, d, dx + dw / 2, cy + 64, font(26, "900", true), C.ink, "center");
        });
        text(ctx, `${timeHM(ticket.expires_at)} 前有效 · 只能用一次 · 要在同一个网络里输入`, w / 2, cy + 108, font(11), C.ink3, "center");
        cy += 142;
        const t = ticket;
        cy = sheetButtons(app, ctx, cy, w, [
          { label: "复制登录链接", style: "ghost", onTap: () => copy(app, `${API_BASE}/?claim=${encodeURIComponent(t.claim_token)}`, "链接已复制，只发给你自己") },
        ]);
        text(ctx, "⚠️ 只发给自己，别发群里：拿到链接的人能直接登录成你", w / 2, cy, font(12), C.red, "center");
        cy += 22;
      } else if (error) { text(ctx, error, w / 2, cy + 16, font(14), C.red, "center"); cy += 40; }
      else { text(ctx, "正在生成…", w / 2, cy + 16, font(14), C.ink3, "center"); cy += 40; }
      cy = sheetButtons(app, ctx, cy + 4, w, [{ label: "关闭", style: "link", onTap: () => app.closeSheet(s) }]);
      return cy - y + 8;
    },
  };
  app.openSheet(s);
}

// ── 登录的设备（G4）──

function deviceName(d: Device): string {
  const ua = d.user_agent ?? "";
  if (ua === "wx-minigame") return "微信小游戏";
  if (/micromessenger/i.test(ua)) return "微信内置浏览器";
  if (/iphone/i.test(ua)) return "iPhone · Safari";
  if (/ipad/i.test(ua)) return "iPad · Safari";
  if (/android/i.test(ua)) return /wv\)/i.test(ua) ? "微信内置浏览器（安卓）" : "Android · 浏览器";
  if (/macintosh/i.test(ua)) return "Mac · Safari";
  return "浏览器";
}

export class Devices implements Scene {
  private devices: Device[] | null = null;
  private current = "";
  private error = "";
  private busy = false;
  private scroll = new Scroller();
  constructor(private app: App, private nav: Nav) {}

  onEnter() { void this.load(); }
  private async load() {
    try {
      const b = await getMe();
      this.devices = b.devices; this.current = b.device_id; this.error = "";
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) { this.nav.back(); return; }
      this.error = "读不到设备列表，稍后再试"; this.devices = [];
    } finally { this.app.redraw(); }
  }

  private async signOut(d: Device) {
    if (this.busy) return;
    this.busy = true;
    try {
      await signOutDevice(d.id);
      if (d.id === this.current) { signOutHere(); this.app.toast("这台设备已经退出"); this.nav.back(); return; }
      this.app.toast("那台设备已经退出了");
    } catch (err) {
      this.error = err instanceof ApiError && err.status === 404 ? "那台设备已经不在了" : "没退成功，请再试一次";
    } finally { this.busy = false; await this.load(); }
  }

  private async signOutOthers() {
    if (this.busy) return;
    this.busy = true;
    try {
      const n = await signOutOtherDevices();
      this.app.toast(n > 0 ? `退掉了 ${n} 台设备` : "只有这一台，没有别的设备");
    } catch { this.error = "没退成功，请再试一次"; }
    finally { this.busy = false; await this.load(); }
  }

  scrollerAt(_x: number, y: number) { return y > this.app.contentTop ? { s: this.scroll, axis: "y" as const } : null; }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, fw = W - PAD * 2, me = currentUser();
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, a.H - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;
    if (me) { text(ctx, `这些设备都是「${me.nickname}」`, PAD, y + 12, font(13), C.ink3); y += 32; }
    if (this.devices === null) y += emptyState(a, ctx, y, ["正在看…"]);
    if (this.error) y += errorLine(a, ctx, y, this.error);
    const ds = this.devices ?? [];
    if (ds.length) {
      y += list(a, ctx, PAD, y, fw, ds.map((d) => ({
        id: "dev:" + d.id,
        icon: d.user_agent === "wx-minigame" ? "🀄" : /micromessenger/i.test(d.user_agent ?? "") ? "💬" : "🧭",
        title: deviceName(d) + (d.id === this.current ? "（本机）" : ""),
        sub: lastSeenLabel(d.last_seen),
        value: d.id === this.current ? "退出本机" : "退出",
        valueColor: C.red,
        onTap: () => simpleSheet(a, {
          title: d.id === this.current ? "退出这台设备？" : `退出「${deviceName(d)}」？`,
          sub: d.id === this.current ? "退出后小游戏会忘掉你，下次要重新填称呼或用认领码认回" : "退掉之后它需要重新填称呼，看不到你的局",
          buttons: (s) => [
            { label: "退出", style: "danger", onTap: () => { a.closeSheet(s); void this.signOut(d); } },
            { label: "算了", style: "soft", onTap: () => a.closeSheet(s) },
          ],
        }),
      }))) + 14;
    }
    if (ds.filter((d) => d.id !== this.current).length > 0) {
      button(a, ctx, "out-others", PAD, y, fw, 48, "退出其他设备", "danger", () => void this.signOutOthers(), { disabled: this.busy });
      y += 60;
    }
    text(ctx, "不认识的设备？退掉之后它需要重新填称呼，看不到你的局。", W / 2, y + 12, font(12), C.ink4, "center");
    y += 40;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - a.H);
    nav(a, ctx, "登录的设备", () => this.nav.back());
  }
}

// ── 隐私与数据（G5，§14；小游戏版加上微信标识，#84）──

const PRIVACY: [string, string][] = [
  ["我们存了什么", "称呼、你绑定的 WhatsApp（可选）、登录的设备、你报过的局；在小游戏里还有微信给的用户标识（openid / unionid），只用来认出同一个人"],
  ["存多久", "设备 180 天没用自动删除；使用统计 1 年后删除；备份 30 天后删除"],
  ["拿来做什么", "只用来让你和牌友互相认出来、把局的提醒发给你。报名时你同意了的话，会用微信订阅消息提醒你成局、有人退出、开局前 1 小时、改时间；每同意一次最多发一条。你填的称呼、地点、备注会发给微信做内容安全检测（微信的要求）。不卖给谁，也没有广告"],
  ["注销之后会怎样", "称呼、WhatsApp、设备、微信标识、提醒和订阅消息记录都会被删除。「我的牌局」历史里你删掉过哪些局的记录也会清掉。已经结束的局里，你的座位会显示成「已注销牌友」，这样那一局当时有几个人还是记得准的"],
];

export class Privacy implements Scene {
  private scroll = new Scroller();
  constructor(private app: App, private nav: Nav) {}
  scrollerAt(_x: number, y: number) { return y > this.app.contentTop ? { s: this.scroll, axis: "y" as const } : null; }
  step() { return this.scroll.step(); }

  private confirmDelete() {
    const a = this.app;
    let busy = false, error = "";
    const s: Sheet = {
      dialog: true,
      modal: true,
      draw: (ctx, y, w) => {
        let cy = sheetHeader(ctx, y, w, "确定注销？", "你的称呼、WhatsApp、设备和提醒会被删除，无法恢复。你报过的局里，你的座位会显示为「已注销牌友」。", true);
        if (error) { text(ctx, error, w / 2, cy + 4, font(13), C.red, "center"); cy += 24; }
        const bw = (w - 52) / 2;
        button(a, ctx, "del-no", 20, cy + 4, bw, 48, "不注销", "soft", () => { if (!busy) a.closeSheet(s); }, { disabled: busy });
        button(a, ctx, "del-yes", 32 + bw, cy + 4, bw, 48, busy ? "正在删除…" : "注销", "primary", async () => {
          busy = true; a.redraw();
          try {
            await deleteMe();
            signOutHere();
            a.closeSheet(s);
            a.toast("已经注销，数据都删掉了");
            this.nav.tab("games");
          } catch (err) {
            error = err instanceof ApiError && err.status === 401 ? "现在这台设备没有身份" : "没删掉，请稍后再试";
          } finally { busy = false; a.redraw(); }
        }, { disabled: busy });
        return cy + 64 - y;
      },
    };
    a.openSheet(s);
  }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, fw = W - PAD * 2;
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, a.H - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;
    y += list(a, ctx, PAD, y, fw, PRIVACY.map(([t, s]) => ({ title: t, sub: s }))) + 16;
    if (currentUser()) {
      button(a, ctx, "delete", PAD, y, fw, 48, "注销并删除我的数据", "danger", () => this.confirmDelete());
      y += 60;
    } else y += emptyState(a, ctx, y, ["现在这台设备没有身份，没有可以注销的数据。"]);
    y += disclaimer(a, ctx, y) + 12;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - a.H);
    nav(a, ctx, "隐私与数据", () => this.nav.back());
  }
}
