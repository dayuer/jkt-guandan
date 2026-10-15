// 我的（G1–G5）：称呼、在别的设备上登录（认领码）、我在网页上报过名、WhatsApp、设备、隐私与注销。

import type { App, Scene, Sheet } from "../core/app";
import { C, Ctx, Scroller, fillRR, font, initial, text } from "../core/ui";
import { PAD, avatar, button, copy, disclaimer, emptyState, errorLine, field, list, nav, sheetButtons, sheetHeader, simpleSheet } from "../core/widgets";
import { ApiError, API_BASE, clearWaPhone, deleteMe, getMe, issueClaim, setWaPhone, signOutDevice, signOutOtherDevices, type ClaimTicket, type Device } from "../lib/api";
import { lastSeenLabel, timeHM } from "../lib/fmt";
import { currentUser, signOutHere } from "../lib/session";
import { maskWaPhone, normalizeWaPhone } from "../../../h5/src/wa";
import { TAB_H } from "./home";
import type { Nav } from "./nav";
import { linkSheet, requireIdentity } from "./sheets";

export class Me implements Scene {
  tab = "me";
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

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - TAB_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, bottom = a.H - TAB_H - a.safeBottom, fw = W - PAD * 2;
    const me = currentUser();
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    if (me) {
      fillRR(ctx, PAD, y + 2, fw, 76, 18, C.line);
      fillRR(ctx, PAD, y, fw, 76, 18, C.card);
      avatar(ctx, PAD + 40, y + 38, 24, initial(me.nickname), "me");
      text(ctx, me.nickname, PAD + 76, y + 28, font(18, "900", true), C.ink);
      text(ctx, "这个称呼就是你在牌局里的名字", PAD + 76, y + 52, font(12), C.ink3);
      y += 92;
      y += list(a, ctx, PAD, y, fw, [
        { id: "claim", title: "在别的设备上登录", sub: "换手机、在浏览器里打开网页版时找回自己：给你一串认领码", chevron: true, onTap: () => claimSheet(a) },
        { id: "wa", title: "绑定 WhatsApp", sub: "可选。换了手机也能凭号码找回", value: this.wa ? maskWaPhone(this.wa) : "去绑定", chevron: true, onTap: () => this.waSheet() },
        { id: "link", title: "我在网页上报过名", sub: "用网页版给的认领码，把网页上的身份接到这个微信", chevron: true, onTap: () => void linkSheet(a).then(() => this.refresh()) },
      ]) + 14;
    } else {
      y += emptyState(a, ctx, y, ["还没有身份。", "填个称呼就能报名、开一局。"]);
      button(a, ctx, "fill-nick", PAD, y, fw, 50, "填个称呼", "primary", () => void requireIdentity(a).then(() => this.refresh()));
      y += 64;
    }
    y += list(a, ctx, PAD, y, fw, [
      { id: "devices", title: "登录的设备", sub: "看看哪些设备认得这个身份，不认识的可以退掉", chevron: true, disabled: !me, onTap: () => this.nav.devices() },
      { id: "privacy", title: "隐私与数据", sub: "我们存了什么、存多久、怎么注销", chevron: true, onTap: () => this.nav.privacy() },
    ]) + 6;
    y += disclaimer(a, ctx, y) + 12;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, "我的");
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
  ["注销之后会怎样", "称呼、WhatsApp、设备、微信标识、提醒和订阅消息记录都会被删除。已经结束的局里，你的座位会显示成「已注销牌友」，这样那一局当时有几个人还是记得准的"],
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
