// 常用控件：导航栏、按钮、胶囊、分段、输入框、列表行、横幅、弹层模板。
// 每个控件当帧画、当帧登记热区；位置由调用方从上往下排，函数返回占用的高度。

import type { App, Sheet } from "./app";
import { C, Ctx, Scroller, card, circle, ellipsize, fillRR, font, measure, paragraph, strokeRR, text, wrap } from "./ui";
import { slashDate, weekday, wibAt, wibParts } from "../lib/fmt";

export const PAD = 16;

// ── 路径图标（适配稿 4.2）：颜色要随状态变的、或不是每台机器字体里都有的符号，一律画路径。
// iOS 可能把 ♠ ♥ 画成彩色 emoji，fillStyle 就不起作用了。形状照搬 design/guandan-minigame/prototype.html。
function strokeIcon(ctx: Ctx, c: string, lw: number) {
  ctx.strokeStyle = c;
  ctx.lineWidth = lw;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.stroke();
}
export type Suit = "spade" | "heart" | "club" | "diamond";
export const icon = {
  back(ctx: Ctx, x: number, cy: number, c: string) {
    ctx.beginPath(); ctx.moveTo(x + 9, cy - 9); ctx.lineTo(x, cy); ctx.lineTo(x + 9, cy + 9); strokeIcon(ctx, c, 2.4);
  },
  check(ctx: Ctx, cx: number, cy: number, c: string) {
    ctx.beginPath(); ctx.moveTo(cx - 5, cy); ctx.lineTo(cx - 1.5, cy + 4); ctx.lineTo(cx + 6, cy - 5); strokeIcon(ctx, c, 2.2);
  },
  /** 实心圆感叹号，直径 14 */
  alert(ctx: Ctx, cx: number, cy: number, c: string) {
    circle(ctx, cx, cy, 7, c);
    ctx.fillStyle = "#fff";
    ctx.fillRect(cx - 1, cy - 4.2, 2, 5);
    ctx.fillRect(cx - 1, cy + 2.2, 2, 2);
  },
  /** 方框 + 向上箭头，替代「⇪」 */
  share(ctx: Ctx, cx: number, cy: number, c: string) {
    ctx.beginPath();
    ctx.moveTo(cx - 6, cy - 1); ctx.lineTo(cx - 6, cy + 7); ctx.lineTo(cx + 6, cy + 7); ctx.lineTo(cx + 6, cy - 1);
    ctx.moveTo(cx, cy + 2); ctx.lineTo(cx, cy - 8);
    ctx.moveTo(cx - 4, cy - 4.5); ctx.lineTo(cx, cy - 8.5); ctx.lineTo(cx + 4, cy - 4.5);
    strokeIcon(ctx, c, 1.8);
  },
  heart(ctx: Ctx, cx: number, cy: number, s: number, c: string) {
    ctx.beginPath(); ctx.moveTo(cx, cy + s * 0.42);
    ctx.bezierCurveTo(cx - s * 0.95, cy - s * 0.12, cx - s * 0.5, cy - s * 0.78, cx, cy - s * 0.28);
    ctx.bezierCurveTo(cx + s * 0.5, cy - s * 0.78, cx + s * 0.95, cy - s * 0.12, cx, cy + s * 0.42);
    ctx.fillStyle = c; ctx.fill();
  },
  spade(ctx: Ctx, cx: number, cy: number, s: number, c: string) {
    ctx.beginPath(); ctx.moveTo(cx, cy - s * 0.5);
    ctx.bezierCurveTo(cx - s * 0.95, cy + s * 0.05, cx - s * 0.5, cy + s * 0.62, cx, cy + s * 0.18);
    ctx.bezierCurveTo(cx + s * 0.5, cy + s * 0.62, cx + s * 0.95, cy + s * 0.05, cx, cy - s * 0.5);
    ctx.fillStyle = c; ctx.fill();
    ctx.beginPath(); ctx.moveTo(cx, cy + s * 0.12); ctx.lineTo(cx - s * 0.2, cy + s * 0.5); ctx.lineTo(cx + s * 0.2, cy + s * 0.5); ctx.closePath(); ctx.fill();
  },
  diamond(ctx: Ctx, cx: number, cy: number, s: number, c: string) {
    ctx.beginPath(); ctx.moveTo(cx, cy - s * 0.5);
    ctx.quadraticCurveTo(cx + s * 0.12, cy - s * 0.12, cx + s * 0.38, cy); ctx.quadraticCurveTo(cx + s * 0.12, cy + s * 0.12, cx, cy + s * 0.5);
    ctx.quadraticCurveTo(cx - s * 0.12, cy + s * 0.12, cx - s * 0.38, cy); ctx.quadraticCurveTo(cx - s * 0.12, cy - s * 0.12, cx, cy - s * 0.5);
    ctx.fillStyle = c; ctx.fill();
  },
  club(ctx: Ctx, cx: number, cy: number, s: number, c: string) {
    const r = s * 0.2;
    circle(ctx, cx, cy - s * 0.24, r, c); circle(ctx, cx - s * 0.22, cy + s * 0.06, r, c); circle(ctx, cx + s * 0.22, cy + s * 0.06, r, c);
    ctx.beginPath(); ctx.moveTo(cx, cy - s * 0.05); ctx.lineTo(cx - s * 0.18, cy + s * 0.5); ctx.lineTo(cx + s * 0.18, cy + s * 0.5); ctx.closePath();
    ctx.fillStyle = c; ctx.fill();
  },
};
const SUITS: Record<string, Suit> = { "♠": "spade", "♥": "heart", "♣": "club", "♦": "diamond" };
/** 花色字符（课程数据里写的是字符）换成路径。 */
export function suit(ctx: Ctx, ch: string, cx: number, cy: number, s: number, c: string) {
  const k = SUITS[ch];
  if (k) icon[k](ctx, cx, cy, s, c);
  else text(ctx, ch, cx, cy, font(s), c, "center");
}

// ── 导航栏：和右上角胶囊同一行，右边留给胶囊 ──
export function nav(app: App, ctx: Ctx, title: string, onBack?: () => void, left?: { label: string; onTap: () => void }) {
  ctx.fillStyle = C.paper;
  ctx.fillRect(0, 0, app.W, app.contentTop);
  if (onBack) {
    if (app.isPressed("nav-back")) circle(ctx, 26, app.navY, 18, C.beige);
    icon.back(ctx, 21, app.navY, C.ink);
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
  label: string, style: BtnStyle, onTap: (() => void) | null,
  opts: { disabled?: boolean; size?: number; radius?: number; icon?: (ctx: Ctx, cx: number, cy: number, c: string) => void } = {},
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
  if (opts.icon) {
    // 图标 14 宽 + 间距 8，和文字一起居中
    const s = ellipsize(ctx, label, w - 42, f);
    const left = x + w / 2 - (measure(ctx, s, f) + 22) / 2;
    opts.icon(ctx, left + 7, y + h / 2, fg);
    text(ctx, s, left + 22, y + h / 2 + 0.5, f, fg);
  } else text(ctx, ellipsize(ctx, label, w - 20, f), x + w / 2, y + h / 2 + 0.5, f, fg, "center");
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

// ── 时间胶囊行（适配稿第三节）：左边固定标签，右边单行横滑，伸到屏幕右边缘 ──
export const ROW_X0 = 20 + 48; // 标签列：左边距 20，宽 48
const ROW_H = 36, ROW_GAP = 8;
function rowChipW(ctx: Ctx, label: string, on: boolean): number {
  return measure(ctx, label, font(14, on ? "700" : "normal")) + 28;
}
/** 选中项落在可视区左起约 1/3 处的滚动量（不小于 0，不超过最大值）。 */
export function chipRowScrollFor(app: App, ctx: Ctx, items: string[], on: number): number {
  let x = 0, total = 0;
  items.forEach((it, i) => {
    const w = rowChipW(ctx, it, i === on) + ROW_GAP;
    if (i < on) x += w;
    total += w;
  });
  const view = app.W - ROW_X0;
  return Math.max(0, Math.min(x - view / 3, total - ROW_GAP + 20 - view));
}
function rgba(hex: string, a: number): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${a})`;
}
/**
 * 一行胶囊：可视区从 x = 68 到屏幕右边缘；右侧 32 渐隐，滑过之后左侧 20 渐隐。
 * 不可选的胶囊画虚线边、--khaki 字，没有热区。返回下一行的 y（行距 10）。
 */
export function chipRow(
  app: App, ctx: Ctx, id: string, y: number, label: string, items: string[], on: number, onPick: (i: number) => void,
  o: { scroll: Scroller; disabled?: (i: number) => boolean; bg?: string },
): number {
  const W = app.W, bg = o.bg ?? C.card, x0 = ROW_X0;
  text(ctx, label, 20, y + ROW_H / 2, font(13, "700"), C.ink2);
  let total = 0;
  const ws = items.map((it, i) => { const w = rowChipW(ctx, it, i === on); total += w + ROW_GAP; return w; });
  o.scroll.setMax(total - ROW_GAP + 20 - (W - x0));
  ctx.save();
  ctx.beginPath(); ctx.rect(x0, y - 4, W - x0, ROW_H + 8); ctx.clip();
  let x = x0 - o.scroll.pos;
  items.forEach((it, i) => {
    const w = ws[i], sel = i === on, off = !sel && !!o.disabled?.(i), cid = `${id}:${i}`;
    if (x + w > x0 && x < W) {
      fillRR(ctx, x, y, w, ROW_H, ROW_H / 2, sel ? C.red : app.isPressed(cid) ? C.beige : C.card);
      if (!sel) strokeRR(ctx, x + 0.5, y + 0.5, w - 1, ROW_H - 1, ROW_H / 2, C.line, 1, off ? [3, 3] : undefined);
      text(ctx, it, x + w / 2, y + ROW_H / 2, font(14, sel ? "700" : "normal"), sel ? C.cream : off ? C.khaki : C.ink2, "center");
      // 热区只登记看得见的部分，免得点到标签列下面藏着的胶囊
      const hx = Math.max(x, x0), hw = Math.min(x + w, W) - hx;
      if (!off && hw > 0) app.hit(cid, hx, y, hw, ROW_H, () => onPick(i));
    }
    x += w + ROW_GAP;
  });
  ctx.restore();
  const fade = (fx: number, w: number, toRight: boolean) => {
    const g = ctx.createLinearGradient(fx, 0, fx + w, 0);
    g.addColorStop(0, rgba(bg, toRight ? 0 : 1));
    g.addColorStop(1, rgba(bg, toRight ? 1 : 0));
    ctx.fillStyle = g;
    ctx.fillRect(fx, y - 2, w, ROW_H + 4);
  };
  if (x - ROW_GAP > W + 0.5) fade(W - 32, 32, true);
  if (o.scroll.pos > 0.5) fade(x0, 20, false);
  return y + ROW_H + 10;
}

/** 服务端要求开局时间至少在 10 分钟后（server/internal/games/validate.go MinLeadTime）。 */
export const MIN_LEAD = 10 * 60;
const nowSec = () => Math.floor(Date.now() / 1000);

/** 选项网格（今天起 8 天 × 9–23 点 × 0/15/30/45 分）里最早一个离现在至少 MIN_LEAD 的时间。 */
export function nextSlot(now = nowSec()): number {
  for (let d = 0; d < 8; d++) for (let h = 9; h <= 23; h++) for (const m of [0, 15, 30, 45]) {
    const t = wibAt(d, h, m, now);
    if (t >= now + MIN_LEAD) return t;
  }
  return wibAt(1, 9, 0, now); // 走不到：明天 9 点一定在网格里且够远
}

/**
 * 哪天 / 几点 / 几分 三行胶囊（适配稿 3.2）：「改一下这一局」和开一局的「自定义」共用。
 * start 是默认选中的时间；它不在选项范围里（比如早上 8 点的局）就插到那一行最前面。
 * id 是胶囊热区前缀：`${id}-day` / `${id}-h` / `${id}-m`。dayCount：今天起几天可选。
 */
export function timePicker(app: App, start: number, id: string, dayCount = 8) {
  const parts = wibParts(start);
  const pad = (n: number) => String(n).padStart(2, "0");
  const dayName = (i: number) => { const t = wibAt(i, 12, 0); return i === 0 ? "今天" : i === 1 ? "明天" : `${weekday(t)} ${slashDate(t)}`; };
  const inRange = (d: number) => d >= 0 && d < dayCount;
  const days = Array.from({ length: dayCount }, (_, i) => i);
  if (!inRange(parts.dayOffset)) days.unshift(parts.dayOffset);
  const hours = Array.from({ length: 15 }, (_, i) => 9 + i);
  if (!hours.includes(parts.hour)) hours.unshift(parts.hour);
  const minutes = [0, 15, 30, 45];
  if (!minutes.includes(parts.minute)) minutes.unshift(parts.minute);
  const st = { day: parts.dayOffset, hour: parts.hour, minute: parts.minute };
  const isOrig = (day: number, hour: number) => day === parts.dayOffset && hour === parts.hour;
  /** 今天已经过去的钟点（这个钟点最晚的 :45 也过了）不能选；原来的值除外。 */
  const hourOff = (day: number, hour: number) => day === 0 && !isOrig(day, hour) && wibAt(0, hour, 45) <= nowSec();
  const rows = { day: new Scroller(), hour: new Scroller(), minute: new Scroller() };
  const rowY = { day: 0, hour: 0, minute: 0 };
  const labels = {
    day: () => days.map((d) => (!inRange(d) ? `${weekday(wibAt(d, 12, 0))} ${slashDate(wibAt(d, 12, 0))}` : dayName(d))),
    hour: () => hours.map((h) => `${h}点`),
    minute: () => minutes.map((m) => `:${pad(m)}`),
  };
  let scrolled = false, jumpHour = false;
  const scrollInto = (ctx: Ctx, k: "day" | "hour" | "minute") => {
    const on = k === "day" ? days.indexOf(st.day) : k === "hour" ? hours.indexOf(st.hour) : minutes.indexOf(st.minute);
    rows[k].setMax(1e6); // 真正的最大值在 chipRow 里设；chipRowScrollFor 已经夹在范围里
    rows[k].scrollTo(chipRowScrollFor(app, ctx, labels[k](), on));
  };
  const pickDay = (i: number) => {
    st.day = days[i];
    // 切回今天时，选中的钟点已经过了：跳到下一个能选的，并滚进可视区
    if (hourOff(st.day, st.hour)) {
      const next = hours.find((h) => h > st.hour && !hourOff(st.day, h)) ?? hours.find((h) => !hourOff(st.day, h));
      if (next !== undefined) { st.hour = next; jumpHour = true; }
    }
  };
  return {
    st, parts, inRange, days, hours, minutes, labels, dayName, hourOff, pickDay,
    unix: () => wibAt(st.day, st.hour, st.minute),
    scrollerAt(x: number, y: number): { s: Scroller; axis: "x" } | null {
      if (x < ROW_X0) return null;
      for (const k of ["day", "hour", "minute"] as const) if (y >= rowY[k] - 4 && y <= rowY[k] + 40) return { s: rows[k], axis: "x" };
      return null;
    },
    step: () => rows.day.step() || rows.hour.step() || rows.minute.step(),
    /** 画三行，返回下一行的 y。 */
    draw(ctx: Ctx, y: number): number {
      if (!scrolled) { scrollInto(ctx, "day"); scrollInto(ctx, "hour"); scrollInto(ctx, "minute"); scrolled = true; }
      if (jumpHour) { scrollInto(ctx, "hour"); jumpHour = false; }
      rowY.day = y;
      y = chipRow(app, ctx, `${id}-day`, y, "哪天", labels.day(), days.indexOf(st.day), pickDay, { scroll: rows.day });
      rowY.hour = y;
      y = chipRow(app, ctx, `${id}-h`, y, "几点", labels.hour(), hours.indexOf(st.hour), (i) => { st.hour = hours[i]; },
        { scroll: rows.hour, disabled: (i) => hourOff(st.day, hours[i]) });
      rowY.minute = y;
      return chipRow(app, ctx, `${id}-m`, y, "几分", labels.minute(), minutes.indexOf(st.minute), (i) => { st.minute = minutes[i]; }, { scroll: rows.minute });
    },
  };
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
  // 激活且到上限的 80% 起，右侧显示字数，文字区让出 44（适配稿 2.1）
  const max = o.maxLength ?? 40;
  const count = Array.from(o.value).length;
  const showCount = focused && count >= max * 0.8;
  const tw = w - 32 - (showCount ? 44 : 0);
  const lines = o.value ? wrap(ctx, o.value, tw, f) : [o.placeholder];
  const h = o.multiple ? Math.max(84, 28 + lines.length * lh) : 52;
  if (focused) {
    fillRR(ctx, x - 4, y - 4, w + 8, h + 8, 16, C.redRing); // 4px 焦点环（--sh-focus）
    app.focusRect = { y, h };
  }
  fillRR(ctx, x, y, w, h, 12, o.error ? C.errTint : C.card);
  strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 12, o.error || focused ? C.red : C.line, focused ? 1.5 : 1);
  const ty = o.multiple ? y + 14 + lh / 2 : y + h / 2;
  const shown = o.multiple ? lines : [ellipsizeStart(ctx, lines.join(""), tw - 2, f)];
  shown.forEach((ln, i) => text(ctx, ln, x + 16, ty + i * lh, f, o.value ? C.ink : C.ink4));
  if (showCount) text(ctx, `${count}/${max}`, x + w - 14, o.multiple ? y + h - 14 : y + h / 2, font(11), C.ink4, "right");
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
  if (o.error) {
    // 感叹号画路径（直径 14），不用「⚠」字符；说明可能折行
    icon.alert(ctx, x + 9, y + h + 15, C.red);
    used += 6 + paragraph(ctx, o.error, x + 22, y + h + 6, w - 24, font(12), C.red, 18);
  }
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
