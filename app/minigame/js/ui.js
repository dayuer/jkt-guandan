// 画布 UI 小工具：颜色令牌、圆角矩形、文字排版、滚动。
// 数值取自 design/guandan-h5/tokens.css，小游戏里没有 CSS，只能在这里再写一份。

const C = {
  red: '#b3352c', redDeep: '#8e2a23', redInk: '#7a231d', redTint: '#f6e2df',
  ink: '#2a1c17', ink2: '#5c4a3a', ink3: '#7a6a5c', ink4: '#9a8a7c',
  paper: '#f6efe4', card: '#fffcf7', cream: '#fff7ec', sand: '#f1e4cc',
  beige: '#ede3d3', line: '#e8dccb', line2: '#e3d6c3', khaki: '#c9b79e',
  gold: '#e9c46a', green: '#3e7c4f', scrim: 'rgba(28,20,17,0.45)',
};

const SANS = '"PingFang SC", "Noto Sans SC", "Hiragino Sans GB", sans-serif';
const SERIF = '"Songti SC", "Noto Serif SC", "STSong", serif';

function font(size, weight, serif) {
  return `${weight || 'normal'} ${size}px ${serif ? SERIF : SANS}`;
}

function rr(ctx, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function fillRR(ctx, x, y, w, h, r, color) {
  rr(ctx, x, y, w, h, r);
  ctx.fillStyle = color;
  ctx.fill();
}

function strokeRR(ctx, x, y, w, h, r, color, lw, dash) {
  rr(ctx, x, y, w, h, r);
  ctx.strokeStyle = color;
  ctx.lineWidth = lw || 1;
  if (dash) ctx.setLineDash(dash);
  ctx.stroke();
  if (dash) ctx.setLineDash([]);
}

// 卡片：底边多一条 2px 的「牌边」，再描 1px 边
function card(ctx, x, y, w, h, r) {
  fillRR(ctx, x, y + 2, w, h, r, C.line);
  fillRR(ctx, x, y, w, h, r, C.card);
  strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, r, C.line, 1);
}

function text(ctx, s, x, y, f, color, align, baseline) {
  ctx.font = f;
  ctx.fillStyle = color;
  ctx.textAlign = align || 'left';
  ctx.textBaseline = baseline || 'middle';
  ctx.fillText(s, x, y);
}

function ellipsize(ctx, s, maxW) {
  if (ctx.measureText(s).width <= maxW) return s;
  let lo = 0, hi = s.length;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (ctx.measureText(s.slice(0, mid) + '…').width <= maxW) lo = mid; else hi = mid - 1;
  }
  return s.slice(0, lo) + '…';
}

// 逐字折行（中英文混排够用；英文单词会被拆开，原型阶段先接受）
function wrap(ctx, s, maxW) {
  const lines = [];
  for (const para of String(s).split('\n')) {
    let line = '';
    for (const ch of para) {
      if (ctx.measureText(line + ch).width > maxW && line) { lines.push(line); line = ch; }
      else line += ch;
    }
    lines.push(line);
  }
  return lines;
}

function circle(ctx, cx, cy, r, fill, stroke, lw, dash) {
  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI * 2);
  if (fill) { ctx.fillStyle = fill; ctx.fill(); }
  if (stroke) {
    ctx.strokeStyle = stroke; ctx.lineWidth = lw || 1;
    if (dash) ctx.setLineDash(dash);
    ctx.stroke();
    if (dash) ctx.setLineDash([]);
  }
}

// 带惯性和回弹的一维滚动
class Scroller {
  constructor() { this.pos = 0; this.v = 0; this.max = 0; this.drag = false; this.lastT = 0; }
  setMax(m) { this.max = Math.max(0, m); }
  start() { this.drag = true; this.v = 0; this.lastT = Date.now(); }
  move(d) {
    const now = Date.now();
    const dt = Math.max(1, now - this.lastT);
    this.lastT = now;
    // 越界时阻尼一半
    const over = this.pos < 0 || this.pos > this.max;
    this.pos -= over ? d * 0.4 : d;
    this.v = -d / dt * 16; // 每帧位移
  }
  end() { this.drag = false; if (Date.now() - this.lastT > 80) this.v = 0; }
  // 返回 true 表示还在动，需要继续重绘
  step() {
    if (this.drag) return false;
    let moving = false;
    if (Math.abs(this.v) > 0.2) { this.pos += this.v; this.v *= 0.95; moving = true; } else this.v = 0;
    if (this.pos < 0) { this.pos += (0 - this.pos) * 0.2; this.v *= 0.5; if (this.pos > -0.5) this.pos = 0; else moving = true; }
    if (this.pos > this.max) { this.pos += (this.max - this.pos) * 0.2; this.v *= 0.5; if (this.pos - this.max < 0.5) this.pos = this.max; else moving = true; }
    return moving;
  }
  scrollTo(p) { this.pos = Math.min(Math.max(0, p), this.max); this.v = 0; }
}

const ease = {
  out: t => 1 - Math.pow(1 - t, 3),
  spring: t => { const c1 = 1.70158 * 1.5, c3 = c1 + 1; return 1 + c3 * Math.pow(t - 1, 3) + c1 * Math.pow(t - 1, 2); },
};

module.exports = { C, font, rr, fillRR, strokeRR, card, text, ellipsize, wrap, circle, Scroller, ease };
