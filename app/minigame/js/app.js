// 画布运行时的骨架：场景栈、触摸分发（点按 / 拖动）、重绘循环、弹层、toast、键盘。
// 小游戏没有 DOM，所有「按钮」都是每帧登记的矩形热区。

const { C, font, fillRR, text, ease } = require('./ui');

const TAP_SLOP = 8;

class App {
  constructor() {
    const sys = wx.getSystemInfoSync();
    this.sys = sys;
    this.W = sys.windowWidth;
    this.H = sys.windowHeight;
    this.dpr = sys.pixelRatio;
    const safe = sys.safeArea || { top: sys.statusBarHeight || 20, bottom: this.H };
    this.safeTop = safe.top || sys.statusBarHeight || 20;
    this.safeBottom = this.H - (safe.bottom || this.H);
    // 右上角胶囊（「···」和关闭）。小游戏里它盖在画布上，界面必须让开
    let menu = null;
    try { menu = wx.getMenuButtonBoundingClientRect(); } catch (e) { /* 老基础库 */ }
    this.menu = menu && menu.width ? menu
      : { top: this.safeTop + 4, bottom: this.safeTop + 36, left: this.W - 95, right: this.W - 7, height: 32, width: 88 };
    this.navY = (this.menu.top + this.menu.bottom) / 2; // 导航栏标题和胶囊垂直居中对齐

    this.canvas = wx.createCanvas();
    this.canvas.width = this.W * this.dpr;
    this.canvas.height = this.H * this.dpr;
    this.ctx = this.canvas.getContext('2d');

    this.stack = [];
    this.trans = null;   // 场景切换动画
    this.sheet = null;   // 底部弹层
    this.toastMsg = null;
    this.anims = [];
    this.hits = [];
    this.pressed = null;
    this.touch = null;
    this.dirty = true;
    this.kbHeight = 0;
    this.editing = null;
    this.me = '';        // 称呼；正式版来自 /api/me

    this.bindTouches();
    this.bindKeyboard();
    const loop = () => { this.frame(); requestAnimationFrame(loop); };
    requestAnimationFrame(loop);
  }

  get top() { return this.stack[this.stack.length - 1]; }

  push(scene) {
    const from = this.top;
    this.stack.push(scene);
    if (from) this.trans = { from, to: scene, dir: 1, t0: Date.now() };
    this.redraw();
  }

  pop() {
    if (this.stack.length < 2) return;
    const from = this.stack.pop();
    this.trans = { from, to: this.top, dir: -1, t0: Date.now() };
    if (this.top.onResume) this.top.onResume();
    this.redraw();
  }

  replaceRoot(scene) { this.stack = [scene]; this.redraw(); }

  redraw() { this.dirty = true; }

  // 动画：onFrame(进度 0..1)，结束后调 done
  animate(dur, onFrame, done) {
    this.anims.push({ t0: Date.now(), dur, onFrame, done });
    this.redraw();
  }

  toast(msg, ms) {
    this.toastMsg = { msg, until: Date.now() + (ms || 1800) };
    this.redraw();
  }

  openSheet(sheet) { this.sheet = sheet; sheet.t0 = Date.now(); this.redraw(); }
  closeSheet() { if (this.sheet && this.sheet.onClose) this.sheet.onClose(); this.sheet = null; this.redraw(); }

  // 场景在 draw 里登记热区；pressed 用来画按下态
  hit(id, x, y, w, h, onTap) { this.hits.push({ id, x, y, w, h, onTap }); }
  isPressed(id) { return this.pressed === id; }

  // ── 键盘：小游戏没有输入框，只能拉起微信原生键盘，文字从回调里拿 ──
  edit(opts) {
    this.editing = opts;
    wx.showKeyboard({
      defaultValue: opts.value || '',
      maxLength: opts.maxLength || 40,
      multiple: !!opts.multiple,
      confirmHold: false,
      confirmType: opts.confirmType || 'done',
      fail: e => this.toast('键盘没拉起来：' + (e && e.errMsg)),
    });
    this.redraw();
  }

  bindKeyboard() {
    wx.onKeyboardInput(res => { if (this.editing) { this.editing.onInput(res.value); this.redraw(); } });
    wx.onKeyboardConfirm(res => {
      const ed = this.editing;
      if (ed) { ed.onInput(res.value); if (ed.onConfirm) ed.onConfirm(res.value); }
      wx.hideKeyboard({});
    });
    wx.onKeyboardComplete(res => {
      const ed = this.editing;
      this.editing = null;
      this.kbHeight = 0;
      if (ed) { ed.onInput(res.value); if (ed.onDone) ed.onDone(res.value); }
      this.redraw();
    });
    if (wx.onKeyboardHeightChange) {
      wx.onKeyboardHeightChange(res => { this.kbHeight = res.height || 0; this.redraw(); });
    }
  }

  // ── 触摸：位移小于 8px 且在 touchend 结束的才算点按 ──
  // onTap 在 touchend 回调里同步执行，订阅消息、录音授权这类接口需要这一点
  bindTouches() {
    const pick = (x, y) => {
      for (let i = this.hits.length - 1; i >= 0; i--) {
        const h = this.hits[i];
        if (x >= h.x && x <= h.x + h.w && y >= h.y && y <= h.y + h.h) return h;
      }
      return null;
    };
    wx.onTouchStart(e => {
      const t = e.touches[0];
      if (!t || this.trans) return;
      const x = t.clientX, y = t.clientY;
      const h = pick(x, y);
      const scene = this.top;
      // 场景返回 true 表示自己接管这次触摸（比如按住说话），不再滚动
      const captured = !this.sheet && scene.onTouchStart ? scene.onTouchStart(x, y, h) : false;
      const scr = !captured && !this.sheet && scene.scrollerAt ? scene.scrollerAt(x, y) : null;
      this.touch = { x0: x, y0: y, x, y, hit: h, scr, moved: false, t0: Date.now() };
      if (scr) scr.s.start();
      this.pressed = h ? h.id : null;
      this.redraw();
    });
    wx.onTouchMove(e => {
      const t = e.touches[0], tc = this.touch;
      if (!t || !tc) return;
      const x = t.clientX, y = t.clientY;
      const dx = x - tc.x, dy = y - tc.y;
      tc.x = x; tc.y = y;
      if (!tc.moved && Math.hypot(x - tc.x0, y - tc.y0) > TAP_SLOP) { tc.moved = true; this.pressed = null; }
      if (tc.scr && tc.moved) tc.scr.s.move(tc.scr.axis === 'x' ? dx : dy);
      if (this.top.onTouchMove && !this.sheet) this.top.onTouchMove(x, y, tc);
      this.redraw();
    });
    const end = e => {
      const tc = this.touch;
      this.touch = null;
      this.pressed = null;
      if (!tc) return;
      if (tc.scr) tc.scr.s.end();
      if (this.top.onTouchEnd && !this.sheet) this.top.onTouchEnd(tc);
      if (!tc.moved && tc.hit && tc.hit.onTap) tc.hit.onTap(tc.x, tc.y);
      this.redraw();
    };
    wx.onTouchEnd(end);
    wx.onTouchCancel(end);
  }

  frame() {
    const now = Date.now();
    let busy = false;
    for (const s of this.stack) if (s.step && s.step()) busy = true;
    if (this.anims.length) {
      busy = true;
      this.anims = this.anims.filter(a => {
        const p = Math.min(1, (now - a.t0) / a.dur);
        a.onFrame(p);
        if (p >= 1) { if (a.done) a.done(); return false; }
        return true;
      });
    }
    if (this.trans || (this.sheet && now - this.sheet.t0 < 300)) busy = true;
    if (this.toastMsg) { busy = true; if (now > this.toastMsg.until) this.toastMsg = null; }
    if (this.editing) busy = true; // 光标闪烁
    if (!busy && !this.dirty) return;
    this.dirty = false;
    this.draw(now);
  }

  draw(now) {
    const ctx = this.ctx;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    this.hits = [];
    if (this.trans) {
      const p = ease.out(Math.min(1, (now - this.trans.t0) / 280));
      const { from, to, dir } = this.trans;
      const under = dir > 0 ? from : to, over = dir > 0 ? to : from;
      const k = dir > 0 ? p : 1 - p;
      ctx.save(); ctx.translate(-this.W * 0.3 * k, 0); under.draw(ctx); ctx.restore();
      ctx.fillStyle = `rgba(28,20,17,${0.15 * k})`; ctx.fillRect(0, 0, this.W, this.H);
      ctx.save(); ctx.translate(this.W * (1 - k), 0); over.draw(ctx); ctx.restore();
      this.hits = [];
      if (p >= 1) { this.trans = null; this.redraw(); }
    } else {
      this.top.draw(ctx);
    }
    if (this.sheet) this.drawSheet(ctx, now);
    if (this.toastMsg) this.drawToast(ctx);
  }

  drawSheet(ctx, now) {
    const p = ease.out(Math.min(1, (now - this.sheet.t0) / 280));
    ctx.fillStyle = `rgba(28,20,17,${0.45 * p})`;
    ctx.fillRect(0, 0, this.W, this.H);
    this.hits = []; // 弹层打开时只响应弹层里的热区
    this.hit('scrim', 0, 0, this.W, this.H, () => this.closeSheet());
    const h = this.sheet.height + this.safeBottom;
    // 键盘弹起时把弹层顶上去
    const y = this.H - h * p - this.kbHeight;
    fillRR(ctx, 0, y, this.W, h + 30, 26, C.card);
    this.hit('sheet-body', 0, y, this.W, h + 30, null);
    this.sheet.draw(ctx, y);
  }

  drawToast(ctx) {
    ctx.font = font(14, '600');
    const w = Math.min(this.W - 48, ctx.measureText(this.toastMsg.msg).width + 32);
    const x = (this.W - w) / 2, y = this.H * 0.42;
    fillRR(ctx, x, y, w, 40, 20, 'rgba(42,28,23,0.88)');
    text(ctx, require('./ui').ellipsize(ctx, this.toastMsg.msg, w - 24), this.W / 2, y + 20, font(14, '600'), C.cream, 'center');
  }
}

module.exports = App;
