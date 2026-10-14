// 03 · 牌局详情（设计稿 B1 / B3 / W1 / W2 / R1 / R3 / K1–K6）。
// 状态全部来自服务端：me.role 说明我在不在局里，missing 说明差几个人，status 说明还能不能报。

import type { App, Scene, Sheet } from "../core/app";
import { C, Ctx, Scroller, circle, ease, fillRR, font, initial, measure, strokeRR, text, wrap } from "../core/ui";
import { PAD, banner, button, chipsWrap, disclaimer, errorLine, field, nav, sheetButtons, sheetHeader, simpleSheet, type BtnStyle } from "../core/widgets";
import {
  ApiError, cancelGame, getGame, inviteError, inviteFriends, inviteSummary, joinGame, kickPlayer, leaveGame, subscribeRetry, updateGame,
  type GameDetail, type GamePatch, type Seat,
} from "../lib/api";
import { dayLabel, kindLabel, timeHM, wibAt, wibParts, weekday, slashDate } from "../lib/fmt";
import { realtime } from "../lib/realtime";
import { currentUser } from "../lib/session";
import { track } from "../lib/telemetry";
import { conflictingFields, describeConflict, type FieldDiff } from "../../../h5/src/conflict";
import { acknowledge, fromDetail, observe, type Notice, type NoticeKind } from "../../../h5/src/status";
import { rescheduleText } from "./home";
import type { Nav } from "./nav";
import { addToCalendar, friendPicker, joinedSheet, promotedSheet, requireIdentity, shareSheet } from "./sheets";

const POS = ["top", "right", "bottom", "left"] as const;

export class Detail implements Scene {
  private detail: GameDetail | null = null;
  private error = "";
  private busy = false;
  private retrying = false;
  private notices: Notice[] = [];
  private viewers = 0;
  private scroll = new Scroller();
  private unreg: (() => void) | null = null;
  private unretry: (() => void) | null = null;
  private unstatus: (() => void) | null = null;
  private seen = new Set<string>();
  private drop: Record<string, number> = {};
  private promotedOpen = false;
  private joinedOpen = false;
  private viewed = false;
  private barH = 76;

  constructor(private app: App, private code: string, private nav: Nav) {
    this.scroll.onPullRefresh = () => void this.load();
  }

  onEnter() {
    void this.load();
    this.unreg = realtime.register({
      topics: [`game:${this.code}`],
      poll: true,
      onEvent: (ev) => {
        if (ev.type === "presence") {
          const v = (ev.data as any)?.viewers;
          if (typeof v === "number") { this.viewers = v; this.app.redraw(); }
          return;
        }
        void this.load();
      },
      refresh: () => void this.load(),
    });
    this.unretry = subscribeRetry((v) => { this.retrying = v; this.app.redraw(); });
    this.unstatus = realtime.onStatus((s) => { if (s !== "online") { this.viewers = 0; this.app.redraw(); } });
  }
  onLeave() {
    this.unreg?.(); this.unretry?.(); this.unstatus?.();
  }

  private async load() {
    try {
      this.set(await getGame(this.code));
      this.error = "";
    } catch (err) {
      this.error = err instanceof ApiError && err.status === 404 ? "这一局不在了" : "打不开这一局，下拉重试";
    } finally {
      this.app.redraw();
    }
  }

  /** 拿到新数据：播落座动画、和本机快照比出提示（#114）。 */
  private set(d: GameDetail) {
    const ids = d.seats.map((s) => s.user.id);
    if (this.seen.size > 0) {
      for (const id of ids) if (!this.seen.has(id)) {
        this.drop[id] = 0;
        this.app.animate(380, (p) => { this.drop[id] = ease.spring(p); }, () => { delete this.drop[id]; });
      }
    }
    this.seen = new Set(ids);
    this.detail = d;
    if (!this.viewed) { this.viewed = true; track("detail_view", { game: this.code }); }
    this.notices = observe(fromDetail(d), currentUser()?.id);
    this.maybePromoted();
  }

  private dismiss(kind: NoticeKind) {
    if (this.detail) acknowledge(fromDetail(this.detail), currentUser()?.id, [kind]);
    this.notices = this.notices.filter((n) => n.kind !== kind);
    this.app.redraw();
  }

  private get closed() { const s = this.detail?.game.status; return s === "cancelled" || s === "done"; }
  private get isHost() { const me = currentUser(); return !!me && this.detail?.host.id === me.id; }

  private maybePromoted() {
    const d = this.detail;
    if (!d || this.closed || this.joinedOpen || this.promotedOpen || !this.notices.some((n) => n.kind === "promoted")) return;
    this.promotedOpen = true;
    promotedSheet(this.app, d.game, {
      onOk: () => { this.promotedOpen = false; this.dismiss("promoted"); },
      onCalendar: () => { this.promotedOpen = false; this.dismiss("promoted"); addToCalendar(this.app, this.detail!); },
      onLeave: () => { this.promotedOpen = false; this.dismiss("promoted"); void this.run(() => leaveGame(this.code)); },
    });
  }

  private explain(err: unknown): string {
    if (err instanceof ApiError) {
      if (err.isClosed) return "这一局已经结束或取消了";
      if (err.code === "banned") return "你被移出过这一局，不能再报名";
      if (err.code === "quota_exceeded") return err.message;
      if (err.code === "game_started") return "这一局已经开始了，问问局头还能不能加";
      if (err.code === "network") return err.message;
      if (err.status === 503) return "人有点多，稍等一下再试";
      return err.message;
    }
    return "操作没成功，请再试一次";
  }

  private async run(fn: () => Promise<GameDetail | void>): Promise<boolean> {
    if (this.busy) return false;
    this.busy = true; this.app.redraw();
    try {
      const next = await fn();
      if (next) this.set(next); else await this.load();
      this.error = "";
      return true;
    } catch (err) {
      this.error = this.explain(err);
      return false;
    } finally {
      this.busy = false; this.app.redraw();
    }
  }

  private async onJoin() {
    if (!currentUser()) {
      const u = await requireIdentity(this.app);
      if (!u) return;
    }
    track("join_click", { game: this.code });
    let seated = false;
    const ok = await this.run(async () => {
      const next = await joinGame(this.code);
      seated = next.me.role === "seat";
      return next;
    });
    if (ok) track("join_ok", { game: this.code });
    if (ok && seated && this.detail) {
      wx.vibrateShort?.({ type: "light" });
      this.joinedOpen = true;
      joinedSheet(this.app, this.detail, () => addToCalendar(this.app, this.detail!));
      // 弹层关掉之后再允许弹补位
      setTimeout(() => { this.joinedOpen = false; }, 600);
    }
  }

  // ── 局头：移出、取消、改时间 / 地点（K1 / K3 / K4 / K5 / K6）──

  private onSeat(seat: Seat) {
    if (!this.isHost || this.closed) return;
    if (seat.user.id === currentUser()?.id) { this.app.toast("局头不能移出自己，想走请点「退出」"); return; }
    simpleSheet(this.app, {
      title: seat.user.nickname,
      sub: "移出后 TA 不能再报这局，会收到通知；有候补会自动补上",
      buttons: (s) => [
        { label: "移出这局", style: "danger", onTap: () => { this.app.closeSheet(s); void this.run(() => kickPlayer(this.code, seat.user.id)); } },
        { label: "再想想", style: "soft", onTap: () => this.app.closeSheet(s) },
      ],
    });
  }

  private onCancel() {
    const d = this.detail!;
    simpleSheet(this.app, {
      title: "取消这一局？",
      sub: `会通知 ${d.seats.length} 位座位上的人${d.waitlist.length > 0 ? `和 ${d.waitlist.length} 位候补` : ""}，取消后不能恢复。`,
      buttons: (s) => [
        { label: "取消这局", style: "danger", onTap: () => {
          this.app.closeSheet(s);
          void this.run(async () => { await cancelGame(this.code); this.app.toast("这一局已取消"); });
        } },
        { label: "再想想", style: "soft", onTap: () => this.app.closeSheet(s) },
      ],
    });
  }

  private openEdit() {
    const base = this.detail!;
    const parts = wibParts(base.game.starts_at);
    const st = {
      day: Math.max(0, parts.dayOffset), hour: parts.hour, minute: parts.minute,
      venue: base.game.venue, err: null as null | { field: string; message: string }, busy: false,
    };
    const days = Array.from({ length: 8 }, (_, i) => {
      const t = wibAt(i, 12, 0);
      return i === 0 ? "今天" : i === 1 ? "明天" : `${weekday(t)} ${slashDate(t)}`;
    });
    const hours = Array.from({ length: 15 }, (_, i) => 9 + i);
    const minutes = [0, 15, 30, 45];
    const pad = (n: number) => String(n).padStart(2, "0");
    const save = () => {
      const patch: GamePatch = {};
      const next = wibAt(st.day, st.hour, st.minute);
      if (next !== base.game.starts_at) patch.starts_at = next;
      if (st.venue.trim() && st.venue.trim() !== base.game.venue) patch.venue = st.venue.trim();
      if (Object.keys(patch).length === 0) { this.app.closeSheet(sheet); return; }
      void this.submitEdit(patch, base, sheet, st);
    };
    const sheet: Sheet = {
      draw: (ctx, y, w) => {
        let cy = sheetHeader(ctx, y, w, "改一下这一局", "改完会通知座位上的人，他们那边的提醒也会跟着更新");
        text(ctx, "哪天", 20, cy + 8, font(13, "700"), C.ink2); cy += 24;
        cy = chipsWrap(this.app, ctx, "ed-day", 20, cy, w - 40, days, (i) => i === st.day, (i) => { st.day = i; }) + 12;
        text(ctx, "几点（雅加达时间）", 20, cy + 8, font(13, "700"), C.ink2); cy += 24;
        cy = chipsWrap(this.app, ctx, "ed-h", 20, cy, w - 40, hours.map((h) => `${h}点`), (i) => hours[i] === st.hour, (i) => { st.hour = hours[i]; }) + 8;
        cy = chipsWrap(this.app, ctx, "ed-m", 20, cy, w - 40, minutes.map((m) => `:${pad(m)}`), (i) => minutes[i] === st.minute, (i) => { st.minute = minutes[i]; }) + 14;
        cy += field(this.app, ctx, 20, cy, w - 40, {
          key: "edit-venue", value: st.venue, placeholder: "地点", maxLength: 40,
          error: st.err ? st.err.message : undefined,
          onChange: (v) => { st.venue = v; st.err = null; },
        }) + 14;
        text(ctx, `改成 ${days[st.day]} ${pad(st.hour)}:${pad(st.minute)}`, w / 2, cy + 6, font(12), C.ink3, "center");
        cy = sheetButtons(this.app, ctx, cy + 22, w, [
          { label: st.busy ? "正在保存…" : "保存", style: "primary", disabled: st.busy, onTap: save },
          { label: "算了", style: "link", onTap: () => this.app.closeSheet(sheet) },
        ]);
        return cy - y + 8;
      },
    };
    this.app.openSheet(sheet);
  }

  /** 409 后先看冲突和这次修改有没有关系：没关系就带新版本号重提（K5），有关系才让局头选（K4）。 */
  private async submitEdit(patch: GamePatch, baseline: GameDetail, sheet: Sheet, st: { busy: boolean; err: null | { field: string; message: string } }) {
    st.busy = true; this.app.redraw();
    try {
      this.set(await updateGame(this.code, patch, baseline.game.version));
      this.app.closeSheet(sheet);
      this.app.toast("改好了，已经通知座位上的人");
    } catch (err) {
      if (err instanceof ApiError && err.isVersionConflict) {
        const latest = err.body as GameDetail;
        this.app.closeSheet(sheet);
        if (conflictingFields(patch, baseline, latest).length === 0) await this.retryEdit(patch, latest);
        else this.conflictDialog(patch, latest, describeConflict(patch, baseline, latest));
      } else if (err instanceof ApiError && err.code === "sensitive_word") {
        st.err = { field: err.field, message: err.message };
      } else if (err instanceof ApiError && err.code === "not_host") {
        this.app.closeSheet(sheet);
        this.app.toast("你已经不是局头了");
        await this.load();
      } else {
        this.app.closeSheet(sheet);
        this.error = this.explain(err);
      }
    } finally {
      st.busy = false; this.app.redraw();
    }
  }

  private async retryEdit(patch: GamePatch, latest: GameDetail) {
    try {
      this.set(await updateGame(this.code, patch, latest.game.version));
      this.app.toast("已保存 · 期间有人报名");
    } catch (err) {
      if (err instanceof ApiError && err.isVersionConflict) {
        const again = err.body as GameDetail;
        this.conflictDialog(patch, again, describeConflict(patch, latest, again));
        return;
      }
      this.error = this.explain(err);
    } finally { this.app.redraw(); }
  }

  private conflictDialog(patch: GamePatch, latest: GameDetail, diffs: FieldDiff[]) {
    const app = this.app;
    const s: Sheet = {
      dialog: true,
      draw: (ctx, y, w) => {
        let cy = sheetHeader(ctx, y, w, "这局刚被改过", "你改的这几项别人也动了，想用哪一边？", true);
        for (const d of diffs) {
          fillRR(ctx, 20, cy, w - 40, 70, 12, C.paper);
          text(ctx, d.label, 34, cy + 18, font(12, "700"), C.ink3);
          text(ctx, `我：${d.mine}`, 34, cy + 38, font(13, "600"), C.red);
          text(ctx, `现在：${d.theirs}`, 34, cy + 56, font(13), C.ink2);
          cy += 78;
        }
        cy = sheetButtons(app, ctx, cy + 4, w, [
          { label: "用对方的版本", style: "soft", onTap: () => { app.closeSheet(s); this.set(latest); app.toast("已用对方的版本"); } },
          { label: "仍然改成我的", style: "primary", onTap: () => { app.closeSheet(s); void this.keepMine(patch, latest); } },
          { label: "先不改了", style: "link", onTap: () => app.closeSheet(s) },
        ]);
        return cy - y + 8;
      },
    };
    app.openSheet(s);
  }

  private async keepMine(patch: GamePatch, latest: GameDetail) {
    await this.run(async () => {
      const next = await updateGame(this.code, patch, latest.game.version);
      this.app.toast("已按你的版本改好");
      return next;
    });
  }

  private invite() {
    const d = this.detail!;
    let inviting = false;
    friendPicker(this.app, {
      inGame: new Map<string, string>([
        ...d.seats.map((x) => [x.user.id, "已在座位上"] as [string, string]),
        ...d.waitlist.map((w) => [w.user.id, `候补第 ${w.position} 位`] as [string, string]),
      ]),
      confirmLabel: (n) => (n > 0 ? `邀请 ${n} 位` : "选几位牌友"),
      busy: () => inviting,
      onConfirm: async (picked, sheet) => {
        inviting = true; this.app.redraw();
        try {
          this.app.toast(inviteSummary(await inviteFriends(this.code, picked.map((f) => f.user.id))), 2600);
          this.app.closeSheet(sheet);
        } catch (err) {
          this.app.toast(inviteError(err));
        } finally {
          inviting = false; this.app.redraw();
        }
      },
    });
  }

  // ── 画 ──

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - this.barH - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, d = this.detail;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    if (!d) {
      nav(a, ctx, "牌局详情", () => this.nav.back());
      if (this.error) errorLine(a, ctx, a.contentTop + 40, this.error);
      else text(ctx, "正在打开…", W / 2, a.contentTop + 60, font(15), C.ink3, "center");
      return;
    }
    const me = currentUser(), closed = this.closed, g = d.game;
    const actions = this.actions(d);
    this.barH = actions.length > 2 ? 136 : 76;
    const top = a.contentTop, bottom = a.H - this.barH - a.safeBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    if (!closed) for (const n of this.notices) {
      if (n.kind === "promoted") continue;
      y += banner(a, ctx, `notice:${n.kind}`, PAD, y, W - PAD * 2, {
        kind: n.kind === "host" ? "ink" : "red", icon: n.kind === "host" ? "👑" : "🕘",
        title: n.kind === "rescheduled" ? rescheduleText(n) : `${n.formerHost ? n.formerHost + "退出了，" : ""}你现在是局头。空出的座位已开放报名。`,
        onDismiss: () => this.dismiss(n.kind),
      }) + 10;
    }
    if (d.me.invited_by && d.me.role === "none" && !closed) {
      y += banner(a, ctx, "invited", PAD, y, W - PAD * 2, {
        kind: "green", dot: true, title: `${d.me.invited_by.nickname} 邀请了你`,
        sub: d.missing > 0 ? `还差 ${d.missing} 人，报个名占座吧` : "已经满了，可以排候补",
      }) + 10;
    }
    const myWait = d.me.role === "waitlist" ? d.waitlist.find((w) => w.user.id === me?.id) : undefined;
    if (myWait && !closed) {
      y += banner(a, ctx, "wait", PAD, y, W - PAD * 2, { kind: "strip", dot: true, title: `你是候补第 ${myWait.position} 位`, sub: "有人退出会自动补上你" }) + 10;
    }
    if (closed) {
      y += banner(a, ctx, "closed", PAD, y, W - PAD * 2, { kind: "red", title: g.status === "cancelled" ? "这一局已经取消了" : "这一局已经结束" }) + 10;
    }

    ctx.globalAlpha = closed ? 0.55 : 1;
    text(ctx, dayLabel(g.starts_at), W / 2, y + 14, font(14, "700"), C.red, "center");
    text(ctx, timeHM(g.starts_at), W / 2, y + 54, font(44, "900", true), C.ink, "center");
    text(ctx, `雅加达时间 WIB · ${kindLabel(g.kind)}`, W / 2, y + 88, font(13), C.ink3, "center");
    ctx.globalAlpha = 1;
    y += 116;
    y = this.drawTable(ctx, d, y);
    y = this.drawNames(ctx, d, y);
    y = this.drawInfo(ctx, d, y);

    if (d.me.role === "seat" && !closed) {
      button(a, ctx, "calendar", PAD, y, W - PAD * 2, 46, "加入日历", "ghost", () => addToCalendar(a, d));
      y += 56;
    }
    if (this.isHost && !closed) {
      button(a, ctx, "invite", PAD, y, W - PAD * 2, 46, "＋ 邀请牌友", "ghost", () => this.invite());
      y += 56;
    }
    y += disclaimer(a, ctx, y);
    if (this.error) y += errorLine(a, ctx, y, this.error);
    y += 12;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);

    nav(a, ctx, "牌局详情", () => this.nav.back());
    this.drawBar(ctx, actions);
  }

  private drawTable(ctx: Ctx, d: GameDetail, y: number): number {
    const a = this.app, me = currentUser(), cx = a.W / 2, size = 196, x = cx - size / 2;
    const closed = this.closed, ty = y + 34, mid = ty + size / 2;
    fillRR(ctx, x, ty + 6, size, size, 24, "rgba(142,42,35,0.25)");
    fillRR(ctx, x, ty, size, size, 24, closed ? "#8a7d70" : C.redDeep);
    strokeRR(ctx, x + 3, ty + 3, size - 6, size - 6, 21, closed ? "#6f645a" : C.redInk, 6);
    strokeRR(ctx, x + 22, ty + 22, size - 44, size - 44, 14, "rgba(233,196,106,0.45)", 1, [4, 3]);
    text(ctx, `${d.seats.length}/${d.game.capacity}`, cx, mid - 6, font(40, "900", true), C.cream, "center");
    const label = d.game.status === "cancelled" ? "已取消" : d.game.status === "done" ? "已结束"
      : d.game.status !== "full" ? "对家为搭档" : d.waitlist.length > 0 ? `已成局 · ${d.waitlist.length} 人候补` : "已成局";
    text(ctx, label, cx, mid + 26, font(12), "rgba(255,247,236,0.8)", "center");

    const at = { top: [cx, ty], right: [x + size, mid], bottom: [cx, ty + size], left: [x, mid] } as const;
    const bySeat = new Map(d.seats.map((s) => [s.seat_no, s]));
    for (let no = 1; no <= 4; no++) {
      const [sx, sy] = at[POS[(no - 1) % 4]];
      const seat = bySeat.get(no);
      if (!seat) {
        circle(ctx, sx, sy, 30, C.paper);
        circle(ctx, sx, sy, 30, null, C.khaki, 2, [5, 4]);
        if (d.me.role === "none" && !closed && d.missing > 0) {
          text(ctx, "+", sx, sy, font(24, "300"), C.khaki, "center");
          a.hit(`seat:${no}`, sx - 32, sy - 32, 64, 64, () => void this.onJoin());
        }
        continue;
      }
      const host = seat.user.id === d.host.id, isMe = !!me && seat.user.id === me.id;
      const k = this.drop[seat.user.id] ?? 1;
      const r = 32 * k;
      circle(ctx, sx, sy, r + 4, C.paper);
      circle(ctx, sx, sy, r, isMe ? C.gold : host ? C.redTint : C.beige, host ? "#e3b5ae" : "#e6dccd", 3);
      text(ctx, initial(seat.user.nickname), sx, sy + 1, font(Math.max(1, 26 * k), "900", true), host ? C.red : C.ink, "center");
      if (host && isMe) {
        fillRR(ctx, sx - 20, sy + r - 8, 40, 18, 9, C.red);
        text(ctx, "局头", sx, sy + r + 1, font(10, "700"), C.cream, "center");
      }
      if (this.isHost && !closed) a.hit(`seat:${no}`, sx - 34, sy - 34, 68, 68, () => this.onSeat(seat));
    }
    return ty + size + 46;
  }

  private drawNames(ctx: Ctx, d: GameDetail, y: number): number {
    const a = this.app, me = currentUser();
    const items: { t: string; wait?: boolean }[] = [
      ...d.seats.map((s) => ({ t: (s.user.id === d.host.id ? `局头 · ${s.user.nickname}` : s.user.nickname) + (s.user.id === me?.id ? "（我）" : "") })),
      ...d.waitlist.map((w) => ({ t: `候补 ${w.position} · ${w.user.nickname}`, wait: true })),
    ];
    if (this.viewers > 1 && !this.closed && realtime.status === "online") items.push({ t: `${this.viewers} 人正在看` });
    const f = font(13);
    // 居中换行
    const rows: { t: string; wait?: boolean; w: number }[][] = [[]];
    let rowW = 0;
    for (const it of items) {
      const w = measure(ctx, it.t, f) + 24;
      if (rowW + w > a.W - PAD * 2 && rows[rows.length - 1].length) { rows.push([]); rowW = 0; }
      rows[rows.length - 1].push({ ...it, w });
      rowW += w + 8;
    }
    rows.forEach((row, ri) => {
      const total = row.reduce((s, c) => s + c.w, 0) + 8 * (row.length - 1);
      let x = (a.W - total) / 2;
      const ry = y + ri * 38;
      for (const c of row) {
        fillRR(ctx, x, ry, c.w, 30, 15, c.wait ? C.sand : C.card);
        if (!c.wait) strokeRR(ctx, x + 0.5, ry + 0.5, c.w - 1, 29, 15, C.line, 1);
        text(ctx, c.t, x + c.w / 2, ry + 15, f, C.ink2, "center");
        x += c.w + 8;
      }
    });
    return y + rows.length * 38 + 8;
  }

  private drawInfo(ctx: Ctx, d: GameDetail, y: number): number {
    const a = this.app, x = PAD, w = a.W - PAD * 2, lw = 64, vw = w - 32 - lw;
    const rows: [string, string][] = [["地点", d.game.venue], ["商圈", d.game.area], ["局头", d.host.nickname]];
    if (d.game.note) rows.push(["备注", d.game.note]);
    const f = font(15);
    const lines = rows.map((r) => wrap(ctx, r[1], vw, f));
    const hs = lines.map((l) => Math.max(52, 20 + l.length * 22 + 10));
    const h = hs.reduce((s, v) => s + v, 0);
    fillRR(ctx, x, y + 2, w, h, 18, C.line);
    fillRR(ctx, x, y, w, h, 18, C.card);
    strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 18, C.line, 1);
    let ry = y;
    rows.forEach((r, i) => {
      text(ctx, r[0], x + 16, ry + 26, font(13), C.ink3);
      lines[i].forEach((ln, j) => text(ctx, ln, x + 16 + lw, ry + 26 + j * 22, f, C.ink));
      ry += hs[i];
      if (i < rows.length - 1) { ctx.fillStyle = C.line2; ctx.fillRect(x + 16, ry, w - 32, 1); }
    });
    return y + h + 16;
  }

  private actions(d: GameDetail): { id: string; label: string; style: BtnStyle; onTap: () => void; disabled?: boolean }[] {
    const closed = this.closed, out: { id: string; label: string; style: BtnStyle; onTap: () => void; disabled?: boolean }[] = [];
    if (!closed) out.push({ id: "share", label: "分享", style: "dark", onTap: () => { track("share_open", { game: this.code }); shareSheet(this.app, d); } });
    if (d.me.role !== "none") out.push({
      id: "leave", label: d.me.role === "waitlist" ? "候补中 · 退出候补" : "已报名 · 退出", style: "ghost",
      disabled: this.busy || closed, onTap: () => void this.run(() => leaveGame(this.code)),
    });
    if (this.isHost && !closed) {
      out.push({ id: "edit", label: "改时间 / 地点", style: "soft", onTap: () => this.openEdit() });
      out.push({ id: "cancel", label: "取消这局", style: "danger", disabled: this.busy || this.retrying, onTap: () => this.onCancel() });
    }
    if (d.me.role === "none") out.push({
      id: "join", style: "primary", disabled: this.busy || this.retrying || closed, onTap: () => void this.onJoin(),
      label: this.retrying ? "人有点多，正在重试…" : this.busy ? "稍等…" : closed ? "已结束" : d.missing > 0 ? "报名占座" : `排候补 · 第 ${d.waitlist.length + 1} 位`,
    });
    return out;
  }

  private drawBar(ctx: Ctx, actions: ReturnType<Detail["actions"]>) {
    const a = this.app, W = a.W, y = a.H - this.barH - a.safeBottom;
    ctx.fillStyle = C.card;
    ctx.fillRect(0, y, W, this.barH + a.safeBottom);
    ctx.fillStyle = C.line;
    ctx.fillRect(0, y, W, 1);
    const h = 52, gap = 12;
    if (actions.length <= 2) {
      // 一排：分享窄一点，主按钮占剩下的
      const narrow = actions.length === 2 ? 110 : W - PAD * 2;
      let x = PAD;
      actions.forEach((b, i) => {
        const w = i === 0 && actions.length === 2 ? narrow : W - PAD - x;
        button(a, ctx, "bar:" + b.id, x, y + 12, w, h, b.label, b.style, b.onTap, { disabled: b.disabled });
        x += w + gap;
      });
    } else {
      const w = (W - PAD * 2 - gap) / 2;
      actions.forEach((b, i) => {
        const bx = PAD + (i % 2) * (w + gap), by = y + 12 + Math.floor(i / 2) * (h + 8);
        button(a, ctx, "bar:" + b.id, bx, by, w, h, b.label, b.style, b.onTap, { disabled: b.disabled, size: 15 });
      });
    }
  }
}
