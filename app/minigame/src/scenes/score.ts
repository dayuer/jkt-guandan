// 牌桌计分（#165，设计稿 design/guandan-minigame/README.md 第七节，截图 S3–S15）。
// 记分页（级牌、升级路线、上一局）、记一局 / 改一局弹层、每局记录、本场结束、不升级、旁观、战报卡片。
// 级数、打谁的级、过 A、没过几次全由服务端算（GET/POST/PUT /api/games/{code}/score…）；
// 本地的 lib/scoring.ts 只在服务端回话之前做预览。不涉及金钱（#91）。

import type { App, Scene, Sheet } from "../core/app";
import { C, Ctx, Scroller, card, circle, ease, ellipsize, fillRR, font, initial, measure, strokeRR, text } from "../core/ui";
import { PAD, button, chevron, errorLine, icon, nav } from "../core/widgets";
import { ApiError, editRound, getGame, getScore, newIdempotencyKey, recordRound, type GameDetail } from "../lib/api";
import { cardDay, kindLabel, slashDate, timeHM, weekday } from "../lib/fmt";
import { realtime } from "../lib/realtime";
import {
  A, RevisionGate, TEAM_NAME, aTip, boardState, boardTitle, compute, decideConflict, durationLong, durationShort,
  factOf, howText, isStarted, levelName, mateOf, matchDuration, other, placeName, playingTag, previewEdit, previewText,
  resultLine, sameFact, teamOf, whoLine,
  type Preview, type RoundFact, type ScoreEventData, type ScoreRound, type ScoreState, type Side, type StartedScore,
} from "../lib/scoring";
import { currentUser } from "../lib/session";
import type { Nav } from "./nav";
import { shareSheet } from "./sheets";

const CREAM = (a: number) => `rgba(255,247,236,${a})`;
const TEAM = {
  red: { suit: "heart" as const, c: C.red, tint: C.redTint, edge: "#e3b5ae" },
  black: { suit: "spade" as const, c: C.ink, tint: C.beige, edge: "#e6dccd" },
};
/** 翻牌：scaleX 1 → 0 换字，再 0 → 1，各 110ms（--ease-out）。 */
const FLIP_MS = 220;

/**
 * 系统「减少动态效果」：小游戏基础库没有读这个设置的接口（wx.getSystemInfoSync / getAppBaseInfo 都没有），
 * 先当作关闭。以后有了接口在这里接上，翻牌就直接换。真机核对见 #141。
 */
export function reduceMotion(): boolean {
  return false;
}

// ── 画：牌桌、级牌、头像 ──

function felt(ctx: Ctx, x: number, y: number, w: number, h: number) {
  fillRR(ctx, x, y + 6, w, h, 24, "rgba(142,42,35,0.25)");
  fillRR(ctx, x, y, w, h, 24, C.redDeep);
  strokeRR(ctx, x + 3, y + 3, w - 6, h - 6, 21, C.redInk, 6);
  strokeRR(ctx, x + 16, y + 16, w - 32, h - 32, 14, "rgba(233,196,106,0.45)", 1, [4, 3]);
}

/**
 * 级牌 104×140。inPlay：本局打这一级——上移 10、投影 9、3px 金边、顶上挂「本局打这级」。
 * 右下只放倒置的花色，不放倒置的级数（倒过来的 9 会被看成 6）。scaleX 是翻牌动画中途的横向缩放。
 */
function levelCard(ctx: Ctx, cx: number, top: number, lv: string, t: Side, o: { inPlay?: boolean; scaleX?: number; gold?: boolean } = {}) {
  const w = 104, h = 140, r = 12, T = TEAM[t];
  const y = top - (o.inPlay ? 10 : 0), x = cx - w / 2;
  ctx.save();
  if (o.scaleX !== undefined && o.scaleX !== 1) {
    ctx.translate(cx, 0); ctx.scale(Math.max(0.001, o.scaleX), 1); ctx.translate(-cx, 0);
  }
  fillRR(ctx, x, y + (o.inPlay ? 9 : 3), w, h, r, "rgba(40,10,8,0.35)");
  if (o.inPlay || o.gold) strokeRR(ctx, x - 3.5, y - 3.5, w + 7, h + 7, r + 3, C.gold, 3);
  fillRR(ctx, x, y, w, h, r, C.cream);
  strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, r, C.line2, 1);
  text(ctx, lv, x + 10, y + 18, font(16, "900"), T.c);
  icon[T.suit](ctx, x + 17, y + 38, 14, T.c);
  ctx.save(); ctx.translate(x + w - 17, y + h - 20); ctx.rotate(Math.PI); icon[T.suit](ctx, 0, 0, 14, T.c); ctx.restore();
  text(ctx, lv, cx, y + h / 2 + 4, font(lv.length > 1 ? 50 : 60, "900", true), T.c, "center");
  ctx.restore();
  if (o.inPlay) {
    const f = font(11, "900"), pw = measure(ctx, "本局打这级", f) + 18;
    fillRR(ctx, cx - pw / 2, y - 12, pw, 22, 11, C.gold);
    text(ctx, "本局打这级", cx, y - 1, f, C.redInk, "center");
  }
}

/** 头像：队色底；「我」外面 3px 金圈。 */
function teamAvatar(ctx: Ctx, cx: number, cy: number, r: number, nickname: string, t: Side, me: boolean) {
  const T = TEAM[t];
  if (me) circle(ctx, cx, cy, r + 3, null, C.gold, 3);
  circle(ctx, cx, cy, r, T.tint, T.edge, 2);
  const fs = Math.round(r * 0.8);
  text(ctx, initial(nickname), cx, cy + 1, font(fs, "900", fs >= 20), T.c, "center");
}

/** 奶油色圆底花色 + 队名（牌桌上）。 */
function teamLabel(ctx: Ctx, cx: number, cy: number, t: Side) {
  const nf = font(15, "700"), name = TEAM_NAME[t], tw = measure(ctx, name, nf);
  circle(ctx, cx - tw / 2 - 12, cy, 9, C.cream);
  icon[TEAM[t].suit](ctx, cx - tw / 2 - 12, cy + 0.5, 11, TEAM[t].c);
  text(ctx, name, cx - tw / 2 + 2, cy, nf, C.cream);
}

// ── 记分页 ──

interface Flip { from: ScoreState; p: number }
interface Strip { by: string; team: Side; title: string; line: string; until: number }

export class ScoreScene implements Scene {
  detail: GameDetail | null = null;
  score: StartedScore | null = null;
  private notStarted = false;
  error = "";
  private gate = new RevisionGate();
  /** 本场结束后点了「再来一场」：两队按 2 显示，等第一局记下 */
  private nextMatch = false;
  private flip: Flip | null = null;
  private strip: Strip | null = null;
  private scroll = new Scroller();
  private unreg: (() => void) | null = null;
  private barH = 76;
  busy = false;

  constructor(readonly app: App, readonly code: string, private nav: Nav) {
    this.scroll.onPullRefresh = () => void this.load();
  }

  onEnter() {
    void this.load();
    this.unreg = realtime.register({
      topics: [`game:${this.code}`],
      poll: true,
      onEvent: (ev) => this.onEvent(ev.type, ev.data),
      refresh: () => void this.load(), // 重连、降级轮询：只重画，不翻牌
    });
  }
  onLeave() { this.unreg?.(); }

  async load() {
    try {
      const [d, s] = await Promise.all([getGame(this.code), getScore(this.code)]);
      this.detail = d;
      this.setScore(s, false);
      this.error = "";
    } catch (err) {
      this.error = err instanceof ApiError && err.status === 404 ? "这一局不在了" : "记分没打开，下拉重试";
    } finally {
      this.app.redraw();
    }
  }

  /** 服务端给的整份计分。flip：这是新记的一局（自己记的或实时推来的），要翻牌。 */
  setScore(s: any, flip: boolean) {
    if (!s || !s.started) { this.notStarted = !!s; return; }
    if (!isStarted(s) || !this.gate.seen(s.revision)) return; // 慢到的旧响应不覆盖新的
    this.applyScore(s, flip);
  }

  private applyScore(s: StartedScore, flip: boolean) {
    const old = this.score;
    this.score = s;
    this.notStarted = false;
    if (!s.state.match_over) this.nextMatch = false;
    if (flip && old && s.rounds.length > old.rounds.length) this.startFlip(boardState(old.state, true));
    this.app.redraw();
  }

  private startFlip(from: ScoreState) {
    if (reduceMotion()) return;
    this.flip = { from, p: 0 };
    this.app.animate(FLIP_MS, (p) => { if (this.flip) this.flip.p = p; }, () => { this.flip = null; });
  }

  private onEvent(type: string, data: any) {
    if (type === "presence") return;
    if (!type.startsWith("score.")) { void this.load(); return; } // 有人退出、局被改：重拉，can_record 可能变了
    const ev = data as ScoreEventData;
    if (!this.gate.accept(ev?.revision)) return; // 计分事件没有顶层 v，按 data.revision 去重（自己提交的那次也在这里丢掉）
    const s = this.score, r = ev.round;
    if (type === "score.round" && s && r && r.round_no === s.rounds.length + 1) {
      const next = { ...s, revision: ev.revision, settings: ev.settings, state: ev.state, rounds: [...s.rounds, r] };
      this.applyScore(next, true);
      this.showStrip(ev, `记了第 ${r.round_no} 局`, resultLine(next.settings.kind, r));
      return;
    }
    if (type === "score.edited" && r && s) {
      this.showStrip(ev, `改了第 ${r.round_no} 局`, resultLine(s.settings.kind, r));
    }
    void this.load(); // 改一局后面各局都可能变了；开始记分、跳号：拉整份
  }

  /** S11：顶部深色条，谁记的、记了什么，3 秒收起。自己记的不提示。 */
  private showStrip(ev: ScoreEventData, what: string, line: string) {
    const me = currentUser();
    if (!ev.by || (me && ev.by.id === me.id)) return;
    const seat = this.detail?.seats.find((x) => x.user.id === ev.by.id);
    this.strip = { by: ev.by.nickname, team: seat ? teamOf(seat.seat_no) : "black", title: `${ev.by.nickname} ${what}`, line, until: Date.now() + 3000 };
    this.app.animate(3000, () => {}, () => { if (this.strip && Date.now() >= this.strip.until) { this.strip = null; this.app.redraw(); } });
  }

  // ── 谁是谁 ──

  /** 座号 → 称呼（现在坐在那个座位上的人） */
  names = (seat: number): string => this.detail?.seats.find((x) => x.seat_no === seat)?.user.nickname ?? `${seat} 号`;
  isMeSeat = (seat: number): boolean => {
    const me = currentUser();
    return !!me && this.detail?.seats.find((x) => x.seat_no === seat)?.user.id === me.id;
  };
  get seated(): boolean { return this.detail?.me.role === "seat"; }
  get canRecord(): boolean { return !!this.score?.can_record; }
  members(t: Side, withMe: boolean): string {
    const seats = t === "red" ? [1, 3] : [2, 4];
    return seats.map((n) => this.names(n) + (withMe && this.isMeSeat(n) ? "（我）" : "")).join(" · ");
  }
  get ended(): boolean { return !!this.score?.state.match_over && !this.nextMatch; }
  /** 下一局（记一局要看的）状态 */
  get board(): ScoreState { return boardState(this.score!.state, true); }

  // ── 画 ──

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - this.barH - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step() || (!!this.strip && Date.now() < this.strip.until); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, s = this.score;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    if (!s) {
      nav(a, ctx, "牌桌记分", () => this.nav.back());
      if (this.error) errorLine(a, ctx, a.contentTop + 40, this.error);
      else text(ctx, this.notStarted ? "这一局还没开始记分" : "正在打开…", W / 2, a.contentTop + 60, font(15), C.ink3, "center");
      return;
    }
    const ended = this.ended;
    const top = a.contentTop, bottom = a.H - this.barH - a.safeBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 18 - this.scroll.pos;
    if (ended) y = this.drawEnded(ctx, s, y);
    else {
      y = s.settings.kind === "no-level" ? this.drawNoLevel(ctx, s, y) : this.drawBoard(ctx, s, y);
      if (s.settings.kind !== "no-level") y = this.drawLadder(ctx, y);
      y = this.drawLast(ctx, s, y);
      if (s.settings.kind === "no-level") y = this.drawDoubles(ctx, y);
    }
    if (this.error) y += errorLine(a, ctx, y, this.error);
    y += 8;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, ended ? "本场结束" : `牌桌记分 · ${kindLabel(s.settings.kind)}`, () => this.nav.back());
    this.drawBar(ctx, s, ended);
    this.drawStrip(ctx);
  }

  /** 两队当前要显示的级数：翻牌前半程还是旧的。 */
  private shown(t: Side): { lv: number; scaleX: number } {
    const now = this.board[t].level, f = this.flip;
    if (!f || f.from[t].level === now) return { lv: now, scaleX: 1 };
    if (f.p < 0.5) return { lv: f.from[t].level, scaleX: 1 - ease.out(f.p / 0.5) };
    return { lv: now, scaleX: ease.out((f.p - 0.5) / 0.5) };
  }

  /** S3：两张级牌立在牌桌上 */
  private drawBoard(ctx: Ctx, s: StartedScore, y: number): number {
    const a = this.app, x = PAD, w = a.W - PAD * 2, h = 284, st = this.board;
    felt(ctx, x, y, w, h);
    text(ctx, boardTitle(s.rounds.length + 1, st, s.settings.kind), a.W / 2, y + 32, font(13, "700"), CREAM(0.85), "center");
    (["red", "black"] as const).forEach((t) => {
      const cx = x + w * (t === "red" ? 0.27 : 0.73), sh = this.shown(t);
      levelCard(ctx, cx, y + 64, levelName(sh.lv), t, { inPlay: st.playing === t, scaleX: sh.scaleX });
      const ny = y + 64 + 140 + 24;
      teamLabel(ctx, cx, ny, t);
      text(ctx, ellipsize(ctx, this.members(t, this.seated), w * 0.46, font(12)), cx, ny + 22, font(12), CREAM(0.75), "center");
      const tip = aTip(st[t]);
      if (tip) text(ctx, tip, cx, ny + 42, font(12, "700"), C.gold, "center");
    });
    return y + h + 16;
  }

  /** S13：不升级——两张牌换成胜局数 */
  private drawNoLevel(ctx: Ctx, s: StartedScore, y: number): number {
    const a = this.app, x = PAD, w = a.W - PAD * 2, h = 236, st = this.board;
    felt(ctx, x, y, w, h);
    text(ctx, boardTitle(s.rounds.length + 1, st, "no-level"), a.W / 2, y + 32, font(13, "700"), CREAM(0.85), "center");
    (["red", "black"] as const).forEach((t) => {
      const cx = x + w * (t === "red" ? 0.27 : 0.73);
      text(ctx, String(st[t].wins), cx, y + 112, font(76, "900", true), C.cream, "center");
      teamLabel(ctx, cx, y + 172, t);
      text(ctx, ellipsize(ctx, this.members(t, this.seated), w * 0.46, font(12)), cx, y + 194, font(12), CREAM(0.75), "center");
    });
    text(ctx, ":", a.W / 2, y + 108, font(40, "900", true), CREAM(0.6), "center");
    return y + h + 16;
  }

  private drawDoubles(ctx: Ctx, y: number): number {
    const x = PAD, w = this.app.W - PAD * 2, st = this.board;
    card(ctx, x, y, w, 56, 18);
    text(ctx, "双上", x + 16, y + 28, font(13), C.ink3);
    icon.heart(ctx, x + 70, y + 28.5, 12, C.red); text(ctx, `${st.red.double_ups} 次`, x + 82, y + 28, font(14, "700"), C.ink);
    icon.spade(ctx, x + 150, y + 28.5, 12, C.ink); text(ctx, `${st.black.double_ups} 次`, x + 162, y + 28, font(14, "700"), C.ink);
    return y + 56 + 12;
  }

  /** 升级路线：2 到 A 共 13 格，两队各一条；翻牌时圆点滑到新位置 */
  private drawLadder(ctx: Ctx, y: number): number {
    const x = PAD, w = this.app.W - PAD * 2, st = this.board, f = this.flip;
    card(ctx, x, y, w, 108, 18);
    const x0 = x + 62, x1 = x + w - 20, step = (x1 - x0) / 12;
    for (let i = 0; i < 13; i++) {
      const l = levelName(i + 2);
      text(ctx, l, x0 + i * step, y + 22, font(11, l === "A" ? "700" : "normal"), l === "A" ? C.ink2 : C.ink4, "center");
    }
    (["red", "black"] as const).forEach((t, k) => {
      const ry = y + 54 + k * 30, T = TEAM[t], lv = st[t].level;
      let idx = lv - 2;
      if (f && f.from[t].level !== lv) idx = f.from[t].level - 2 + (lv - f.from[t].level) * ease.out(f.p);
      icon[T.suit](ctx, x + 22, ry + 0.5, 13, T.c);
      text(ctx, TEAM_NAME[t], x + 32, ry, font(12, "700"), C.ink2);
      line(ctx, x0, ry, x1, ry, C.line, 4);
      if (idx > 0) line(ctx, x0, ry, x0 + idx * step, ry, T.c, 4);
      for (let i = Math.ceil(idx + 0.01); i < 13; i++) circle(ctx, x0 + i * step, ry, 2, C.line2);
      const dx = x0 + idx * step, shownLv = this.shown(t).lv;
      if (st.playing === t) circle(ctx, dx, ry, 12, null, C.gold, 2.5);
      circle(ctx, dx, ry, 9.5, T.c);
      const s = levelName(shownLv);
      text(ctx, s, dx, ry + 0.5, font(s.length > 1 ? 9 : 11, "900"), C.cream, "center");
    });
    return y + 108 + 12;
  }

  /** 上一局一行，点了看每局记录 */
  private drawLast(ctx: Ctx, s: StartedScore, y: number): number {
    const r = s.rounds[s.rounds.length - 1];
    if (!r) return y;
    const a = this.app, x = PAD, w = a.W - PAD * 2;
    const pressed = a.isPressed("last");
    card(ctx, x, y, w, 68, 18);
    if (pressed) fillRR(ctx, x + 1, y + 1, w - 2, 66, 17, C.paper);
    const title = `上一局 · 第 ${r.round_no} 局${s.settings.kind === "no-level" ? "" : ` · ${playingTag(r).replace(/^打[红黑] /, "打 ")}`}`;
    text(ctx, title, x + 16, y + 22, font(12), C.ink3);
    const main = `${TEAM_NAME[r.winner]} ${howText(r.partner_place)}${r.passed_a ? " · 过 A" : ""}`;
    const right = s.settings.kind === "no-level" ? "" : `${levelName(r.level_before)} → ${levelName(r.level_after)}`;
    const rw = right ? measure(ctx, right, font(20, "900", true)) + 12 : 0;
    text(ctx, ellipsize(ctx, main, w - 56 - rw, font(15, "700")), x + 16, y + 46, font(15, "700"), C.ink);
    if (right) text(ctx, right, x + w - 34, y + 35, font(20, "900", true), TEAM[r.winner].c, "right");
    chevron(ctx, x + w - 18, y + 35, C.ink4);
    a.hit("last", x, y, w, 68, () => this.openRecords());
    return y + 68 + 12;
  }

  /** S8：本场结束 */
  private drawEnded(ctx: Ctx, s: StartedScore, y: number): number {
    const a = this.app, x = PAD, w = a.W - PAD * 2, st = s.state, win = (st.winner || "red") as Side, lose = other(win);
    felt(ctx, x, y, w, 330);
    text(ctx, `第 ${st.match} 场 · ${st.match_rounds} 局`, a.W / 2, y + 32, font(13, "700"), CREAM(0.85), "center");
    // 赢方的 A 牌：奶油底加金边（README 7.5）
    levelCard(ctx, a.W / 2, y + 64, "A", win, { gold: true });
    text(ctx, `${TEAM_NAME[win]} 过 A`, a.W / 2, y + 246, font(34, "900", true), C.cream, "center");
    text(ctx, ellipsize(ctx, this.members(win, this.seated), w - 40, font(14)), a.W / 2, y + 284, font(14), CREAM(0.8), "center");
    y += 330 + 16;
    card(ctx, x, y, w, 132, 18);
    const dur = durationShort(matchDuration(s.started_at, s.rounds, st.match));
    const cells: [string, string][] = [[String(st.match_rounds), "局"], [dur, "用时"], [`${st.red.double_ups} : ${st.black.double_ups}`, "双上 红 : 黑"]];
    const cw = w / 3;
    cells.forEach(([v, l], i) => {
      text(ctx, v, x + cw * i + cw / 2, y + 40, font(26, "900", true), C.ink, "center");
      text(ctx, l, x + cw * i + cw / 2, y + 70, font(12), C.ink3, "center");
      if (i) { ctx.fillStyle = C.line2; ctx.fillRect(x + cw * i, y + 20, 1, 64); }
    });
    ctx.fillStyle = C.line2; ctx.fillRect(x + 16, y + 92, w - 32, 1);
    icon[TEAM[lose].suit](ctx, x + 26, y + 112.5, 12, TEAM[lose].c);
    text(ctx, `${TEAM_NAME[lose]} 停在 ${levelName(st[lose].level)}`, x + 38, y + 112, font(13), C.ink2);
    text(ctx, "每局记录", x + w - 34, y + 112, font(13, "600"), a.isPressed("ended-records") ? C.redDeep : C.red, "right");
    chevron(ctx, x + w - 20, y + 112, C.red);
    a.hit("ended-records", x + w / 2, y + 93, w / 2, 39, () => this.openRecords());
    y += 132 + 16;
    button(a, ctx, "end-scoring", x, y, w, 48, "结束记分", "ghost", () => this.nav.back(), { size: 15 });
    return y + 48 + 16;
  }

  private drawBar(ctx: Ctx, s: StartedScore, ended: boolean) {
    const a = this.app, W = a.W, by = a.H - this.barH - a.safeBottom;
    ctx.fillStyle = C.card;
    ctx.fillRect(0, by, W, this.barH + a.safeBottom);
    ctx.fillStyle = C.line;
    ctx.fillRect(0, by, W, 1);
    if (ended) {
      if (this.canRecord) {
        button(a, ctx, "bar:report", PAD, by + 12, 130, 52, "分享战报", "dark", () => this.shareReport(), { icon: icon.share, size: 15 });
        button(a, ctx, "bar:again", PAD + 142, by + 12, W - PAD * 2 - 142, 52, "再来一场 · 从 2 打起", "primary",
          () => { this.nextMatch = true; this.scroll.scrollTo(0); a.redraw(); }, { size: 15 });
      } else button(a, ctx, "bar:report", PAD, by + 12, W - PAD * 2, 52, "分享战报", "dark", () => this.shareReport(), { icon: icon.share, size: 15 });
      return;
    }
    if (this.canRecord) {
      button(a, ctx, "bar:records", PAD, by + 12, 110, 52, "每局记录", "ghost", () => this.openRecords(), { size: 15 });
      button(a, ctx, "bar:record", PAD + 122, by + 12, W - PAD * 2 - 122, 52, `记一局 · 第 ${s.rounds.length + 1} 局`, "primary", () => this.openRecord());
      return;
    }
    // S14：旁观（候补、别的牌友）只有分享和一行说明；坐着但有人退出了，等坐满
    button(a, ctx, "bar:share", PAD, by + 12, 110, 52, "分享", "dark", () => { if (this.detail) shareSheet(a, this.detail); }, { icon: icon.share });
    const why = this.seated ? "有人退出了，坐满 4 人才能接着记" : "只有座位上的四个人能记分";
    text(ctx, ellipsize(ctx, why, W - PAD * 2 - 128, font(13)), PAD + 128, by + 38, font(13), C.ink3);
  }

  private drawStrip(ctx: Ctx) {
    const st = this.strip;
    if (!st || Date.now() >= st.until) return;
    const a = this.app, ty = a.contentTop + 6, w = a.W - PAD * 2;
    fillRR(ctx, PAD, ty, w, 56, 14, C.ink);
    circle(ctx, PAD + 24, ty + 28, 13, TEAM[st.team].tint);
    text(ctx, initial(st.by), PAD + 24, ty + 29, font(12, "900"), TEAM[st.team].c, "center");
    text(ctx, ellipsize(ctx, st.title, w - 62, font(12)), PAD + 46, ty + 18, font(12), CREAM(0.7));
    text(ctx, ellipsize(ctx, st.line, w - 62, font(14, "700")), PAD + 46, ty + 38, font(14, "700"), C.cream);
  }

  // ── 操作 ──

  openRecords() {
    if (!this.score) return;
    this.app.push(new RecordsScene(this.app, this));
  }

  openRecord() { if (this.score && this.canRecord) recordSheet(this, null); }
  openEdit(r: ScoreRound) { if (this.score && this.canRecord) recordSheet(this, r); }

  /** 写接口的通用错误 */
  explain(err: unknown): string {
    if (err instanceof ApiError) {
      if (err.code === "game_closed") return "这一局已经结束，记分只能看了";
      if (err.code === "not_seated") return "只有座位上的四个人能记分";
      if (err.code === "not_full") return "有人退出了，坐满 4 人才能接着记";
      if (err.code === "rate_limited") return "记得太快了，歇一下再记";
      if (err.code === "network") return err.message;
      if (err.status === 400) return "记分有更新，看一下再记";
      if (err.status === 503) return "人有点多，稍等一下再试";
      return err.message;
    }
    return "没记上，再试一次";
  }

  /** S15：战报卡片 5:4，画在主画布左上角裁出来（离屏画布在真机上导不出图，见 PR #144） */
  shareReport() {
    const s = this.score, d = this.detail;
    if (!s || !d || !s.state.match_over) return;
    const st = s.state, win = (st.winner || "red") as Side;
    wx.shareAppMessage({
      title: `掼蛋战报：${TEAM_NAME[win]}过 A · ${st.match_rounds} 局`,
      imageUrl: renderReportCard(this.app, (ctx) => drawReportCard(ctx, this)),
      query: `g=${encodeURIComponent(this.code)}`,
    });
  }
}

function line(ctx: Ctx, x1: number, y1: number, x2: number, y2: number, c: string, lw: number) {
  ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2);
  ctx.strokeStyle = c; ctx.lineWidth = lw; ctx.lineCap = "round"; ctx.stroke(); ctx.lineCap = "butt";
}

// ── 记一局 / 改一局（S4–S7、S10）──

function recordSheet(sc: ScoreScene, editing: ScoreRound | null) {
  const app = sc.app, s0 = sc.score!;
  const settings = s0.settings, noLevel = settings.kind === "no-level";
  const no = editing ? editing.round_no : s0.rounds.length + 1;
  // 这一局开打之前的状态：记新的一局看现在的（本场结束了就是下一场），改一局按前面各局重算
  const before = (): ScoreState => {
    const s = sc.score!;
    return editing ? compute(settings, s.rounds.slice(0, no - 1).map(factOf)).state : s.state;
  };
  const st = { head: editing?.head_seat ?? 0, partner: editing?.partner_place ?? 0, busy: false, err: "", key: "", keyFor: "" };
  const fact = (): RoundFact | null => (st.head && (st.partner || noLevel) ? { head_seat: st.head, partner_place: st.partner || 3 } : null);

  const submit = async () => {
    const f = fact();
    if (!f || st.busy) return;
    st.busy = true; st.err = ""; app.redraw();
    try {
      if (editing) await submitEdit(sc, no, f, factOf(editing), sheet);
      else {
        // 同一次提交（同一个结果）重试时复用幂等键；换了选择就换一个
        const k = `${f.head_seat}/${f.partner_place}`;
        if (st.keyFor !== k) { st.key = newIdempotencyKey(); st.keyFor = k; }
        await submitRecord(sc, no, f, st.key, sheet);
      }
    } catch (err) {
      st.err = sc.explain(err);
    } finally {
      st.busy = false; app.redraw();
    }
  };

  const sheet: Sheet = {
    draw(ctx, y, w) {
      const s = sc.score!, pre = before(), playA = !noLevel && pre.playing !== "" && pre[pre.playing].level === A;
      const sub = editing
        ? `原来：${TEAM_NAME[editing.winner]} ${howText(editing.partner_place)}${noLevel ? "" : ` · ${levelName(editing.level_before)} → ${levelName(editing.level_after)}`}`
        : noLevel ? "不升级 · 点头游就行" : pre.match_over ? "新一场 · 打 2"
        : pre.playing ? `本局打${TEAM_NAME[pre.playing]}的 ${levelName(pre.playing_level)}${playA ? ` · 第 ${pre[pre.playing].a_fails + 1} 次` : ""}` : "本局打 2";
      let cy = y + 30;
      text(ctx, editing ? `改第 ${no} 局` : `记第 ${no} 局`, 20, cy + 6, font(20, "900", true), C.ink);
      cy += 34;
      text(ctx, ellipsize(ctx, sub, w - 40, font(13)), 20, cy, font(13), C.ink3);
      cy += 26;

      // ① 谁是头游：按队排两行，同一行就是搭档
      text(ctx, "① 谁是头游？", 20, cy + 14, font(14, "700"), C.ink);
      cy += 32;
      const tw = (w - 50) / 2, th = 64;
      [[1, 3], [2, 4]].forEach((row, ri) => row.forEach((seat, ci) => {
        const tx = 20 + ci * (tw + 10), ty = cy + ri * (th + 10), on = st.head === seat, t = teamOf(seat), id = `rs-head:${seat}`;
        fillRR(ctx, tx, ty, tw, th, 14, on ? C.cream : app.isPressed(id) ? C.paper : C.card);
        strokeRR(ctx, tx + (on ? 1 : 0.5), ty + (on ? 1 : 0.5), tw - (on ? 2 : 1), th - (on ? 2 : 1), 14, on ? C.red : C.line, on ? 2 : 1);
        teamAvatar(ctx, tx + 34, ty + th / 2, 20, sc.names(seat), t, sc.isMeSeat(seat));
        // 「我」只靠头像金圈，不写「（我）」，给「头游」签让位
        text(ctx, ellipsize(ctx, sc.names(seat), tw - 64 - (on ? 52 : 10), font(15, "700")), tx + 64, ty + 24, font(15, "700"), C.ink);
        icon[TEAM[t].suit](ctx, tx + 70, ty + 44, 11, TEAM[t].c);
        text(ctx, TEAM_NAME[t], tx + 80, ty + 44, font(12), C.ink3);
        if (on) { fillRR(ctx, tx + tw - 50, ty + 10, 40, 20, 10, C.red); text(ctx, "头游", tx + tw - 30, ty + 20, font(11, "900"), C.cream, "center"); }
        app.hit(id, tx, ty, tw, th, () => { st.head = seat; st.err = ""; });
      }));
      cy += th * 2 + 10 + 22;

      // ② 头游的搭档第几：打本队的 A 时副标题直接写「过 A / 没过」
      const mate = st.head ? mateOf(st.head) : 0;
      text(ctx, mate ? `② ${ellipsize(ctx, sc.names(mate), 120, font(14, "700"))}是第几个出完的？` : "② 头游的搭档第几？", 20, cy, font(14, "700"), mate ? C.ink : C.ink4);
      if (!mate) text(ctx, "先选头游", w - 20, cy, font(12), C.ink4, "right");
      else if (noLevel) text(ctx, "可以不选", w - 20, cy, font(12), C.ink4, "right");
      cy += 18;
      const atA = playA && (!st.head || teamOf(st.head) === pre.playing);
      const opts: [number, string][] = noLevel ? [[2, "双上"], [3, ""], [4, ""]]
        : atA ? [[2, "过 A"], [3, "过 A"], [4, "没过"]] : [[2, "双上 · 升 3 级"], [3, "升 2 级"], [4, "升 1 级"]];
      const ow = (w - 56) / 3, oh = 72;
      opts.forEach(([p, subl], i) => {
        const ox = 20 + i * (ow + 8), on = st.partner === p, dis = !mate, id = `rs-place:${p}`;
        fillRR(ctx, ox, cy, ow, oh, 14, on ? C.red : app.isPressed(id) && !dis ? C.paper : C.card);
        if (!on) strokeRR(ctx, ox + 0.5, cy + 0.5, ow - 1, oh - 1, 14, C.line, 1, dis ? [3, 3] : undefined);
        text(ctx, placeName(p), ox + ow / 2, cy + (subl ? 28 : 36), font(18, "700"), on ? C.cream : dis ? C.khaki : C.ink, "center");
        if (subl) text(ctx, subl, ox + ow / 2, cy + 52, font(12, on ? "700" : "normal"), on ? CREAM(0.9) : dis ? C.khaki : C.ink3, "center");
        // 不升级时搭档名次只影响「双上」统计：再点一下取消
        app.hit(id, ox, cy, ow, oh, dis ? null : () => { st.partner = noLevel && st.partner === p ? 0 : p; st.err = ""; });
      });
      cy += oh + 14;

      // 预览：固定高 60，没选完画虚线占位，选完不跳动
      const f = fact(), pw = w - 40, ph = 60;
      let pv: Preview | null = null;
      if (f) {
        pv = previewText(settings, pre, f, sc.names);
        if (editing && pv.kind === "normal" && editing.winner === pv.team) pv = { ...pv, sub: `原来 → ${levelName(editing.level_after)}` };
      }
      drawPreview(ctx, 20, cy, pw, ph, pv);
      cy += ph + 14;

      // S10：改一局的连带变化（本地按规则重算的预览，提交后以服务端为准）
      const unchanged = !!editing && !!f && sameFact(f, editing);
      if (editing && f && !unchanged) {
        const p = previewEdit(settings, s.rounds, no, f);
        const lines = [
          p.later ? `后面 ${p.later} 局跟着重新算` : "这是最后一局，后面没有要重算的",
          noLevel ? `现在 红方 ${p.state.red.wins} : ${p.state.black.wins} 黑方` : impactLine(s.state, p.state),
        ];
        fillRR(ctx, 20, cy, pw, 54, 14, C.sand);
        lines.forEach((ln, i) => text(ctx, ellipsize(ctx, ln, pw - 32, font(12, i === 0 ? "700" : "normal")), 36, cy + 17 + i * 20, font(12, i === 0 ? "700" : "normal"), C.ink2));
        cy += 54 + 14;
      }
      const label = st.busy ? "正在记…" : editing ? (unchanged ? "没有改动" : "改成这样") : pv?.kind === "pass" ? "记下 · 本场结束" : `记下第 ${no} 局`;
      button(app, ctx, "rs-ok", 20, cy, pw, 52, label, "primary", () => void submit(), { disabled: !f || st.busy || unchanged });
      cy += 52;
      if (st.err) { cy += 8; text(ctx, ellipsize(ctx, st.err, pw, font(13)), w / 2, cy + 10, font(13), C.red, "center"); cy += 20; }
      return cy + 20 - y;
    },
  };
  app.openSheet(sheet);
}

/** 「现在 红方 J → 10 · 黑方 7 不变 · 下一局打 10」 */
function impactLine(now: ScoreState, after: ScoreState): string {
  const team = (t: Side) => now[t].level === after[t].level
    ? `${TEAM_NAME[t]} ${levelName(now[t].level)} 不变`
    : `${TEAM_NAME[t]} ${levelName(now[t].level)} → ${levelName(after[t].level)}`;
  const tail = after.match_over ? `${TEAM_NAME[(after.winner || "red") as Side]}过 A，本场结束` : `下一局打 ${levelName(after.playing_level)}`;
  return `现在 ${team("red")} · ${team("black")} · ${tail}`;
}

function drawPreview(ctx: Ctx, x: number, y: number, w: number, h: number, p: Preview | null) {
  if (!p) {
    strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 14, C.line2, 1, [4, 4]);
    text(ctx, "选完两步，这里显示升几级", x + w / 2, y + h / 2, font(13), C.ink4, "center");
    return;
  }
  if (p.kind === "pass") {
    fillRR(ctx, x, y, w, h, 14, C.gold);
    text(ctx, ellipsize(ctx, p.main, w - 32, font(17, "900")), x + 16, y + 22, font(17, "900"), C.redInk);
    text(ctx, ellipsize(ctx, p.sub, w - 32, font(12)), x + 16, y + 44, font(12), C.redInk);
  } else if (p.kind === "fail") {
    fillRR(ctx, x, y, w, h, 14, C.errTint);
    strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 14, "#e3b5ae", 1);
    text(ctx, ellipsize(ctx, p.main, w - 32, font(16, "700")), x + 16, y + 22, font(16, "700"), C.red);
    text(ctx, ellipsize(ctx, p.sub, w - 32, font(12)), x + 16, y + 44, font(12), C.ink2);
  } else {
    const T = TEAM[p.team];
    fillRR(ctx, x, y, w, h, 14, C.paper);
    icon[T.suit](ctx, x + 20, y + h / 2, 16, T.c);
    const subW = measure(ctx, p.sub, font(13));
    text(ctx, ellipsize(ctx, p.main, w - 52 - subW - 12, font(20, "900", true)), x + 36, y + h / 2, font(20, "900", true), T.c);
    text(ctx, p.sub, x + w - 16, y + h / 2, font(13), C.ink3, "right");
  }
}

async function submitRecord(sc: ScoreScene, no: number, mine: RoundFact, key: string, sheet: Sheet) {
  const app = sc.app;
  try {
    sc.setScore(await recordRound(sc.code, no, mine, key), true);
    app.closeSheet(sheet);
    wx.vibrateShort?.({ type: "light" });
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 409) throw err;
    if (err.code !== "round_taken") {
      app.closeSheet(sheet);
      app.toast(sc.explain(err));
      void sc.load();
      return;
    }
    // S12：这一局已经有人记了。带回来的是记下的那一局和最新的整份计分
    const body = err.body ?? {};
    const existing = body.round as ScoreRound | undefined;
    sc.setScore(body, true);
    app.closeSheet(sheet);
    if (!existing) return;
    const d = decideConflict(existing, mine, currentUser()?.id);
    if (d === "mine") return; // 自己重复提交：就是成功了
    if (d === "same") { app.toast(`和 ${existing.recorded_by.nickname} 记的一样`); return; }
    conflictDialog(sc, existing, mine);
  }
}

async function submitEdit(sc: ScoreScene, no: number, mine: RoundFact, was: RoundFact, sheet: Sheet | null) {
  const app = sc.app;
  try {
    const s = await editRound(sc.code, no, mine, was);
    sc.setScore(s, false);
    if (sheet) app.closeSheet(sheet);
    const st = sc.score?.state;
    if (st) app.toast(sc.score!.settings.kind === "no-level"
      ? `改好了 · 现在 红方 ${st.red.wins} : ${st.black.wins} 黑方`
      : `改好了 · 现在 红方 ${levelName(st.red.level)} · 黑方 ${levelName(st.black.level)}`, 2600);
  } catch (err) {
    if (err instanceof ApiError && err.status === 409 && err.code === "round_changed") {
      sc.setScore(err.body, false);
      if (sheet) app.closeSheet(sheet);
      app.toast("这一局刚被别人改过，看一下再改", 2600);
      return;
    }
    if (!sheet) { app.toast(sc.explain(err)); return; }
    throw err;
  }
}

/** S12：两边记的不一样，并排给出，让点的人选 */
function conflictDialog(sc: ScoreScene, existing: ScoreRound, mine: RoundFact) {
  const app = sc.app, by = existing.recorded_by.nickname, no = existing.round_no;
  const s0 = sc.score!, kind = s0.settings.kind;
  const myRound = previewEdit(s0.settings, s0.rounds, no, mine).round;
  const range = (r: { winner: Side; level_before: number; level_after: number; double_up: boolean }) =>
    kind === "no-level" ? (r.double_up ? "双上" : `${TEAM_NAME[r.winner]}赢`) : `${levelName(r.level_before)} → ${levelName(r.level_after)}`;
  let busy = false;
  const dlg: Sheet = {
    dialog: true,
    draw(ctx, y, w) {
      let cy = y + 36;
      text(ctx, ellipsize(ctx, `第 ${no} 局 ${by} 已经记了`, w - 32, font(18, "700")), w / 2, cy, font(18, "700"), C.ink, "center");
      cy += 26;
      text(ctx, "两边记的不一样，用哪个？", w / 2, cy, font(13), C.ink3, "center");
      cy += 22;
      const rows: [string, RoundFact, { winner: Side; level_before: number; level_after: number; double_up: boolean }][] = [
        [`${by} 记的`, existing, existing], ["你记的", mine, myRound],
      ];
      rows.forEach(([label, f, r]) => {
        fillRR(ctx, 16, cy, w - 32, 48, 12, C.paper);
        text(ctx, label, 30, cy + 16, font(12, "700"), C.ink2);
        const rt = range(r), rw = measure(ctx, rt, font(17, "900"));
        text(ctx, ellipsize(ctx, whoLine(f, sc.names), w - 76 - rw, font(13)), 30, cy + 34, font(13), C.ink);
        text(ctx, rt, w - 30, cy + 25, font(17, "900"), TEAM[r.winner].c, "right");
        cy += 56;
      });
      cy += 8;
      const bw = (w - 32 - 10) / 2;
      button(app, ctx, "cf-theirs", 16, cy, bw, 48, `用 ${ellipsize(ctx, by, bw - 60, font(15, "700"))} 的`, "soft", () => {
        app.closeSheet(dlg);
        app.toast(`用了 ${by} 记的`);
      }, { size: 15 });
      button(app, ctx, "cf-mine", 26 + bw, cy, bw, 48, busy ? "正在改…" : "改成我的", "primary", async () => {
        if (busy) return;
        busy = true; app.redraw();
        try { await submitEdit(sc, no, mine, factOf(existing), null); } finally { busy = false; app.closeSheet(dlg); }
      }, { size: 15, disabled: busy });
      return cy + 48 + 16 - y;
    },
  };
  app.openSheet(dlg);
}

// ── 每局记录（S9），点一局改（S10）──

export class RecordsScene implements Scene {
  private scroll = new Scroller();
  constructor(private app: App, private sc: ScoreScene) {}

  scrollerAt(_x: number, y: number) { return y > this.app.contentTop ? { s: this.scroll, axis: "y" as const } : null; }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, s = this.sc.score;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    const top = a.contentTop, bottom = a.H - a.safeBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 12 - this.scroll.pos;
    if (s) {
      const x = PAD, w = W - PAD * 2, kind = s.settings.kind, editable = this.sc.canRecord;
      // 按场分组，新的在上
      const matches = [...new Set(s.rounds.map((r) => r.match))].sort((p, q) => q - p);
      if (!matches.length) text(ctx, "还没有记过一局", W / 2, y + 40, font(15), C.ink3, "center");
      matches.forEach((m, mi) => {
        const rs = s.rounds.filter((r) => r.match === m).reverse();
        text(ctx, `共 ${rs.length} 局 · 第 ${m} 场`, x, y + 12, font(13), C.ink3);
        if (mi === 0 && kind !== "no-level") {
          const st = s.state;
          icon.heart(ctx, x + w - 112, y + 12.5, 12, C.red); text(ctx, levelName(st.red.level), x + w - 100, y + 12, font(14, "900"), C.red);
          icon.spade(ctx, x + w - 60, y + 12.5, 12, C.ink); text(ctx, levelName(st.black.level), x + w - 48, y + 12, font(14, "900"), C.ink);
        }
        y += 32;
        card(ctx, x, y, w, 84 * rs.length, 18);
        rs.forEach((r, i) => {
          const ry = y + i * 84, T = TEAM[r.winner], id = `round:${r.round_no}`;
          if (editable && a.isPressed(id)) fillRR(ctx, x + 1, ry + 1, w - 2, 82, 17, C.paper);
          circle(ctx, x + 32, ry + 42, 16, C.paper);
          text(ctx, String(r.round_no), x + 32, ry + 43, font(r.round_no > 99 ? 12 : 15, "900"), C.ink2, "center");
          let tx = x + 60;
          if (kind !== "no-level") {
            const tag = playingTag(r), tf = font(11, "700"), tgw = measure(ctx, tag, tf) + 12;
            fillRR(ctx, tx, ry + 13, tgw, 20, 6, C.beige); text(ctx, tag, tx + 6, ry + 23, tf, C.ink2);
            tx += tgw + 8;
          }
          icon[T.suit](ctx, tx + 6, ry + 23.5, 11, T.c);
          const right = kind === "no-level" ? "" : `${levelName(r.level_before)}→${levelName(r.level_after)}`;
          const rw = right ? measure(ctx, right, font(17, "900")) + 40 : 30;
          const how = `${TEAM_NAME[r.winner]} ${howText(r.partner_place)}${r.passed_a ? " · 过 A" : r.reset_to_2 ? ` · ${TEAM_NAME[r.failed_a as Side]}回到 2` : ""}`;
          text(ctx, ellipsize(ctx, how, x + w - rw - tx - 16, font(14, "700")), tx + 16, ry + 23, font(14, "700"), C.ink);
          text(ctx, ellipsize(ctx, whoLine(r, this.sc.names), w - 60 - rw, font(12)), x + 60, ry + 46, font(12), C.ink2);
          const rec = `${r.recorded_by.nickname} ${timeHM(r.recorded_at)} 记`;
          text(ctx, ellipsize(ctx, rec, w - 60 - rw - 40, font(11)), x + 60, ry + 66, font(11), C.ink4);
          if (r.edited_by) {
            const ex = x + 60 + Math.min(measure(ctx, rec, font(11)), w - 60 - rw - 40) + 8;
            fillRR(ctx, ex, ry + 58, 32, 16, 5, C.sand); text(ctx, "已改", ex + 16, ry + 66, font(10, "700"), C.ink2, "center");
          }
          if (right) text(ctx, right, x + w - (editable ? 30 : 16), ry + 42, font(17, "900"), T.c, "right");
          if (editable) {
            chevron(ctx, x + w - 16, ry + 42, C.ink4);
            a.hit(id, x, ry, w, 84, () => this.sc.openEdit(r));
          }
          if (i < rs.length - 1) { ctx.fillStyle = C.line2; ctx.fillRect(x + 60, ry + 84, w - 76, 1); }
        });
        y += 84 * rs.length + 20;
      });
      if (editable && s.rounds.length) { text(ctx, "点一局可以改，改了后面的局会跟着重新算", W / 2, y + 4, font(12), C.ink4, "center"); y += 28; }
    }
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, "每局记录", () => a.pop());
  }
}

// ── 战报卡片（S15）──

/**
 * 卡片画在主画布左上角，按 5:4 裁出 750×600，同一个 tick 里把页面画回去（和 PR #144 的转发卡片一样：
 * 小游戏的离屏画布在真机上导不出图片，imageUrl 变空，微信就去截当前屏幕）。
 */
export function renderReportCard(app: App, paint: (ctx: Ctx) => void): string | undefined {
  const W = 500, H = 400, canvas = app.canvas, ctx: Ctx = app.ctx;
  const px = Math.min(canvas.width, 1000), k = px / W;
  try {
    ctx.save();
    ctx.setTransform(k, 0, 0, k, 0, 0);
    paint(ctx);
    ctx.restore();
    return canvas.toTempFilePathSync({ x: 0, y: 0, width: px, height: Math.round((px * H) / W), destWidth: 750, destHeight: 600, fileType: "png" });
  } catch {
    return undefined;
  } finally {
    app.repaint();
  }
}

/** 版式同 C0：满版、内描边、文字区左右 48、座位圆心 H − 70。只写谁过了 A、几局、多久、双上几次。 */
export function drawReportCard(ctx: Ctx, sc: ScoreScene, W = 500, H = 400) {
  const s = sc.score!, d = sc.detail!, st = s.state, win = (st.winner || "red") as Side, lose = other(win);
  const g = d.game, day = cardDay(g.starts_at);
  ctx.fillStyle = C.redDeep; ctx.fillRect(0, 0, W, H);
  strokeRR(ctx, 8, 8, W - 16, H - 16, 22, C.redInk, 8);
  strokeRR(ctx, 24, 24, W - 48, H - 48, 14, "rgba(233,196,106,0.45)", 2, [6, 5]);
  const date = `${day === "今天" || day === "明天" ? day + " · " : ""}${weekday(g.starts_at)} ${slashDate(g.starts_at)} · ${g.area}`;
  text(ctx, ellipsize(ctx, date, W - 96, font(22, "700")), 48, 66, font(22, "700"), C.gold);
  text(ctx, `${TEAM_NAME[win]} 过 A`, 48, 140, font(64, "900", true), C.cream);
  const vs = `${sc.members(win, false)}  胜  ${sc.members(lose, false)}`;
  text(ctx, ellipsize(ctx, vs, W - 96, font(20, "700")), 48, 202, font(20, "700"), C.cream);
  const stats = `${st.match_rounds} 局 · ${durationLong(matchDuration(s.started_at, s.rounds, st.match))} · ${TEAM_NAME[win]}双上 ${st[win].double_ups} 次`;
  text(ctx, ellipsize(ctx, stats, W - 96, font(17)), 48, 238, font(17), CREAM(0.75));
  const cy = H - 70;
  for (let seat = 1; seat <= 4; seat++) {
    const cx = 70 + (seat - 1) * 52, t = teamOf(seat), T = TEAM[t];
    circle(ctx, cx, cy, 22, T.tint);
    text(ctx, initial(sc.names(seat)), cx, cy + 1, font(18, "900"), T.c, "center");
    circle(ctx, cx + 15, cy + 15, 8, C.card); icon[T.suit](ctx, cx + 15, cy + 15.5, 9, T.c);
  }
  let fs = 20;
  const label = `${TEAM_NAME[lose]}停在 ${levelName(st[lose].level)}`;
  while (fs > 14 && W - 48 - (measure(ctx, label, font(fs, "700")) + 32) < 262) fs -= 1;
  const pw = measure(ctx, label, font(fs, "700")) + 32;
  fillRR(ctx, W - 48 - pw, cy - 22, pw, 44, 22, CREAM(0.16));
  text(ctx, label, W - 48 - pw / 2, cy, font(fs, "700"), C.cream, "center");
}
