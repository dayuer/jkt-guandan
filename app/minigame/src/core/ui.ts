// 画布绘图工具：颜色令牌、圆角矩形、文字排版、滚动。
// 数值取自 design/guandan-h5/tokens.css；小游戏里没有 CSS，只能在这里再写一份。

export const C = {
  red: "#b3352c", redDeep: "#8e2a23", redInk: "#7a231d", redTint: "#f6e2df",
  ink: "#2a1c17", ink2: "#5c4a3a", ink3: "#7a6a5c", ink4: "#9a8a7c",
  paper: "#f6efe4", card: "#fffcf7", cream: "#fff7ec", sand: "#f1e4cc",
  beige: "#ede3d3", line: "#e8dccb", line2: "#e3d6c3", khaki: "#c9b79e",
  gold: "#e9c46a", green: "#3e7c4f", greenTint: "#e3efe5", errTint: "#fbefed",
  redRing: "rgba(179,53,44,0.18)", // --red-ring：激活字段 4px 焦点环（--sh-focus）
};

const SANS = '"PingFang SC", "Noto Sans SC", "Hiragino Sans GB", sans-serif';
const SERIF = '"Songti SC", "Noto Serif SC", "STSong", serif';

export type Ctx = CanvasRenderingContext2D;

export function font(size: number, weight: string | number = "normal", serif = false): string {
  return `${weight} ${size}px ${serif ? SERIF : SANS}`;
}

export function rr(ctx: Ctx, x: number, y: number, w: number, h: number, r: number) {
  r = Math.max(0, Math.min(r, w / 2, h / 2));
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

export function fillRR(ctx: Ctx, x: number, y: number, w: number, h: number, r: number, color: string) {
  rr(ctx, x, y, w, h, r);
  ctx.fillStyle = color;
  ctx.fill();
}

export function strokeRR(ctx: Ctx, x: number, y: number, w: number, h: number, r: number, color: string, lw = 1, dash?: number[]) {
  rr(ctx, x, y, w, h, r);
  ctx.strokeStyle = color;
  ctx.lineWidth = lw;
  if (dash) ctx.setLineDash(dash);
  ctx.stroke();
  if (dash) ctx.setLineDash([]);
}

/** 卡片：底边多一条 2px 的「牌边」，再描 1px 边。 */
export function card(ctx: Ctx, x: number, y: number, w: number, h: number, r = 18) {
  fillRR(ctx, x, y + 2, w, h, r, C.line);
  fillRR(ctx, x, y, w, h, r, C.card);
  strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, r, C.line, 1);
}

export function text(ctx: Ctx, s: string, x: number, y: number, f: string, color: string, align: CanvasTextAlign = "left") {
  ctx.font = f;
  ctx.fillStyle = color;
  ctx.textAlign = align;
  ctx.textBaseline = "middle";
  ctx.fillText(s, x, y);
}

export function measure(ctx: Ctx, s: string, f: string): number {
  ctx.font = f;
  return ctx.measureText(s).width;
}

export function ellipsize(ctx: Ctx, s: string, maxW: number, f?: string): string {
  if (f) ctx.font = f;
  if (ctx.measureText(s).width <= maxW) return s;
  const chars = Array.from(s);
  let lo = 0, hi = chars.length;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (ctx.measureText(chars.slice(0, mid).join("") + "…").width <= maxW) lo = mid;
    else hi = mid - 1;
  }
  return chars.slice(0, lo).join("") + "…";
}

/** 折行：中文按字，英文和数字尽量按词。 */
export function wrap(ctx: Ctx, s: string, maxW: number, f?: string): string[] {
  if (f) ctx.font = f;
  const out: string[] = [];
  for (const para of String(s).split("\n")) {
    const tokens = para.match(/[A-Za-z0-9@#&+\-_.:/%'’]+|\s+|./gu) || [""];
    let line = "";
    for (const t of tokens) {
      if (ctx.measureText(line + t).width <= maxW || !line) {
        if (ctx.measureText(t).width > maxW && !line) {
          // 单个长词也放不下：按字拆
          for (const ch of Array.from(t)) {
            if (ctx.measureText(line + ch).width > maxW && line) { out.push(line); line = ch; } else line += ch;
          }
        } else line += t;
      } else {
        out.push(line.trimEnd());
        line = t.trimStart();
      }
    }
    out.push(line);
  }
  return out;
}

/** 多行文字，返回占用的高度。 */
export function paragraph(ctx: Ctx, s: string, x: number, y: number, maxW: number, f: string, color: string, lh: number, align: CanvasTextAlign = "left"): number {
  const lines = wrap(ctx, s, maxW, f);
  lines.forEach((ln, i) => text(ctx, ln, align === "center" ? x + maxW / 2 : x, y + lh / 2 + i * lh, f, color, align));
  return lines.length * lh;
}

export function circle(ctx: Ctx, cx: number, cy: number, r: number, fill?: string | null, stroke?: string | null, lw = 1, dash?: number[]) {
  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI * 2);
  if (fill) { ctx.fillStyle = fill; ctx.fill(); }
  if (stroke) {
    ctx.strokeStyle = stroke;
    ctx.lineWidth = lw;
    if (dash) ctx.setLineDash(dash);
    ctx.stroke();
    if (dash) ctx.setLineDash([]);
  }
}

/** 带惯性和回弹的一维滚动。pull 是顶部下拉的距离（用于下拉刷新）。 */
export class Scroller {
  pos = 0;
  v = 0;
  max = 0;
  drag = false;
  private lastT = 0;
  onPullRefresh: (() => void) | null = null;
  setMax(m: number) { this.max = Math.max(0, m); }
  start() { this.drag = true; this.v = 0; this.lastT = Date.now(); }
  move(d: number) {
    const now = Date.now();
    const dt = Math.max(1, now - this.lastT);
    this.lastT = now;
    const over = this.pos < 0 || this.pos > this.max;
    this.pos -= over ? d * 0.4 : d;
    this.v = (-d / dt) * 16;
  }
  end() {
    this.drag = false;
    if (Date.now() - this.lastT > 80) this.v = 0;
    if (this.pos < -64 && this.onPullRefresh) this.onPullRefresh();
  }
  get pull(): number { return this.pos < 0 ? -this.pos : 0; }
  step(): boolean {
    if (this.drag) return false;
    let moving = false;
    if (Math.abs(this.v) > 0.2) { this.pos += this.v; this.v *= 0.95; moving = true; } else this.v = 0;
    if (this.pos < 0) { this.pos += -this.pos * 0.2; this.v *= 0.5; if (this.pos > -0.5) this.pos = 0; else moving = true; }
    if (this.pos > this.max) { this.pos += (this.max - this.pos) * 0.2; this.v *= 0.5; if (this.pos - this.max < 0.5) this.pos = this.max; else moving = true; }
    return moving;
  }
  scrollTo(p: number) { this.pos = Math.min(Math.max(0, p), this.max); this.v = 0; }
}

export const ease = {
  out: (t: number) => 1 - Math.pow(1 - t, 3),
  spring: (t: number) => { const c1 = 1.70158 * 1.5, c3 = c1 + 1; return 1 + c3 * Math.pow(t - 1, 3) + c1 * Math.pow(t - 1, 2); },
};

const SKIP = /^[\s\p{Cc}\p{Cf}\p{P}\p{S}\u{FE00}-\u{FE0F}]$/u;
/** 头像上的首字，和服务端海报的规则一致（H5 Table.tsx 的 initial）。 */
export function initial(nickname: string): string {
  for (const ch of nickname) {
    if (SKIP.test(ch)) continue;
    const up = ch.toUpperCase();
    return Array.from(up).length === 1 ? up : ch;
  }
  return "?";
}
