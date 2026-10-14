// 常用控件：导航栏、按钮、胶囊、分段、输入框、列表行、横幅、弹层模板。
// 每个控件当帧画、当帧登记热区；位置由调用方从上往下排，函数返回占用的高度。

import type { App, Sheet } from "./app";
import { C, Ctx, card, circle, ellipsize, fillRR, font, measure, paragraph, strokeRR, text, wrap } from "./ui";

export const PAD = 16;

// ── 导航栏：和右上角胶囊同一行，右边留给胶囊 ──
export function nav(app: App, ctx: Ctx, title: string, onBack?: () => void, left?: { label: string; onTap: () => void }) {
  ctx.fillStyle = C.paper;
  ctx.fillRect(0, 0, app.W, app.contentTop);
  if (onBack) {
    if (app.isPressed("nav-back")) circle(ctx, 26, app.navY, 18, C.beige);
    // 用路径画箭头，不依赖字体里有没有「‹」
    ctx.beginPath();
    ctx.moveTo(30, app.navY - 9);
    ctx.lineTo(21, app.navY);
    ctx.lineTo(30, app.navY + 9);
    ctx.strokeStyle = C.ink;
    ctx.lineWidth = 2.4;
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    ctx.stroke();
    app.hit("nav-back", 0, app.navY - 22, 60, 44, onBack);
  } else if (left) {
    text(ctx, left.label, PAD + 2, app.navY, font(15, "600"), app.isPressed("nav-left") ? C.ink : C.ink3);
    app.hit("nav-left", 0, app.navY - 22, 72, 44, left.onTap);
  }
  // 标题居中，但不能伸到胶囊下面
  const maxW = (app.menu.left - app.W / 2 - 8) * 2;
  text(ctx, ellipsize(ctx, title, maxW, font(17, "700")), app.W / 2, app.navY, font(17, "700"), C.ink, "center");
}

// ── 按钮 ──
export type BtnStyle = "primary" | "ghost" | "soft" | "danger" | "dark" | "green";

export function button(
  app: App, ctx: Ctx, id: string, x: number, y: number, w: number, h: number,
  label: string, style: BtnStyle, onTap: (() => void) | null, opts: { disabled?: boolean; size?: number; radius?: number } = {},
) {
  const pressed = app.isPressed(id) && !opts.disabled;
  const r = opts.radius ?? Math.min(16, h / 2);
  let bg = C.red, fg = C.cream, border: string | null = null;
  if (style === "primary") bg = pressed ? C.redDeep : C.red;
  if (style === "ghost") { bg = pressed ? C.beige : C.card; fg = C.ink; border = C.line2; }
  if (style === "dark") { bg = pressed ? C.beige : C.card; fg = C.ink; border = C.ink; }
  if (style === "soft") { bg = pressed ? C.line2 : C.beige; fg = C.ink2; }
  if (style === "danger") { bg = pressed ? C.redTint : C.card; fg = C.red; border = "#e3b5ae"; }
  if (style === "green") { bg = C.greenTint; fg = C.green; }
  if (opts.disabled) {
    if (style === "primary") { bg = C.khaki; fg = C.cream; } else fg = C.ink4;
  }
  fillRR(ctx, x, y, w, h, r, bg);
  if (border) strokeRR(ctx, x + 0.75, y + 0.75, w - 1.5, h - 1.5, r, border, style === "dark" ? 1.5 : 1);
  const f = font(opts.size ?? 16, "700");
  text(ctx, ellipsize(ctx, label, w - 20, f), x + w / 2, y + h / 2 + 0.5, f, fg, "center");
  app.hit(id, x, y, w, h, opts.disabled ? null : onTap);
}

/** 纯文字按钮（「先不用」「算了」）。 */
export function textLink(app: App, ctx: Ctx, id: string, cx: number, cy: number, label: string, onTap: () => void, color: string = C.ink3) {
  const f = font(14, "600");
  const w = measure(ctx, label, f) + 24;
  text(ctx, label, cx, cy, f, app.isPressed(id) ? C.ink : color, "center");
  app.hit(id, cx - w / 2, cy - 18, w, 36, onTap);
}

// ── 胶囊和分段 ──
export function chip(app: App, ctx: Ctx, id: string, x: number, y: number, label: string, on: boolean, onTap: (() => void) | null, h = 36): number {
  const f = font(14, on ? "600" : "normal");
  const w = measure(ctx, label, f) + 28;
  const pressed = app.isPressed(id);
  fillRR(ctx, x, y, w, h, h / 2, on ? C.red : pressed ? C.beige : C.card);
  if (!on) strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, h / 2, C.line, 1);
  text(ctx, label, x + w / 2, y + h / 2, f, on ? C.cream : C.ink2, "center");
  if (onTap) app.hit(id, x, y, w, h, onTap);
  return w;
}

/** 自动换行的一组胶囊，返回新的 y。 */
export function chipsWrap(app: App, ctx: Ctx, idPrefix: string, x: number, y: number, maxW: number, items: string[], isOn: (i: number) => boolean, onPick: (i: number) => void): number {
  let cx = x;
  items.forEach((it, i) => {
    const w = measure(ctx, it, font(14, isOn(i) ? "600" : "normal")) + 28;
    if (cx + w > x + maxW && cx > x) { cx = x; y += 44; }
    chip(app, ctx, `${idPrefix}:${i}`, cx, y, it, isOn(i), () => onPick(i));
    cx += w + 8;
  });
  return y + 36;
}

export function segmented(app: App, ctx: Ctx, id: string, x: number, y: number, w: number, items: string[], active: number, onPick: (i: number) => void, h = 44) {
  fillRR(ctx, x, y, w, h, 16, C.beige);
  const cw = (w - 8) / items.length;
  items.forEach((d, i) => {
    const cx = x + 4 + i * cw;
    const on = i === active;
    if (on) fillRR(ctx, cx, y + 4, cw, h - 8, 13, C.card);
    text(ctx, d, cx + cw / 2, y + h / 2, font(15, on ? "700" : "500"), on ? C.ink : C.ink3, "center");
    app.hit(`${id}:${i}`, cx, y, cw, h, () => onPick(i));
  });
}

// ── 输入框：点了拉起原生键盘，画布上同步显示文字和光标 ──
export interface FieldOpts {
  key: string;
  value: string;
  placeholder: string;
  onChange: (v: string) => void;
  multiple?: boolean;
  maxLength?: number;
  error?: string;
  confirmType?: "done" | "next";
  onConfirm?: () => void;
  onDone?: () => void;
  fontSize?: number;
}
export function field(app: App, ctx: Ctx, x: number, y: number, w: number, o: FieldOpts): number {
  const fs = o.fontSize ?? 16;
  const f = font(fs);
  const lh = fs + 8;
  const focused = app.isEditing(o.key);
  const lines = o.value ? wrap(ctx, o.value, w - 32, f) : [o.placeholder];
  const h = o.multiple ? Math.max(84, 28 + lines.length * lh) : 52;
  fillRR(ctx, x, y, w, h, 12, o.error ? C.errTint : C.card);
  strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 12, o.error || focused ? C.red : C.line, focused ? 1.5 : 1);
  const ty = o.multiple ? y + 14 + lh / 2 : y + h / 2;
  const shown = o.multiple ? lines : [ellipsizeStart(ctx, lines.join(""), w - 34, f)];
  shown.forEach((ln, i) => text(ctx, ln, x + 16, ty + i * lh, f, o.value ? C.ink : C.ink4));
  if (focused && Date.now() % 1000 < 550) {
    const last = o.value ? shown[shown.length - 1] : "";
    const cx = x + 16 + measure(ctx, last, f) + 1;
    ctx.fillStyle = C.red;
    ctx.fillRect(cx, ty + (shown.length - 1) * lh - lh / 2 + 3, 1.5, lh - 6);
  }
  app.hit("field:" + o.key, x, y, w, h, () =>
    app.edit({
      key: o.key, value: o.value, multiple: o.multiple, maxLength: o.maxLength, confirmType: o.confirmType,
      onInput: o.onChange, onConfirm: o.onConfirm ? () => o.onConfirm!() : undefined, onDone: o.onDone ? () => o.onDone!() : undefined,
    }),
  );
  let used = h;
  if (o.error) used += 6 + paragraph(ctx, "⚠ " + o.error, x + 2, y + h + 6, w - 4, font(12), C.red, 18);
  return used;
}

/** 单行输入太长时显示末尾（正在打的那部分）。 */
function ellipsizeStart(ctx: Ctx, s: string, maxW: number, f: string): string {
  ctx.font = f;
  if (ctx.measureText(s).width <= maxW) return s;
  const chars = Array.from(s);
  let i = 0;
  while (i < chars.length && ctx.measureText("…" + chars.slice(i).join("")).width > maxW) i++;
  return "…" + chars.slice(i).join("");
}

// ── 列表 ──
export interface Row {
  id?: string;
  title: string;
  sub?: string;
  value?: string;
  valueColor?: string;
  chevron?: boolean;
  onTap?: (() => void) | null;
  icon?: string;
  disabled?: boolean;
}
/** 一组列表行装在一张卡片里，返回高度。 */
export function list(app: App, ctx: Ctx, x: number, y: number, w: number, rows: Row[]): number {
  const heights = rows.map((r) => rowHeight(ctx, r, w));
  const total = heights.reduce((a, b) => a + b, 0);
  card(ctx, x, y, w, total, 16);
  let ry = y;
  rows.forEach((r, i) => {
    const h = heights[i];
    const id = r.id ?? `row:${r.title}`;
    if (r.onTap && app.isPressed(id)) fillRR(ctx, x + 1, ry + 1, w - 2, h - 2, 15, C.paper);
    let tx = x + 16;
    if (r.icon) { text(ctx, r.icon, tx + 10, ry + h / 2, font(18), C.ink2, "center"); tx += 32; }
    const right = (r.chevron ? 24 : 0) + (r.value ? measure(ctx, r.value, font(13)) + 12 : 0);
    const tw = x + w - 16 - right - tx;
    text(ctx, ellipsize(ctx, r.title, tw, font(15, "600")), tx, ry + (r.sub ? 22 : h / 2), font(15, "600"), r.disabled ? C.ink4 : C.ink);
    if (r.sub) paragraph(ctx, r.sub, tx, ry + 32, tw, font(12), C.ink3, 17);
    if (r.value) text(ctx, r.value, x + w - 16 - (r.chevron ? 22 : 0), ry + h / 2, font(13), r.valueColor ?? C.ink3, "right");
    if (r.chevron) chevron(ctx, x + w - 22, ry + h / 2, C.ink4);
    if (i < rows.length - 1) { ctx.fillStyle = C.line2; ctx.fillRect(x + 16, ry + h, w - 32, 1); }
    if (r.onTap) app.hit(id, x, ry, w, h, r.disabled ? null : r.onTap);
    ry += h;
  });
  return total;
}
function rowHeight(ctx: Ctx, r: Row, w: number): number {
  if (!r.sub) return 54;
  const tw = w - 32 - (r.icon ? 32 : 0) - (r.chevron ? 24 : 0) - (r.value ? measure(ctx, r.value, font(13)) + 12 : 0);
  return 42 + wrap(ctx, r.sub, tw, font(12)).length * 17;
}

export function chevron(ctx: Ctx, x: number, cy: number, color: string) {
  ctx.beginPath();
  ctx.moveTo(x - 3, cy - 6);
  ctx.lineTo(x + 3, cy);
  ctx.lineTo(x - 3, cy + 6);
  ctx.strokeStyle = color;
  ctx.lineWidth = 2;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.stroke();
}

// ── 横幅（改时间 / 局头转交 / 候补 / 被邀请 / 已取消）──
export type BannerKind = "red" | "ink" | "strip" | "green";
export function banner(
  app: App, ctx: Ctx, id: string, x: number, y: number, w: number,
  opts: { kind: BannerKind; icon?: string; title: string; sub?: string; onTap?: () => void; onDismiss?: () => void; dot?: boolean },
): number {
  const tf = font(14, "600"), sf = font(12);
  const tx = x + (opts.icon || opts.dot ? 42 : 16);
  const tw = x + w - (opts.onDismiss ? 40 : 16) - tx;
  const tl = wrap(ctx, opts.title + (opts.onTap ? " ›" : ""), tw, tf);
  const sl = opts.sub ? wrap(ctx, opts.sub, tw, sf) : [];
  const h = 24 + tl.length * 20 + sl.length * 17;
  const palette = {
    red: { bg: C.redTint, fg: C.redInk, ic: C.red },
    ink: { bg: C.ink, fg: C.cream, ic: C.gold },
    strip: { bg: C.card, fg: C.ink, ic: C.khaki },
    green: { bg: C.greenTint, fg: C.ink, ic: C.green },
  }[opts.kind];
  fillRR(ctx, x, y, w, h, 14, palette.bg);
  if (opts.kind === "strip") strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 14, C.line, 1);
  if (opts.icon) text(ctx, opts.icon, x + 22, y + 12 + 10, font(17), palette.ic, "center");
  if (opts.dot) circle(ctx, x + 22, y + 22, 5, palette.ic);
  tl.forEach((ln, i) => text(ctx, ln, tx, y + 12 + 10 + i * 20, tf, palette.fg));
  sl.forEach((ln, i) => text(ctx, ln, tx, y + 12 + tl.length * 20 + 8 + i * 17, sf, opts.kind === "ink" ? "rgba(255,247,236,0.75)" : C.ink3));
  if (opts.onTap) app.hit(id, x, y, w - (opts.onDismiss ? 40 : 0), h, opts.onTap);
  if (opts.onDismiss) {
    const cx = x + w - 20, cy = y + 22;
    ctx.beginPath();
    ctx.moveTo(cx - 5, cy - 5); ctx.lineTo(cx + 5, cy + 5);
    ctx.moveTo(cx + 5, cy - 5); ctx.lineTo(cx - 5, cy + 5);
    ctx.strokeStyle = palette.fg; ctx.lineWidth = 1.8; ctx.stroke();
    app.hit(id + ":x", x + w - 44, y, 44, Math.max(44, h), opts.onDismiss);
  }
  return h;
}

export function disclaimer(app: App, ctx: Ctx, y: number): number {
  text(ctx, "纯娱乐 · 不设彩头 · 不涉及任何金钱输赢", app.W / 2, y + 16, font(11), C.ink4, "center");
  return 40;
}

export function emptyState(app: App, ctx: Ctx, y: number, lines: string[]): number {
  lines.forEach((ln, i) => text(ctx, ln, app.W / 2, y + 30 + i * 24, font(i === 0 ? 15 : 13), i === 0 ? C.ink3 : C.ink4, "center"));
  return 40 + lines.length * 24;
}

export function errorLine(app: App, ctx: Ctx, y: number, msg: string): number {
  return 12 + paragraph(ctx, msg, PAD, y + 6, app.W - PAD * 2, font(13), C.red, 19, "center");
}

/** 头像圆：局头红底、我金色、其余米色。 */
export function avatar(ctx: Ctx, cx: number, cy: number, r: number, ch: string, style: "host" | "me" | "other" | "star" = "other") {
  const bg = style === "me" ? C.gold : style === "host" ? C.redTint : style === "star" ? C.sand : C.beige;
  circle(ctx, cx, cy, r, bg);
  text(ctx, ch, cx, cy + 1, font(Math.round(r * 0.95), "900", true), style === "host" ? C.red : C.ink, "center");
}

// ── 弹层模板 ──
export interface SheetButton {
  label: string;
  style: BtnStyle | "link";
  onTap: () => void;
  disabled?: boolean;
}

export function sheetHeader(ctx: Ctx, y: number, w: number, title: string, sub?: string, center = false): number {
  let cy = y + 30;
  const tf = font(20, "900", true);
  const tl = wrap(ctx, title, w - 40, tf);
  tl.forEach((ln, i) => text(ctx, ln, center ? w / 2 : 20, cy + 6 + i * 28, tf, C.ink, center ? "center" : "left"));
  cy += 6 + tl.length * 28;
  if (sub) cy += 4 + paragraph(ctx, sub, 20, cy - 4, w - 40, font(13), C.ink3, 20, center ? "center" : "left");
  return cy + 8;
}

export function sheetButtons(app: App, ctx: Ctx, y: number, w: number, buttons: SheetButton[], idPrefix = "sb"): number {
  let cy = y;
  buttons.forEach((b, i) => {
    if (b.style === "link") {
      textLink(app, ctx, `${idPrefix}:${i}`, w / 2, cy + 20, b.label, b.onTap);
      cy += 40;
    } else {
      button(app, ctx, `${idPrefix}:${i}`, 20, cy, w - 40, 50, b.label, b.style, b.onTap, { disabled: b.disabled });
      cy += 60;
    }
  });
  return cy;
}

/** 标题 + 说明 + 一列按钮的底部弹层（K1 移出、K3 取消……）。 */
export function simpleSheet(app: App, o: { title: string; sub?: string; buttons: (s: Sheet) => SheetButton[]; dialog?: boolean; modal?: boolean; onClose?: () => void }): Sheet {
  const s: Sheet = {
    dialog: o.dialog,
    modal: o.modal,
    onClose: o.onClose,
    draw(ctx, y, w) {
      let cy = sheetHeader(ctx, y, w, o.title, o.sub, !!o.dialog);
      cy = sheetButtons(app, ctx, cy + 4, w, o.buttons(s));
      return cy - y + 8;
    },
  };
  app.openSheet(s);
  return s;
}

/** 复制到剪贴板。微信会弹自己的「内容已复制」，我们只在失败时说话。 */
export function copy(app: App, s: string, ok?: string, fail = "复制不了，稍后再试") {
  wx.setClipboardData({
    data: s,
    success: () => { if (ok) setTimeout(() => app.toast(ok), 1600); },
    fail: () => app.toast(fail),
  });
}
