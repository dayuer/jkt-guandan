// 08 · 开一局（一期约局直接开这个表单，不经过蛋蛋）。
// 文字输入走 wx.showKeyboard：点字段 → 底部原生输入条，画布上同步显示。

import type { App, Scene, Sheet } from "../core/app";
import { C, Ctx, Scroller, ease, font, text } from "../core/ui";
import { MIN_LEAD, PAD, button, chip, chipsWrap, disclaimer, errorLine, field, nav, nextSlot, sheetButtons, sheetHeader, timePicker } from "../core/widgets";
import { ApiError, createGame, inviteError, inviteFriends, inviteSummary, newIdempotencyKey, subscribeRetry, type Friend } from "../lib/api";
import { currentUser } from "../lib/session";
import { slashDate, timeHM, wibAt, weekday } from "../lib/fmt";
import { AREAS } from "./home";
import type { Nav } from "./nav";
import { friendPicker, requireIdentity } from "./sheets";

const KINDS = [{ value: "classic", label: "经典" }, { value: "no-level", label: "不升级" }, { value: "teaching", label: "带新手" }];
const BAR_H = 76;
/** 自定义时间能选今天起几天：服务端上限 30 天（validate.go MaxLeadTime），第 29 天 23:45 也在里面 */
const HORIZON_DAYS = 30;

function presets(): { label: string; unix: number }[] {
  return [0, 1, 2].map((d) => {
    const unix = wibAt(d, 19, 30);
    return { label: `${["今晚", "明晚", "后天"][d]} ${weekday(unix)} 19:30`, unix };
  });
}

export class Create implements Scene {
  private times = presets();
  private startsAt = this.times[0].unix;
  /** 「自定义」选过的时间；和某个预设一样时不记，让预设亮着 */
  private custom: number | null = null;
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
  /** 上一次为哪个字段、多高的键盘滚过：同一个组合只滚一次，之后不和手指抢 */
  private scrolledFor = "";

  constructor(private app: App, private nav: Nav) {}

  onEnter() { this.unretry = subscribeRetry((v) => { this.retrying = v; this.app.redraw(); }); }
  onLeave() { this.unretry?.(); this.app.blurKeyboard(); }

  /** 内容改了就换一个幂等键：同一份内容重试不会开两局，改过的内容是新的一局（§11.4）。 */
  private changed() { this.idemKey = newIdempotencyKey(); }

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - BAR_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, fw = W - PAD * 2;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    const top = a.contentTop;
    // 键盘弹起时操作栏收起，内容区延伸到可视底边（屏高 − 键盘 − 输入条）
    const bottom = a.kbHeight ? a.visibleBottom : a.H - BAR_H - a.safeBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    const label = (s: string, hint?: string) => {
      text(ctx, s, PAD, y + 20, font(14, "700"), C.ink);
      if (hint) text(ctx, hint, W - PAD, y + 20, font(12), C.ink4, "right");
      y += 36;
    };
    label("什么时候");
    const c = this.custom, n = this.times.length;
    const timeLabels = [...this.times.map((t) => t.label), c === null ? "自定义" : `${weekday(c)} ${slashDate(c)} ${timeHM(c)}`];
    y = chipsWrap(a, ctx, "time", PAD, y, fw, timeLabels, (i) => (i < n ? this.times[i].unix : c) === this.startsAt,
      (i) => { if (i === n) this.pickTime(); else this.setTime(this.times[i].unix); }) + 14;
    label("哪个片区");
    y = chipsWrap(a, ctx, "area", PAD, y, fw, AREAS, (i) => AREAS[i] === this.area, (i) => { this.area = AREAS[i]; this.changed(); }) + 14;
    label("具体地点");
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
    this.revealFocus(top, bottom);

    // 新建流程：左上用文字「取消」，不用返回箭头（适配稿 1.3、M3）
    nav(a, ctx, "开一局", undefined, { label: "取消", onTap: () => this.cancel() });
    if (!a.kbHeight) {
      const by = a.H - BAR_H - a.safeBottom;
      ctx.fillStyle = C.card; ctx.fillRect(0, by, W, BAR_H + a.safeBottom);
      ctx.fillStyle = C.line; ctx.fillRect(0, by, W, 1);
      button(a, ctx, "submit", PAD, by + 12, fw, 52, this.retrying ? "人有点多，正在重试…" : this.busy ? "正在开…" : "开这一局", "primary",
        () => void this.submit(), { disabled: this.busy || this.retrying || !this.venue.trim(), radius: 26 });
    }
  }

  private focus(key: "venue" | "note", multiple: boolean) {
    this.app.edit({
      key, value: key === "venue" ? this.venue : this.note, multiple, maxLength: key === "venue" ? 40 : 200,
      onInput: (v) => { if (key === "venue") this.venue = v; else this.note = v; this.clearBad(key); this.changed(); },
    });
  }

  /**
   * 激活的字段滚到可视底边上方 16（适配稿 2.2）。点字段、点「下一项」都走这里：
   * 键盘高度到了、或换了字段，就滚一次；本来就在可视区里不滚。220ms ease-out。
   */
  private revealFocus(top: number, bottom: number) {
    const a = this.app, f = a.focusRect;
    const key = a.editing && a.kbHeight ? `${a.editing.key}@${a.kbHeight}` : "";
    if (!key) { this.scrolledFor = ""; return; }
    if (!f || this.scrolledFor === key) return;
    this.scrolledFor = key;
    let d = 0;
    if (f.y + f.h > bottom - 16) d = f.y + f.h - (bottom - 16);
    else if (f.y < top + 8) d = f.y - (top + 8);
    if (!d) return;
    const from = this.scroll.pos, to = Math.min(Math.max(0, from + d), this.scroll.max);
    a.animate(220, (p) => { this.scroll.pos = from + (to - from) * ease.out(p); });
  }

  /** 地点或备注里有字就先确认；片区、时间这类选择项不算「填过」（适配稿 M4）。 */
  private cancel() {
    const a = this.app;
    a.blurKeyboard();
    if (!this.venue.trim() && !this.note.trim()) { this.nav.back(); return; }
    const s: Sheet = {
      dialog: true,
      draw: (ctx, y, w) => {
        const cy = sheetHeader(ctx, y, w, "不开这一局了？", "填的地点和备注不会保存", true) + 4;
        const bw = (w - 52) / 2;
        button(a, ctx, "cancel-keep", 20, cy, bw, 50, "继续填", "soft", () => a.closeSheet(s));
        button(a, ctx, "cancel-quit", 32 + bw, cy, bw, 50, "不开了", "danger", () => { a.closeSheet(s); this.nav.back(); });
        return cy + 50 + 20 - y;
      },
    };
    a.openSheet(s);
  }

  private setTime(unix: number) {
    if (unix === this.startsAt) return;
    this.startsAt = unix; this.changed();
  }

  /** 「自定义」：和「改一下这一局」同一套三行胶囊；默认选中当前时间，它已经来不及了就选最近能开的一档。 */
  private pickTime() {
    const a = this.app;
    const p = timePicker(a, this.startsAt >= Math.floor(Date.now() / 1000) + MIN_LEAD ? this.startsAt : nextSlot(), "ct", HORIZON_DAYS);
    const s: Sheet = {
      scrollerAt: (x, y) => p.scrollerAt(x, y),
      step: p.step,
      draw: (ctx, y, w) => {
        let cy = sheetHeader(ctx, y, w, "什么时候开", `最早 10 分钟后，最晚 ${HORIZON_DAYS} 天内`) + 6;
        cy = p.draw(ctx, cy) + 10;
        const u = p.unix(), late = u < Math.floor(Date.now() / 1000) + MIN_LEAD;
        cy = sheetButtons(a, ctx, cy, w, [
          { label: late ? "这个时间来不及了" : `就 ${weekday(u)} ${slashDate(u)} ${timeHM(u)}`, style: "primary", disabled: late, onTap: () => {
            this.custom = this.times.some((t) => t.unix === u) ? null : u;
            this.setTime(u);
            a.closeSheet(s); a.redraw();
          } },
          { label: "算了", style: "link", onTap: () => a.closeSheet(s) },
        ], "ct-sb");
        return cy - y + 8;
      },
    };
    a.openSheet(s);
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
}
