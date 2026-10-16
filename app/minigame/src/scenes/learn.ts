// 05 · 学牌列表、06 · 规则速览（#106）。课程内容和进度直接复用 H5 的 learn.ts。

import type { App, Scene } from "../core/app";
import { C, Ctx, Scroller, fillRR, font, paragraph, strokeRR, text, wrap, ellipsize } from "../core/ui";
import { PAD, button, icon, nav, segmented, suit } from "../core/widgets";
import { LESSONS, LEVELS, doneLessons, lessonById, lessonsOf, markDone, nextLesson, type Level } from "../lib/learn";
import { TAB_H } from "./home";
import type { Nav } from "./nav";

export class Learn implements Scene {
  tab = "learn";
  private level: Level;
  private scroll = new Scroller();

  constructor(private app: App, private nav: Nav) {
    const done = doneLessons();
    this.level = LEVELS.find((l) => lessonsOf(l.key).some((x) => !done.has(x.id)))?.key ?? "basic";
  }

  onResume() { this.app.redraw(); }
  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - TAB_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, bottom = a.H - TAB_H - a.safeBottom;
    const done = doneLessons();
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    // 进度卡
    const first = LESSONS.find((l) => !done.has(l.id));
    fillRR(ctx, PAD, y, W - PAD * 2, 118, 18, C.redDeep);
    text(ctx, "我的学习进度", PAD + 18, y + 28, font(17, "900", true), C.cream);
    text(ctx, `已学 ${done.size} / ${LESSONS.length} 课`, W - PAD - 18, y + 28, font(13), "rgba(255,247,236,0.85)", "right");
    fillRR(ctx, PAD + 18, y + 48, W - PAD * 2 - 36, 8, 4, "rgba(255,247,236,0.25)");
    fillRR(ctx, PAD + 18, y + 48, Math.max(8, (W - PAD * 2 - 36) * done.size / LESSONS.length), 8, 4, C.gold);
    if (first) {
      const label = `${done.size === 0 ? "从第一课开始" : "接着学"}：${first.title} ›`;
      text(ctx, ellipsize(ctx, label, W - PAD * 2 - 36, font(14, "700")), PAD + 18, y + 88, font(14, "700"), C.gold);
      a.hit("learn-go", PAD, y + 66, W - PAD * 2, 52, () => this.nav.lesson(first.id));
    } else text(ctx, "九课都学完了，去约一局实战吧", PAD + 18, y + 88, font(14, "700"), C.gold);
    y += 132;

    const idx = LEVELS.findIndex((l) => l.key === this.level);
    segmented(a, ctx, "level", PAD, y, W - PAD * 2, LEVELS.map((l) => l.label), idx, (i) => { this.level = LEVELS[i].key; this.scroll.scrollTo(0); });
    y += 58;
    const meta = LEVELS[idx];
    text(ctx, meta.title, PAD, y + 12, font(17, "900", true), C.ink);
    text(ctx, meta.hint, W - PAD, y + 12, font(12), C.ink3, "right");
    y += 32;

    for (const l of lessonsOf(this.level)) {
      const n = LESSONS.indexOf(l) + 1, read = done.has(l.id), id = "lesson:" + l.id;
      const x = PAD, w = W - PAD * 2;
      const sumLines = wrap(ctx, l.summary, w - 150, font(12)).slice(0, 2);
      const h = 58 + sumLines.length * 17;
      fillRR(ctx, x, y + 2, w, h, 16, C.line);
      fillRR(ctx, x, y, w, h, 16, a.isPressed(id) ? C.paper : C.card);
      strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 16, C.line, 1);
      fillRR(ctx, x + 14, y + 14, 40, 52, 8, C.card);
      strokeRR(ctx, x + 14.5, y + 14.5, 39, 51, 8, C.line2, 1);
      text(ctx, String(n), x + 34, y + 32, font(16, "900", true), C.red, "center");
      icon.spade(ctx, x + 34, y + 52, 13, C.ink);
      text(ctx, ellipsize(ctx, l.title, w - 160, font(15, "700")), x + 68, y + 24, font(15, "700"), C.ink);
      sumLines.forEach((ln, i) => text(ctx, ln, x + 68, y + 44 + i * 17, font(12), C.ink3));
      text(ctx, `${l.minutes} 分钟 · 含 1 道练习题`, x + 68, y + h - 14, font(11), C.ink4);
      const sw = read ? 52 : 52;
      fillRR(ctx, x + w - 14 - sw, y + 14, sw, 22, 11, read ? "#e3efe5" : C.beige);
      text(ctx, read ? "已学完" : "未开始", x + w - 14 - sw / 2, y + 25, font(11, "600"), read ? C.green : C.ink3, "center");
      a.hit(id, x, y, w, h, () => this.nav.lesson(l.id));
      y += h + 10;
    }
    y += 16;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, "学牌");
  }
}

const BAR_H = 76;

export class LessonScene implements Scene {
  private picked: number | null = null;
  private passed: boolean;
  private scroll = new Scroller();
  private quizY = 0;

  constructor(private app: App, private id: string, private nav: Nav) {
    this.passed = doneLessons().has(id);
  }

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - BAR_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, l = lessonById(this.id);
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    if (!l) { nav(a, ctx, "学牌", () => this.nav.back()); text(ctx, "没有这一课", W / 2, a.contentTop + 60, font(15), C.ink3, "center"); return; }
    const top = a.contentTop, bottom = a.H - BAR_H - a.safeBottom, fw = W - PAD * 2;
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    text(ctx, l.title, PAD, y + 20, font(26, "900", true), C.ink);
    text(ctx, `${l.summary} · ${l.minutes} 分钟`, PAD, y + 50, font(13), C.ink3);
    y += 72;

    if (l.hand) {
      const cw = 40, ch = 56, gap = -6;
      const total = l.hand.length * (cw + gap) - gap;
      const scale = Math.min(1, (fw - 24) / total);
      const fh = ch * scale + 30 + (l.handCaption ? 24 : 0);
      fillRR(ctx, PAD, y, fw, fh, 18, "#2f5d47");
      let x = PAD + (fw - total * scale) / 2;
      for (const c of l.hand) {
        const red = c.suit === "♥" || c.suit === "♦";
        fillRR(ctx, x, y + 15 + (c.hot ? -6 : 0), cw * scale, ch * scale, 6, C.card);
        strokeRR(ctx, x + 0.5, y + 15.5 + (c.hot ? -6 : 0), cw * scale - 1, ch * scale - 1, 6, c.hot ? C.gold : C.line2, c.hot ? 2 : 1);
        text(ctx, c.rank, x + 7 * scale, y + 15 + 13 * scale + (c.hot ? -6 : 0), font(Math.round(14 * scale), "900", true), red ? C.red : C.ink);
        suit(ctx, c.suit, x + cw * scale / 2, y + 15 + ch * scale * 0.65 + (c.hot ? -6 : 0), 16 * scale, red ? C.red : C.ink);
        x += (cw + gap) * scale;
      }
      if (l.handCaption) text(ctx, l.handCaption, W / 2, y + fh - 16, font(12), "rgba(255,247,236,0.85)", "center");
      y += fh + 16;
    }

    l.sections.forEach((s, i) => {
      text(ctx, String(i + 1).padStart(2, "0"), PAD, y + 12, font(14, "900", true), C.red);
      text(ctx, s.title, PAD + 28, y + 12, font(16, "700"), C.ink);
      y += 28;
      y += paragraph(ctx, s.body, PAD, y, fw, font(14), C.ink2, 23) + 16;
    });

    if (l.tip) {
      const lines = wrap(ctx, l.tip, fw - 32, font(14));
      const h = 42 + lines.length * 22;
      fillRR(ctx, PAD, y, fw, h, 14, C.sand);
      text(ctx, "秘诀", PAD + 16, y + 20, font(13, "700"), C.redInk);
      lines.forEach((ln, i) => text(ctx, ln, PAD + 16, y + 42 + i * 22, font(14), C.ink));
      y += h + 18;
    }

    // 练一练
    this.quizY = y + this.scroll.pos;
    text(ctx, "练一练", PAD, y + 12, font(18, "900", true), C.ink);
    y += 30;
    y += paragraph(ctx, l.quiz.question, PAD, y, fw, font(15), C.ink, 23) + 10;
    l.quiz.options.forEach((o, i) => {
      const right = i === l.quiz.answer;
      const state = this.picked === i ? (right ? "right" : "wrong") : this.passed && right ? "right" : "";
      const lines = wrap(ctx, o, fw - 32, font(14));
      const h = 24 + lines.length * 21, id = "opt:" + i;
      const bg = state === "right" ? "#e3efe5" : state === "wrong" ? C.errTint : a.isPressed(id) ? C.beige : C.card;
      fillRR(ctx, PAD, y, fw, h, 12, bg);
      strokeRR(ctx, PAD + 0.5, y + 0.5, fw - 1, h - 1, 12, state === "right" ? C.green : state === "wrong" ? C.red : C.line, 1);
      lines.forEach((ln, j) => text(ctx, ln, PAD + 16, y + 12 + 10.5 + j * 21, font(14), C.ink));
      a.hit(id, PAD, y, fw, h, () => {
        this.picked = i;
        if (right) { markDone(l.id); this.passed = true; }
      });
      y += h + 8;
    });
    if (this.picked !== null) {
      const ok = this.picked === l.quiz.answer;
      y += paragraph(ctx, ok ? `答对了。${l.quiz.explain}` : "不对，再想想。", PAD, y + 4, fw, font(14, "600"), ok ? C.green : C.red, 22) + 10;
    }
    y += 24;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, l.title, () => this.nav.back());

    const by = a.H - BAR_H - a.safeBottom;
    ctx.fillStyle = C.card; ctx.fillRect(0, by, W, BAR_H + a.safeBottom);
    ctx.fillStyle = C.line; ctx.fillRect(0, by, W, 1);
    const next = nextLesson(this.id);
    if (!this.passed) button(a, ctx, "do-quiz", PAD, by + 12, fw, 52, "先做练习题", "soft", () => this.scroll.scrollTo(this.quizY - top - 20));
    else if (next) button(a, ctx, "next", PAD, by + 12, fw, 52, `下一课：${next.title}`, "primary", () => this.nav.lesson(next.id));
    else button(a, ctx, "finish", PAD, by + 12, fw, 52, "学完了，回到学牌", "primary", () => this.nav.back());
  }
}
