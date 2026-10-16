// 画布运行时：场景栈、触摸分发（点按 / 拖动）、重绘循环、底部弹层、居中对话框、toast、键盘。
// 小游戏没有 DOM，所有「按钮」都是每帧登记的矩形热区；onTap 在 touchend 回调里同步执行，
// 订阅消息这类必须由用户点按触发的接口靠这一点。

import { C, Ctx, Scroller, ease, ellipsize, fillRR, font, text, wrap } from "./ui";

const TAP_SLOP = 8;
/** 微信原生输入条的高度。先按 56 预留；#141 真机核对 onKeyboardHeightChange 的高度是否已经包含它，包含就改成 0。 */
export const INPUT_BAR_H = 56;

export interface Hit {
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
  onTap: (() => void) | null;
}

export interface Scene {
  draw(ctx: Ctx): void;
  step?(): boolean;
  scrollerAt?(x: number, y: number): { s: Scroller; axis: "x" | "y" } | null;
  /** 返回 true 表示接管这次触摸（不再滚动） */
  onTouchStart?(x: number, y: number, hit: Hit | null): boolean | void;
  onTouchMove?(x: number, y: number, t: Touch0): void;
  onTouchEnd?(t: Touch0): void;
  /** 成为栈顶时 */
  onEnter?(): void;
  /** 离开栈（被弹出或被换掉）时：取消实时订阅等 */
  onLeave?(): void;
  /** 从上一层返回时 */
  onResume?(): void;
  /** 这个场景是否显示底部 Tab（Tab 级页面） */
  tab?: string;
}

/** 底部弹层：draw 返回内容高度。 */
export interface Sheet {
  draw(ctx: Ctx, y: number, w: number): number;
  scrollerAt?(x: number, y: number): { s: Scroller; axis: "x" | "y" } | null;
  step?(): boolean;
  onClose?(): void;
  /** 点遮罩能不能关（默认能） */
  modal?: boolean;
  /** 居中对话框而不是底部弹层 */
  dialog?: boolean;
}

/** 弹层的运行时状态：内容超出可视高度时内部滚动（App 管）。 */
import { KeyboardSession, type EditOptions } from "./keyboard";

type OpenSheet = Sheet & { t0: number; h: number; top: number; scroll: Scroller; focusFor: string };

export interface Touch0 {
  x0: number; y0: number; x: number; y: number;
  hit: Hit | null;
  scr: { s: Scroller; axis: "x" | "y" } | null;
  moved: boolean;
  t0: number;
}

interface Anim { t0: number; dur: number; onFrame: (p: number) => void; done?: () => void }

export class App {
  readonly W: number;
  readonly H: number;
  readonly dpr: number;
  readonly safeTop: number;
  readonly safeBottom: number;
  readonly menu: { top: number; bottom: number; left: number; right: number; width: number; height: number };
  readonly navY: number;
  readonly ctx: Ctx;
  readonly canvas: any;

  stack: Scene[] = [];
  private trans: { from: Scene; to: Scene; dir: 1 | -1; t0: number } | null = null;
  sheets: OpenSheet[] = [];
  private toastMsg: { msg: string; until: number } | null = null;
  private anims: Anim[] = [];
  hits: Hit[] = [];
  pressed: string | null = null;
  private touch: Touch0 | null = null;
  private dirty = true;
  /** 键盘那摊事全在会话里（core/keyboard.ts）：事件顺序、收起再拉、超时兜底都在那儿测。 */
  private keys: KeyboardSession = new KeyboardSession(wx, {
    redraw: () => this.redraw(),
    toast: (m) => this.toast(m),
    setTimeout: (fn, ms) => setTimeout(fn, ms) as unknown,
    clearTimeout: (id) => clearTimeout(id as any),
  });
  /** 正在编辑的字段（会话持有）。留个只读口给外面：create.ts 要拿它算「滚到哪」。 */
  get editing(): EditOptions | null { return this.keys.editing }
  /** 键盘高度（会话持有，`onKeyboardHeightChange` 给的）。visibleBottom / 焦点框都靠它。 */
  get kbHeight(): number { return this.keys.kbHeight }
  /** 当帧画出来的激活字段（屏幕坐标），由 widgets.field 填；用来把它滚进可视区 */
  focusRect: { y: number; h: number } | null = null;
  /** 底部 Tab 栏由外面画（main.ts），这里只存回调 */
  drawTabs: ((ctx: Ctx, active: string) => void) | null = null;

  constructor() {
    const sys = wx.getSystemInfoSync();
    this.W = sys.windowWidth;
    this.H = sys.windowHeight;
    this.dpr = sys.pixelRatio;
    const safe = sys.safeArea || { top: sys.statusBarHeight || 20, bottom: this.H };
    this.safeTop = safe.top || sys.statusBarHeight || 20;
    this.safeBottom = Math.max(0, this.H - (safe.bottom || this.H));
    let m: any = null;
    try { m = wx.getMenuButtonBoundingClientRect(); } catch { /* 老基础库 */ }
    this.menu = m && m.width ? m
      : { top: this.safeTop + 4, bottom: this.safeTop + 36, left: this.W - 95, right: this.W - 7, height: 32, width: 88 };
    this.navY = (this.menu.top + this.menu.bottom) / 2;

    this.canvas = wx.createCanvas();
    this.canvas.width = this.W * this.dpr;
    this.canvas.height = this.H * this.dpr;
    this.ctx = this.canvas.getContext("2d");

    this.bindTouches();
    const loop = () => { this.frame(); requestAnimationFrame(loop); };
    requestAnimationFrame(loop);
  }

  /** 内容区顶部：胶囊下方。导航栏和胶囊在同一行。 */
  get contentTop(): number { return this.menu.bottom + 8; }

  /** 可视区底边：键盘弹起时 = 屏高 − 键盘高 − 输入条高（适配稿 2.2）。 */
  get visibleBottom(): number { return this.kbHeight ? this.H - this.kbHeight - INPUT_BAR_H : this.H; }

  get top(): Scene { return this.stack[this.stack.length - 1]; }

  push(scene: Scene) {
    const from = this.top;
    this.stack.push(scene);
    if (from) this.trans = { from, to: scene, dir: 1, t0: Date.now() };
    scene.onEnter?.();
    this.redraw();
  }

  pop() {
    if (this.stack.length < 2) return;
    this.closeAllSheets();
    const from = this.stack.pop()!;
    from.onLeave?.();
    this.trans = { from, to: this.top, dir: -1, t0: Date.now() };
    this.top.onResume?.();
    this.redraw();
  }

  /** 换掉整个栈（切 Tab、深链进入）。 */
  reset(scenes: Scene[]) {
    this.closeAllSheets();
    for (const s of this.stack) if (!scenes.includes(s)) s.onLeave?.();
    this.stack = scenes;
    this.trans = null;
    for (const s of scenes) s.onEnter?.();
    this.redraw();
  }

  redraw() { this.dirty = true; }
  /** 立刻整屏重画（借主画布导出图片之后，要在同一个 tick 里把页面画回来）。 */
  repaint() { this.draw(Date.now()); }

  /** 返回取消函数：场景离开时停掉还没跑完的动画（取消后 done 不再调用）。 */
  animate(dur: number, onFrame: (p: number) => void, done?: () => void): () => void {
    const a: Anim = { t0: Date.now(), dur, onFrame, done };
    this.anims.push(a);
    this.redraw();
    return () => { this.anims = this.anims.filter((x) => x !== a); };
  }

  toast(msg: string, ms = 2000) {
    this.toastMsg = { msg, until: Date.now() + ms };
    this.redraw();
  }

  openSheet(sheet: Sheet) {
    this.sheets.push(Object.assign(sheet, { t0: Date.now(), h: 240, top: this.H, scroll: new Scroller(), focusFor: "" }));
    this.redraw();
  }
  closeSheet(sheet?: Sheet) {
    const s = sheet ? this.sheets.find((x) => x === sheet) : this.sheets[this.sheets.length - 1];
    if (!s) return;
    if (this.editing) this.blurKeyboard();
    this.sheets = this.sheets.filter((x) => x !== s);
    s.onClose?.();
    this.redraw();
  }
  closeAllSheets() {
    while (this.sheets.length) this.closeSheet();
  }
  hasSheet(sheet: Sheet) { return this.sheets.includes(sheet as any); }

  hit(id: string, x: number, y: number, w: number, h: number, onTap: (() => void) | null) {
    this.hits.push({ id, x, y, w, h, onTap });
  }
  isPressed(id: string) { return this.pressed === id; }

  // ── 键盘：都在 core/keyboard.ts 的 KeyboardSession 里 ──
  // 这里只留薄薄一层，供场景调用。事件顺序、收起再拉、超时兜底都在那边（带假 wx 的单测）。
  edit(opts: EditOptions) { this.keys.focus(opts); }

  isEditing(key: string) { return this.keys.isEditing(key); }

  /** 收起键盘并作废会话状态（离开页面、关弹层）。**别直接调 wx.hideKeyboard** —— 那样会话不知道。 */
  blurKeyboard() { this.keys.blur(); }

  private pick(x: number, y: number): Hit | null {
    for (let i = this.hits.length - 1; i >= 0; i--) {
      const h = this.hits[i];
      if (x >= h.x && x <= h.x + h.w && y >= h.y && y <= h.y + h.h) return h;
    }
    return null;
  }

  private bindTouches() {
    wx.onTouchStart((e: any) => {
      const t = e.touches[0];
      if (!t || this.trans) return;
      const x = t.clientX, y = t.clientY;
      const h = this.pick(x, y);
      const sheet = this.sheets[this.sheets.length - 1];
      let scr = null;
      if (sheet) {
        scr = sheet.scrollerAt?.(x, y) ?? null;
        // 弹层放不下时整体内部滚动
        if (!scr && !sheet.dialog && sheet.scroll.max > 0 && y >= sheet.top) scr = { s: sheet.scroll, axis: "y" as const };
      }
      else {
        const captured = this.top.onTouchStart?.(x, y, h);
        if (!captured) scr = this.top.scrollerAt?.(x, y) ?? null;
      }
      this.touch = { x0: x, y0: y, x, y, hit: h, scr, moved: false, t0: Date.now() };
      if (scr) scr.s.start();
      this.pressed = h ? h.id : null;
      this.redraw();
    });
    wx.onTouchMove((e: any) => {
      const t = e.touches[0], tc = this.touch;
      if (!t || !tc) return;
      const x = t.clientX, y = t.clientY;
      const dx = x - tc.x, dy = y - tc.y;
      tc.x = x; tc.y = y;
      if (!tc.moved && Math.hypot(x - tc.x0, y - tc.y0) > TAP_SLOP) { tc.moved = true; this.pressed = null; }
      if (tc.scr && tc.moved) tc.scr.s.move(tc.scr.axis === "x" ? dx : dy);
      if (!this.sheets.length) this.top.onTouchMove?.(x, y, tc);
      this.redraw();
    });
    const end = () => {
      const tc = this.touch;
      this.touch = null;
      this.pressed = null;
      if (!tc) return;
      if (tc.scr) tc.scr.s.end();
      if (!this.sheets.length) this.top.onTouchEnd?.(tc);
      if (!tc.moved && tc.hit && tc.hit.onTap) tc.hit.onTap();
      this.redraw();
    };
    wx.onTouchEnd(end);
    wx.onTouchCancel(end);
  }

  private frame() {
    const now = Date.now();
    let busy = false;
    for (const s of this.stack) if (s.step && s.step()) busy = true;
    for (const s of this.sheets) {
      if (s.step && s.step()) busy = true;
      if (s.scroll.step()) busy = true;
    }
    if (this.anims.length) {
      busy = true;
      this.anims = this.anims.filter((a) => {
        const p = Math.min(1, (now - a.t0) / a.dur);
        a.onFrame(p);
        if (p >= 1) { a.done?.(); return false; }
        return true;
      });
    }
    if (this.trans || this.sheets.some((s) => now - s.t0 < 300)) busy = true;
    if (this.toastMsg) { busy = true; if (now > this.toastMsg.until) this.toastMsg = null; }
    if (this.editing) busy = true; // 光标闪烁
    if (!busy && !this.dirty) return;
    this.dirty = false;
    this.draw(now);
  }

  private drawScene(ctx: Ctx, s: Scene) {
    s.draw(ctx);
    if (s.tab && this.drawTabs) this.drawTabs(ctx, s.tab);
  }

  private draw(now: number) {
    const ctx = this.ctx;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    this.hits = [];
    this.focusRect = null;
    if (this.trans) {
      const p = ease.out(Math.min(1, (now - this.trans.t0) / 280));
      const { from, to, dir } = this.trans;
      const under = dir > 0 ? from : to, over = dir > 0 ? to : from;
      const k = dir > 0 ? p : 1 - p;
      ctx.save(); ctx.translate(-this.W * 0.3 * k, 0); this.drawScene(ctx, under); ctx.restore();
      ctx.fillStyle = `rgba(28,20,17,${0.15 * k})`; ctx.fillRect(0, 0, this.W, this.H);
      ctx.save(); ctx.translate(this.W * (1 - k), 0); this.drawScene(ctx, over); ctx.restore();
      this.hits = [];
      if (p >= 1) { this.trans = null; this.redraw(); }
    } else if (this.top) {
      this.drawScene(ctx, this.top);
    }
    this.sheets.forEach((s, i) => this.drawSheet(ctx, s, now, i === this.sheets.length - 1));
    if (this.toastMsg) this.drawToast(ctx);
  }

  private drawSheet(ctx: Ctx, s: OpenSheet, now: number, topmost: boolean) {
    const p = ease.out(Math.min(1, (now - s.t0) / 280));
    ctx.fillStyle = `rgba(28,20,17,${0.45 * p})`;
    ctx.fillRect(0, 0, this.W, this.H);
    this.hits = []; // 只响应最上层弹层
    this.hit("scrim", 0, 0, this.W, this.H, s.modal ? null : () => this.closeSheet(s));
    if (s.dialog) {
      const w = Math.min(this.W - 48, 340);
      const x = (this.W - w) / 2;
      // 键盘弹起时在「状态栏底 ~ 可视底边」之间居中（适配稿 2.3）
      const y = this.kbHeight
        ? Math.max(this.safeTop + 8, this.safeTop + (this.visibleBottom - this.safeTop - s.h) / 2)
        : Math.max(this.safeTop + 20, (this.H - s.h) / 2);
      ctx.save();
      ctx.globalAlpha = p;
      fillRR(ctx, x, y, w, s.h, 22, C.card);
      this.hit("sheet-body", x, y, w, s.h, null);
      ctx.translate(x, 0);
      const prevH = s.h;
      s.h = s.draw(ctx, y, w);
      if (s.h !== prevH) this.redraw();
      ctx.restore();
      this.shiftHits(x);
    } else {
      // 底边贴可视区底（键盘弹起时是输入条顶边）；顶边不高于 contentTop，放不下就内部滚动（适配稿 2.3）
      const bottom = this.visibleBottom;
      const full = s.h + (this.kbHeight ? 0 : this.safeBottom);
      const viewH = Math.min(full, bottom - this.contentTop);
      const y = bottom - viewH * p;
      s.top = y;
      s.scroll.setMax(full - viewH);
      fillRR(ctx, 0, y, this.W, viewH + 40, 26, C.card);
      this.hit("sheet-body", 0, y, this.W, viewH + 40, null);
      const before = this.hits.length;
      ctx.save();
      ctx.beginPath(); ctx.rect(0, y, this.W, bottom - y); ctx.clip();
      const prevH = s.h;
      s.h = s.draw(ctx, y - s.scroll.pos, this.W);
      ctx.restore();
      if (s.h !== prevH) this.redraw(); // 高度变了（比如编辑地点时胶囊收起），下一帧按新高度排
      // 滚出可视区的热区裁掉
      const visTop = y, visBottom = bottom;
      this.hits = this.hits.filter((h, i) => {
        if (i < before) return true;
        const t = Math.max(h.y, visTop), b = Math.min(h.y + h.h, visBottom);
        if (b <= t) return false;
        h.y = t; h.h = b - t;
        return true;
      });
      if (s.scroll.max > 0) fillRR(ctx, 0, y, this.W, 18, 0, C.card); // 滚动时把手下面不透出内容
      fillRR(ctx, this.W / 2 - 18, y + 8, 36, 4, 2, C.line2);
      this.keepFocusVisible(s, y, bottom);
    }
    if (!topmost) this.hits = [];
  }

  /** 弹层里正在输入的字段要完整可见：键盘弹起或换字段时滚一次，之后不和手指抢。 */
  private keepFocusVisible(s: OpenSheet, top: number, bottom: number) {
    const f = this.focusRect, key = this.editing && this.kbHeight ? `${this.editing.key}@${this.kbHeight}` : "";
    if (!key || !f || s.focusFor === key) { if (!key) s.focusFor = ""; return; }
    s.focusFor = key;
    let d = 0;
    if (f.y + f.h > bottom - 16) d = f.y + f.h - (bottom - 16);
    else if (f.y < top + 24) d = f.y - (top + 24);
    if (d) { s.scroll.pos = Math.min(Math.max(0, s.scroll.pos + d), s.scroll.max); this.redraw(); }
  }

  /** 对话框里用了 translate，热区要换回屏幕坐标。 */
  private shiftHits(dx: number) {
    for (const h of this.hits) if (h.id !== "scrim" && h.id !== "sheet-body") h.x += dx;
  }

  private drawToast(ctx: Ctx) {
    const msg = this.toastMsg!.msg;
    const f = font(14, "600");
    ctx.font = f;
    const w = Math.min(this.W - 48, ctx.measureText(msg).width + 32);
    const lines = wrap(ctx, msg, w - 28, f).slice(0, 3);
    if (lines.length === 3) lines[2] = ellipsize(ctx, lines[2], w - 28, f);
    const h = 18 + lines.length * 21;
    const x = (this.W - w) / 2, y = this.H * 0.42;
    fillRR(ctx, x, y, w, h, lines.length > 1 ? 16 : 20, "rgba(42,28,23,0.9)");
    lines.forEach((ln, i) => text(ctx, ln, this.W / 2, y + 19.5 + i * 21, f, C.cream, "center"));
  }
}
