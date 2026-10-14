// 画布运行时：场景栈、触摸分发（点按 / 拖动）、重绘循环、底部弹层、居中对话框、toast、键盘。
// 小游戏没有 DOM，所有「按钮」都是每帧登记的矩形热区；onTap 在 touchend 回调里同步执行，
// 订阅消息、录音授权这类必须由用户点按触发的接口靠这一点。

import { C, Ctx, Scroller, ease, ellipsize, fillRR, font, text, wrap } from "./ui";

const TAP_SLOP = 8;

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

export interface Touch0 {
  x0: number; y0: number; x: number; y: number;
  hit: Hit | null;
  scr: { s: Scroller; axis: "x" | "y" } | null;
  moved: boolean;
  t0: number;
}

export interface EditOptions {
  key: string;
  value: string;
  maxLength?: number;
  multiple?: boolean;
  confirmType?: "done" | "next" | "search" | "go" | "send";
  onInput: (v: string) => void;
  onConfirm?: (v: string) => void;
  onDone?: (v: string) => void;
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
  sheets: (Sheet & { t0: number; h: number })[] = [];
  private toastMsg: { msg: string; until: number } | null = null;
  private anims: Anim[] = [];
  hits: Hit[] = [];
  pressed: string | null = null;
  private touch: Touch0 | null = null;
  private dirty = true;
  kbHeight = 0;
  editing: EditOptions | null = null;
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
    this.bindKeyboard();
    const loop = () => { this.frame(); requestAnimationFrame(loop); };
    requestAnimationFrame(loop);
  }

  /** 内容区顶部：胶囊下方。导航栏和胶囊在同一行。 */
  get contentTop(): number { return this.menu.bottom + 8; }

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

  animate(dur: number, onFrame: (p: number) => void, done?: () => void) {
    this.anims.push({ t0: Date.now(), dur, onFrame, done });
    this.redraw();
  }

  toast(msg: string, ms = 2000) {
    this.toastMsg = { msg, until: Date.now() + ms };
    this.redraw();
  }

  openSheet(sheet: Sheet) {
    this.sheets.push(Object.assign(sheet, { t0: Date.now(), h: 240 }));
    this.redraw();
  }
  closeSheet(sheet?: Sheet) {
    const s = sheet ? this.sheets.find((x) => x === sheet) : this.sheets[this.sheets.length - 1];
    if (!s) return;
    if (this.editing) wx.hideKeyboard({});
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

  // ── 键盘：拉起微信原生输入条，文字从回调里拿 ──
  edit(opts: EditOptions) {
    this.editing = opts;
    wx.showKeyboard({
      defaultValue: opts.value || "",
      maxLength: opts.maxLength || 40,
      multiple: !!opts.multiple,
      confirmHold: false,
      confirmType: opts.confirmType || "done",
      fail: (e: any) => this.toast("键盘没拉起来：" + (e && e.errMsg)),
    });
    this.redraw();
  }
  isEditing(key: string) { return !!this.editing && this.editing.key === key; }

  private bindKeyboard() {
    wx.onKeyboardInput((res: any) => { if (this.editing) { this.editing.onInput(res.value); this.redraw(); } });
    wx.onKeyboardConfirm((res: any) => {
      const ed = this.editing;
      if (ed) { ed.onInput(res.value); ed.onConfirm?.(res.value); }
      if (this.editing === ed) wx.hideKeyboard({});
    });
    wx.onKeyboardComplete((res: any) => {
      const ed = this.editing;
      this.editing = null;
      this.kbHeight = 0;
      if (ed) { ed.onInput(res.value); ed.onDone?.(res.value); }
      this.redraw();
    });
    if (wx.onKeyboardHeightChange) wx.onKeyboardHeightChange((res: any) => { this.kbHeight = res.height || 0; this.redraw(); });
  }

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
      if (sheet) scr = sheet.scrollerAt?.(x, y) ?? null;
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
    for (const s of this.sheets) if (s.step && s.step()) busy = true;
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

  private drawSheet(ctx: Ctx, s: Sheet & { t0: number; h: number }, now: number, topmost: boolean) {
    const p = ease.out(Math.min(1, (now - s.t0) / 280));
    ctx.fillStyle = `rgba(28,20,17,${0.45 * p})`;
    ctx.fillRect(0, 0, this.W, this.H);
    this.hits = []; // 只响应最上层弹层
    this.hit("scrim", 0, 0, this.W, this.H, s.modal ? null : () => this.closeSheet(s));
    if (s.dialog) {
      const w = Math.min(this.W - 48, 340);
      const x = (this.W - w) / 2;
      const y = Math.max(this.safeTop + 20, (this.H - s.h) / 2 - this.kbHeight / 2);
      ctx.save();
      ctx.globalAlpha = p;
      fillRR(ctx, x, y, w, s.h, 22, C.card);
      this.hit("sheet-body", x, y, w, s.h, null);
      ctx.translate(x, 0);
      s.h = s.draw(ctx, y, w);
      ctx.restore();
      this.shiftHits(x);
    } else {
      const total = s.h + this.safeBottom;
      const y = this.H - total * p - this.kbHeight;
      fillRR(ctx, 0, y, this.W, total + 40, 26, C.card);
      fillRR(ctx, this.W / 2 - 18, y + 8, 36, 4, 2, C.line2);
      this.hit("sheet-body", 0, y, this.W, total + 40, null);
      s.h = s.draw(ctx, y, this.W);
    }
    if (!topmost) this.hits = [];
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
