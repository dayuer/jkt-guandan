// 08 · 开一局（一期约局直接开这个表单，不经过蛋蛋）。
// 文字输入走 wx.showKeyboard：点字段 → 底部原生输入条，画布上同步显示。

import type { App, Scene } from "../core/app";
import { C, Ctx, Scroller, circle, fillRR, font, text } from "../core/ui";
import { PAD, button, chip, chipsWrap, disclaimer, errorLine, field, nav } from "../core/widgets";
import { ApiError, request, createGame, inviteError, inviteFriends, inviteSummary, newIdempotencyKey, subscribeRetry, type Friend } from "../lib/api";
import { currentUser } from "../lib/session";
import { wibAt, weekday } from "../lib/fmt";
import { AREAS } from "./home";
import type { Nav } from "./nav";
import { friendPicker, requireIdentity } from "./sheets";

const KINDS = [{ value: "classic", label: "经典" }, { value: "no-level", label: "不升级" }, { value: "teaching", label: "带新手" }];
const BAR_H = 76;

function presets(): { label: string; unix: number }[] {
  return [0, 1, 2].map((d) => {
    const unix = wibAt(d, 19, 30);
    return { label: `${["今晚", "明晚", "后天"][d]} ${weekday(unix)} 19:30`, unix };
  });
}

export class Create implements Scene {
  private times = presets();
  private startsAt = this.times[0].unix;
  private area = AREAS[0];
  private venue = "";
  private kind = "classic";
  private note = "";
  private invitees: Friend[] = [];
  private busy = false;
  private retrying = false;
  private error = "";
  private badField = "";
  private idemKey = newIdempotencyKey();
  private scroll = new Scroller();
  private unretry: (() => void) | null = null;
  private fieldY: Record<string, number> = {};
  // 按住说话（#122 的入口位置）：开发版显示；正式版等服务端 /api/config 的 features.voice 打开
  private voice = __DEV__;
  private rec: { t0: number; cancel: boolean } | null = null;
  private lastRec: { cancel: boolean } | null = null;
  private recorder: any = null;

  constructor(private app: App, private nav: Nav) {}

  onEnter() {
    this.unretry = subscribeRetry((v) => { this.retrying = v; this.app.redraw(); });
    request<{ features?: { voice?: boolean } }>("GET", "/api/config")
      .then((c) => { if (c?.features?.voice) { this.voice = true; this.app.redraw(); } })
      .catch(() => {});
  }
  onLeave() { this.unretry?.(); if (this.app.editing) wx.hideKeyboard({}); }

  /** 内容改了就换一个幂等键：同一份内容重试不会开两局，改过的内容是新的一局（§11.4）。 */
  private changed() { this.idemKey = newIdempotencyKey(); }

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - BAR_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step() || !!this.rec; }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, fw = W - PAD * 2;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    const top = a.contentTop;
    const bottom = a.H - (a.kbHeight ? a.kbHeight : BAR_H + a.safeBottom);
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    const label = (s: string, hint?: string) => {
      text(ctx, s, PAD, y + 20, font(14, "700"), C.ink);
      if (hint) text(ctx, hint, W - PAD, y + 20, font(12), C.ink4, "right");
      y += 36;
    };
    if (this.voice) y = this.drawVoice(ctx, y);
    label("什么时候");
    y = chipsWrap(a, ctx, "time", PAD, y, fw, this.times.map((t) => t.label), (i) => this.times[i].unix === this.startsAt, (i) => { this.startsAt = this.times[i].unix; this.changed(); }) + 14;
    label("哪个片区");
    y = chipsWrap(a, ctx, "area", PAD, y, fw, AREAS, (i) => AREAS[i] === this.area, (i) => { this.area = AREAS[i]; this.changed(); }) + 14;
    label("具体地点");
    this.fieldY.venue = y + this.scroll.pos;
    y += field(a, ctx, PAD, y, fw, {
      key: "venue", value: this.venue, placeholder: "比如：Pacific Place · 鼎泰丰包间", maxLength: 40, confirmType: "next",
      error: this.badField === "venue" ? this.error : undefined,
      onChange: (v) => { this.venue = v; this.clearBad("venue"); this.changed(); },
      onConfirm: () => setTimeout(() => this.focus("note", true), 60),
    }) + 14;
    label("怎么打");
    let x = PAD;
    for (const k of KINDS) x += chip(a, ctx, "kind:" + k.value, x, y, k.label, this.kind === k.value, () => { this.kind = k.value; this.changed(); }) + 8;
    y += 50;
    label("备注", "可不填");
    this.fieldY.note = y + this.scroll.pos;
    y += field(a, ctx, PAD, y, fw, {
      key: "note", value: this.note, placeholder: "比如：新手也欢迎，慢慢打", maxLength: 200, multiple: true,
      error: this.badField === "note" ? this.error : undefined,
      onChange: (v) => { this.note = v; this.clearBad("note"); this.changed(); },
    }) + 14;
    label("邀请谁", "可不填");
    x = PAD;
    for (const f of this.invitees) {
      const w = chip(a, ctx, "inv:" + f.user.id, x, y, f.user.nickname, false, null);
      if (x + w > W - PAD) { x = PAD; y += 44; }
      x += w + 8;
    }
    if (x + 120 > W - PAD) { x = PAD; y += 44; }
    chip(a, ctx, "inv-add", x, y, this.invitees.length ? "改一下" : "＋ 从牌友里选", false, () => this.pickInvitees());
    y += 50;
    y += disclaimer(a, ctx, y);
    if (this.error && !this.badField) y += errorLine(a, ctx, y, this.error);
    y += 12;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);

    nav(a, ctx, "开一局", () => this.nav.back());
    if (this.rec) this.drawRecording(ctx);
    if (!a.kbHeight) {
      const by = a.H - BAR_H - a.safeBottom;
      ctx.fillStyle = C.card; ctx.fillRect(0, by, W, BAR_H + a.safeBottom);
      ctx.fillStyle = C.line; ctx.fillRect(0, by, W, 1);
      button(a, ctx, "submit", PAD, by + 12, fw, 52, this.retrying ? "人有点多，正在重试…" : this.busy ? "正在开…" : "开这一局", "primary",
        () => void this.submit(), { disabled: this.busy || this.retrying || !this.venue.trim(), radius: 26 });
    }
  }

  private focus(key: "venue" | "note", multiple: boolean) {
    const a = this.app;
    a.edit({
      key, value: key === "venue" ? this.venue : this.note, multiple, maxLength: key === "venue" ? 40 : 200,
      onInput: (v) => { if (key === "venue") this.venue = v; else this.note = v; this.clearBad(key); this.changed(); },
    });
    setTimeout(() => { this.scroll.scrollTo(this.fieldY[key] - a.contentTop - 60); a.redraw(); }, 250);
  }

  private clearBad(f: string) {
    if (this.badField !== f) return;
    this.badField = ""; this.error = "";
  }

  private pickInvitees() {
    friendPicker(this.app, {
      selected: this.invitees.map((f) => f.user.id),
      allowNone: true,
      confirmLabel: (n) => (n > 0 ? `就这 ${n} 位` : "不邀请了"),
      onConfirm: (picked, s) => { this.invitees = picked; this.app.closeSheet(s); this.app.redraw(); },
    });
  }

  private async submit() {
    if (this.busy) return;
    if (!currentUser()) {
      const u = await requireIdentity(this.app);
      if (!u) return;
    }
    this.busy = true; this.error = ""; this.badField = ""; this.app.redraw();
    try {
      const d = await createGame({ starts_at: this.startsAt, area: this.area, venue: this.venue.trim(), kind: this.kind, note: this.note.trim() || undefined }, this.idemKey);
      const code = d.game.short_code;
      if (this.invitees.length) {
        try {
          this.app.toast(`开好了，${inviteSummary(await inviteFriends(code, this.invitees.map((f) => f.user.id)))}`, 2600);
        } catch (err) {
          this.app.toast(`局开好了，但${inviteError(err)}；可以在详情页再邀请`, 2600);
        }
      } else this.app.toast("开好了，点「分享」发到群里");
      this.nav.created(code);
    } catch (err) {
      if (err instanceof ApiError && err.code === "sensitive_word") {
        this.badField = err.field; this.error = err.message;
        if (!["venue", "note"].includes(err.field)) this.badField = "";
      } else if (err instanceof ApiError && (err.code === "quota_exceeded" || err.code === "invalid_input" || err.code === "network")) {
        this.error = err.message;
      } else this.error = "没开成，请再试一次";
    } finally {
      this.busy = false; this.app.redraw();
    }
  }

  // ── 按住说话：录音 → 上滑取消 → 松手结束（上传和识别在 #122）──

  private drawVoice(ctx: Ctx, y: number): number {
    const a = this.app, x = PAD, w = a.W - PAD * 2, h = 64, on = !!this.rec;
    fillRR(ctx, x, y, w, h, 18, on ? C.redDeep : C.redTint);
    circle(ctx, x + 34, y + h / 2, 18, on ? C.red : C.card);
    text(ctx, "🎙", x + 34, y + h / 2 + 1, font(17), C.red, "center");
    text(ctx, on ? "松手结束，上滑取消" : "按住说话，蛋蛋帮你填", x + 64, y + 24, font(15, "700"), on ? C.cream : C.red);
    text(ctx, "例：「周四晚上七点半 PIK，还差三个」", x + 64, y + 45, font(12), on ? "rgba(255,247,236,0.8)" : C.ink3);
    a.hit("voice", x, y, w, h, null); // 按住的逻辑在 onTouchStart / onTouchEnd
    return y + h + 12;
  }

  onTouchStart(_x: number, _y: number, hit: { id: string } | null) {
    if (hit && hit.id === "voice") { this.startRec(); return true; }
    return false;
  }
  onTouchMove(_x: number, y: number, t: { y0: number }) {
    if (this.rec) this.rec.cancel = t.y0 - y > 60;
  }
  onTouchEnd() {
    if (this.rec) this.stopRec();
  }

  private startRec() {
    const a = this.app;
    if (!this.recorder) {
      this.recorder = wx.getRecorderManager();
      this.recorder.onStop((res: any) => {
        if (this.lastRec?.cancel) { a.toast("已取消"); return; }
        if ((res.duration || 0) < 600) { a.toast("说得太短了，再按住说一次"); return; }
        // #122：wx.uploadFile → POST /api/voice → 文字交给 /api/agent 出草稿。服务端还没有，先提示手动填
        a.toast(`录到 ${((res.duration || 0) / 1000).toFixed(1)} 秒。语音开局还没上线，先手动填表`, 2600);
      });
      this.recorder.onError((e: any) => { this.rec = null; a.toast("录音没成功：" + (e?.errMsg || "")); });
    }
    this.rec = { t0: Date.now(), cancel: false };
    this.recorder.start({ duration: 15000, sampleRate: 16000, numberOfChannels: 1, encodeBitRate: 48000, format: "mp3" });
    wx.vibrateShort?.({ type: "medium" });
  }

  private stopRec() {
    this.lastRec = this.rec;
    this.rec = null;
    this.recorder?.stop();
    this.app.redraw();
  }

  private drawRecording(ctx: Ctx) {
    const a = this.app, W = a.W, cy = a.H * 0.45, r = this.rec!;
    ctx.fillStyle = "rgba(28,20,17,0.45)";
    ctx.fillRect(0, 0, W, a.H);
    const t = (Date.now() - r.t0) / 1000;
    fillRR(ctx, W / 2 - 110, cy - 90, 220, 180, 22, r.cancel ? C.ink2 : C.card);
    for (let i = 0; i < 9; i++) {
      const hg = 10 + 26 * Math.abs(Math.sin(t * 6 + i * 0.9));
      fillRR(ctx, W / 2 - 64 + i * 15, cy - 30 - hg / 2, 8, hg, 4, r.cancel ? C.khaki : C.red);
    }
    text(ctx, `${Math.max(0, 15 - t).toFixed(0)} 秒`, W / 2, cy + 22, font(22, "900", true), r.cancel ? C.cream : C.ink, "center");
    text(ctx, r.cancel ? "松手取消" : "上滑取消", W / 2, cy + 56, font(13), r.cancel ? C.cream : C.ink3, "center");
  }
}
