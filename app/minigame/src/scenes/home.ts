// 02 · 首页牌局列表（设计稿 A2 / A3 / W4 / R2）。
// 标题和筛选固定在上面，列表单独滚动，下拉刷新。实时按片区订阅 feed:{area}，收到就重拉。

import type { App, Scene } from "../core/app";
import { C, Ctx, Scroller, card, circle, ellipsize, fillRR, font, initial, strokeRR, text } from "../core/ui";
import { PAD, banner, chip, disclaimer, emptyState, errorLine, icon, segmented, textLink, type Suit } from "../core/widgets";
import { leaveGame, listGames, type GameSummary } from "../lib/api";
import { cardDay, kindLabel, missingLabel, slashDate, timeHM, dayShort } from "../lib/fmt";
import { realtime } from "../lib/realtime";
import { currentUser } from "../lib/session";
import { APP_NAME } from "../../../shared/src/appName";
import type { Notice } from "../../../shared/src/status";
import { dismissNotice, observeNotices, summaryView } from "../lib/notice";
import { promotedSheet } from "./sheets";
import type { Nav } from "./nav";

export const AREAS = ["SCBD", "PIK", "Central Park", "Kemang", "Kuningan", "Senayan", "Kelapa Gading", "BSD", "Other"];
const DAYS: { value: "" | "today" | "tomorrow" | "weekend"; label: string }[] = [
  { value: "", label: "全部" }, { value: "today", label: "今天" }, { value: "tomorrow", label: "明天" }, { value: "weekend", label: "周末" },
];
const CARD_H = 116, GAP = 14;
export const TAB_H = 56;

export function rescheduleText(n: Extract<Notice, { kind: "rescheduled" }>, withVenue = false): string {
  const when = `${dayShort(n.startsAt)} ${timeHM(n.startsAt)}`;
  if (n.timeChanged && n.venueChanged) return `局头把时间改到 ${when}、地点改到 ${n.venue} 了`;
  if (n.venueChanged) return `局头把地点改到 ${n.venue} 了`;
  // 换设备兜底（#160）：只知道改过，不知道改的是时间还是地点，把现在的都说出来。
  if (!n.timeChanged) return `局头改过时间或地点，现在是 ${when} · ${n.venue}`;
  return withVenue ? `局头把 ${n.venue} 那局改到 ${when} 了` : `局头把时间改到 ${when} 了`;
}

export class Home implements Scene {
  tab = "games";
  private day = 0;
  private area = "";
  private games: GameSummary[] | null = null;
  private error = "";
  private notices: Notice[] = [];
  private list = new Scroller();
  private chips = new Scroller();
  private unreg: (() => void) | null = null;
  private loading = false;
  private promptedPromoted = new Set<string>();
  private chipY = 0;
  private listTop = 0;
  private listBottom = 0;

  constructor(private app: App, private nav: Nav) {
    this.list.onPullRefresh = () => void this.load(true);
  }

  onEnter() {
    void this.load();
    this.subscribe();
  }
  onLeave() {
    this.unreg?.();
    this.unreg = null;
  }
  onResume() {
    void this.load();
  }

  private subscribe() {
    this.unreg?.();
    const topics = (this.area ? [this.area] : AREAS).map((a) => `feed:${a}`);
    this.unreg = realtime.register({ topics, poll: true, onEvent: () => void this.load(), refresh: () => void this.load() });
  }

  async load(manual = false) {
    if (this.loading) return;
    this.loading = true;
    const day = DAYS[this.day].value, area = this.area;
    try {
      const games = await listGames({ day, area });
      if (day !== DAYS[this.day].value || area !== this.area) return; // 筛选已经变了
      this.games = games;
      this.error = "";
      const me = currentUser();
      this.notices = me ? games.filter((g) => g.my_role !== undefined).flatMap((g) => observeNotices(summaryView(g), me.id)) : [];
      this.maybePromoted();
      if (manual) this.app.toast("刷新好了", 1000);
    } catch {
      this.error = "列表没拉到，下拉再试一次";
      if (!this.games) this.games = [];
    } finally {
      this.loading = false;
      this.app.redraw();
    }
  }

  private dismiss(n: Notice) {
    const g = this.games?.find((x) => x.short_code === n.code);
    if (g) dismissNotice(summaryView(g), currentUser()?.id, n.kind);
    this.notices = this.notices.filter((x) => !(x.code === n.code && x.kind === n.kind));
    this.app.redraw();
  }

  /** W3：候补转正。首页也弹，一局只弹一次。 */
  private maybePromoted() {
    const n = this.notices.find((x) => x.kind === "promoted");
    if (!n || this.promptedPromoted.has(n.code) || this.app.top !== this) return;
    const g = this.games?.find((x) => x.short_code === n.code);
    if (!g) return;
    this.promptedPromoted.add(n.code);
    promotedSheet(this.app, g, {
      onOk: () => this.dismiss(n),
      onCalendar: () => { this.dismiss(n); this.nav.detail(n.code); },
      onLeave: async () => {
        this.dismiss(n);
        try {
          await leaveGame(n.code);
          this.app.toast("已经让给下一位了");
        } catch {
          this.app.toast("没退成，进这一局再试一次");
        } finally {
          void this.load();
        }
      },
    });
  }

  scrollerAt(_x: number, y: number) {
    if (y >= this.chipY - 4 && y <= this.chipY + 40) return { s: this.chips, axis: "x" as const };
    if (y >= this.listTop && y <= this.listBottom) return { s: this.list, axis: "y" as const };
    return null;
  }
  step() { return this.list.step() || this.chips.step() || this.loading; }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    // 胶囊那一行只放地区
    text(ctx, "Jakarta · 雅加达时间", PAD, a.navY, font(13), C.ink3);
    if (realtime.status === "offline") {
      circle(ctx, PAD + 140, a.navY, 3, C.khaki);
      text(ctx, "离线 · 15 秒刷新", PAD + 148, a.navY, font(11), C.ink4);
    }
    const titleY = a.menu.bottom + 26;
    text(ctx, APP_NAME, PAD, titleY, font(28, "900", true), C.red);
    text(ctx, "找人凑一桌，就差你一个", PAD, titleY + 28, font(13), C.ink3);
    // 小牌顶边离胶囊底至少 8（适配稿 1.2）：牌的中心在 y + 24，半高 24
    this.drawMiniCards(ctx, W - 86, a.contentTop + 4);

    const segY = titleY + 46;
    segmented(a, ctx, "day", PAD, segY, W - PAD * 2, DAYS.map((d) => d.label), this.day, (i) => {
      if (i === this.day) return;
      this.day = i; this.games = null; this.list.scrollTo(0); void this.load();
    });
    this.chipY = segY + 56;
    this.drawChips(ctx);
    this.listTop = this.chipY + 46;
    this.listBottom = a.H - TAB_H - a.safeBottom;
    this.drawList(ctx);
  }

  private drawMiniCards(ctx: Ctx, x: number, y: number) {
    const one = (dx: number, rot: number, suit: Suit, color: string) => {
      ctx.save();
      ctx.translate(x + dx + 18, y + 24);
      ctx.rotate(rot);
      fillRR(ctx, -17, -23, 34, 46, 5, C.line);
      fillRR(ctx, -18, -24, 34, 46, 5, C.card);
      strokeRR(ctx, -18, -24, 34, 46, 5, C.line2, 1);
      icon[suit](ctx, -1, -1, 18, color);
      ctx.restore();
    };
    one(0, -0.12, "spade", C.ink);
    one(36, 0.1, "heart", C.red);
  }

  private drawChips(ctx: Ctx) {
    const a = this.app;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, this.chipY - 4, a.W, 46); ctx.clip();
    let x = PAD - this.chips.pos;
    for (const it of ["", ...AREAS]) {
      x += chip(a, ctx, "area:" + it, x, this.chipY, it || "全部", this.area === it, () => {
        if (this.area === it) return;
        this.area = it; this.games = null; this.list.scrollTo(0); this.subscribe(); void this.load();
      }) + 8;
    }
    ctx.restore();
    this.chips.setMax(x + this.chips.pos - 8 + PAD - a.W);
  }

  private drawList(ctx: Ctx) {
    const a = this.app, W = a.W, top = this.listTop, bottom = this.listBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    if (this.list.pull > 0) {
      text(ctx, this.list.pull > 64 ? "松手刷新" : "下拉刷新", W / 2, top + this.list.pull / 2, font(12), C.ink4, "center");
    }
    let y = top - this.list.pos;

    for (const n of this.notices) {
      if (n.kind === "promoted") continue;
      y += banner(a, ctx, `notice:${n.code}:${n.kind}`, PAD, y + 4, W - PAD * 2, {
        kind: n.kind === "host" ? "ink" : "red",
        icon: n.kind === "host" ? "👑" : "🕘",
        title: n.kind === "rescheduled" ? rescheduleText(n, true) : `${n.formerHost ? n.formerHost + "退出了，" : ""}你现在是局头。空出的座位已开放报名。`,
        onTap: () => { this.dismiss(n); this.nav.detail(n.code); },
        onDismiss: () => this.dismiss(n),
      }) + 10;
    }

    text(ctx, "可报名的局", PAD, y + 18, font(20, "900", true), C.ink);
    const games = this.games;
    if (games && games.length) {
      const waiting = games.reduce((s, g) => s + g.missing, 0);
      text(ctx, `${waiting} 桌在等人`, W - PAD, y + 20, font(12), C.ink3, "right");
    }
    y += 44;
    if (this.error) y += errorLine(a, ctx, y, this.error);
    if (games === null) y += emptyState(a, ctx, y, ["正在找局…"]);
    else if (games.length === 0 && !this.error) {
      y += emptyState(a, ctx, y, ["这几天还没有局。"]);
      textLink(a, ctx, "empty-create", W / 2, y + 4, "开一局 ›", () => this.nav.create(), C.red);
      y += 40;
    }
    for (const g of games ?? []) {
      if (y + CARD_H > top - 10 && y < bottom + 10) this.drawCard(ctx, g, PAD, y, W - PAD * 2);
      y += CARD_H + GAP;
    }
    y += disclaimer(a, ctx, y);
    ctx.restore();
    this.list.setMax(y + this.list.pos - bottom);
  }

  private drawCard(ctx: Ctx, g: GameSummary, x: number, y: number, w: number) {
    const a = this.app, id = "card:" + g.short_code;
    const me = currentUser();
    ctx.save();
    if (a.isPressed(id)) { ctx.translate(x + w / 2, y + CARD_H / 2); ctx.scale(0.98, 0.98); ctx.translate(-x - w / 2, -y - CARD_H / 2); }
    card(ctx, x, y, w, CARD_H, 18);
    fillRR(ctx, x + 14, y + 14, 82, CARD_H - 28, 10, C.paper);
    text(ctx, cardDay(g.starts_at), x + 55, y + 34, font(12, "700"), C.red, "center");
    text(ctx, timeHM(g.starts_at), x + 55, y + 59, font(26, "900", true), C.ink, "center");
    text(ctx, slashDate(g.starts_at), x + 55, y + 84, font(11), C.ink3, "center");

    const rx = x + 110, rw = w - 110 - 14;
    ctx.font = font(12);
    const tagW = ctx.measureText(g.area).width + 14;
    fillRR(ctx, rx, y + 16, tagW, 22, 6, C.sand);
    text(ctx, g.area, rx + tagW / 2, y + 27, font(12), C.ink2, "center");
    text(ctx, kindLabel(g.kind), rx + tagW + 8, y + 27, font(12), C.ink3);
    text(ctx, ellipsize(ctx, g.venue, rw, font(16, "700")), rx, y + 55, font(16, "700"), C.ink);

    for (let no = 1; no <= 4; no++) {
      const cx = rx + 14 + (no - 1) * 32, cy = y + 88;
      const seat = g.seat_list?.find((s) => s.seat_no === no);
      if (seat) {
        const host = seat.user.id === g.host.id, isMe = !!me && seat.user.id === me.id;
        circle(ctx, cx, cy, 13, isMe && !host ? C.gold : host ? C.redTint : C.beige);
        text(ctx, initial(seat.user.nickname), cx, cy + 1, font(13, "900", true), host ? C.red : C.ink, "center");
      } else circle(ctx, cx, cy, 12.5, null, C.khaki, 1.2, [3, 3]);
    }
    // W4：我在这局里时，标签换成我的位置
    let label: string, bg: string, fg: string;
    if (g.my_role === "seated") { label = "已报名"; bg = C.greenTint; fg = C.green; }
    else if (g.my_role === "waitlist") { label = g.waitlist_pos ? `候补第 ${g.waitlist_pos} 位` : "候补中"; bg = C.sand; fg = C.ink2; }
    else if (g.status === "full") { label = `满 · ${g.seats}/4`; bg = C.beige; fg = C.ink3; }
    else { label = missingLabel(g.missing); bg = g.missing === 1 ? C.red : C.redTint; fg = g.missing === 1 ? C.cream : C.red; }
    ctx.font = font(13, "700");
    const bw = ctx.measureText(label).width + 22, bx = x + w - 14 - bw;
    fillRR(ctx, bx, y + 74, bw, 28, 8, bg);
    text(ctx, label, bx + bw / 2, y + 88, font(13, "700"), fg, "center");
    ctx.restore();
    const vy = Math.max(y, this.listTop), vh = Math.min(y + CARD_H, this.listBottom) - vy;
    if (vh > 0) a.hit(id, x, vy, w, vh, () => this.nav.detail(g.short_code));
  }
}
