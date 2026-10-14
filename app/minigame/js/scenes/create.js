// 开一局（对应设计稿 X1，加上一期开局表单的字段）
// 文字输入全部走 wx.showKeyboard：点字段 → 屏幕底部弹出微信原生输入条，画布上同步显示。
// 「按住说话」验证 #122 的录音链路（只录不传）。

const { C, font, fillRR, strokeRR, text, wrap, circle, Scroller } = require('../ui');
const data = require('../data');

const BAR_H = 76;
const DAYS = [['今天', '10/14', '周三'], ['明天', '10/15', '周四'], ['后天', '10/16', '周五'], ['周六', '10/17', '周六'], ['周日', '10/18', '周日']];
const TIMES = ['12:00', '14:00', '18:30', '19:00', '19:30', '20:00', '20:30'];

class Create {
  constructor(app, open) {
    this.app = app;
    this.open = open;
    this.scroll = new Scroller();
    this.f = { day: 0, time: '19:30', area: '', kind: data.KINDS[0], place: '', note: '' };
    this.err = {};
    this.fieldY = {};
    this.rec = null; // { t0, cancel } 录音中
    this.recorder = null;
  }

  scrollerAt(x, y) {
    const a = this.app;
    return y > a.menu.bottom + 8 && y < a.H - BAR_H - a.safeBottom ? { s: this.scroll, axis: 'y' } : null;
  }
  step() { return this.scroll.step() || !!this.rec; }

  draw(ctx) {
    const a = this.app, W = a.W;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);
    const top = a.menu.bottom + 8;
    // 键盘弹起时底部操作栏被盖住，可视区缩短
    const bottom = a.H - (a.kbHeight ? a.kbHeight : BAR_H + a.safeBottom);
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 8 - this.scroll.pos;

    y = this.drawVoice(ctx, y);
    y = this.label(ctx, '哪天', y);
    y = this.chips(ctx, y, DAYS.map(d => d[0]), i => this.f.day === i, i => { this.f.day = i; });
    y = this.label(ctx, '几点（雅加达时间）', y);
    y = this.chips(ctx, y, TIMES, i => this.f.time === TIMES[i], i => { this.f.time = TIMES[i]; });
    y = this.label(ctx, '商圈', y, this.err.area);
    y = this.chips(ctx, y, data.AREAS, i => this.f.area === data.AREAS[i], i => { this.f.area = data.AREAS[i]; this.err.area = ''; });
    y = this.label(ctx, '地点', y, null);
    y = this.input(ctx, y, 'place', '比如：Pacific Place · 鼎泰丰包间', false);
    y = this.label(ctx, '玩法', y);
    y = this.chips(ctx, y, data.KINDS, i => this.f.kind === data.KINDS[i], i => { this.f.kind = data.KINDS[i]; });
    y = this.label(ctx, '备注', y, null, '选填');
    y = this.input(ctx, y, 'note', '比如：打到 A 为止，新手友好', true);
    text(ctx, '不涉及任何金钱输赢 · 纯娱乐约局', W / 2, y + 16, font(11), C.ink4, 'center');
    y += 48;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);

    this.drawNav(ctx);
    if (!a.kbHeight) this.drawBar(ctx);
    if (this.rec) this.drawRecording(ctx);
  }

  drawNav(ctx) {
    const a = this.app;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, a.W, a.menu.bottom + 8);
    // 设计稿把「取消」放在右上角，小游戏里那里是胶囊，只能挪到左边
    text(ctx, '取消', 18, a.navY, font(15, '600'), a.isPressed('cancel') ? C.ink : C.ink3);
    a.hit('cancel', 4, a.navY - 22, 64, 44, () => { wx.hideKeyboard({}); a.pop(); });
    text(ctx, '开一局', a.W / 2, a.navY, font(17, '700'), C.ink, 'center');
  }

  label(ctx, s, y, err, hint) {
    const W = this.app.W;
    text(ctx, s, 16, y + 22, font(14, '700'), C.ink);
    if (err) text(ctx, err, W - 16, y + 22, font(12), C.red, 'right');
    else if (hint) text(ctx, hint, W - 16, y + 22, font(12), C.ink4, 'right');
    return y + 38;
  }

  // 自动换行的胶囊组
  chips(ctx, y, items, isOn, onPick) {
    const a = this.app, maxX = a.W - 16;
    let x = 16;
    ctx.font = font(14);
    items.forEach((it, i) => {
      const w = ctx.measureText(it).width + 28;
      if (x + w > maxX) { x = 16; y += 44; }
      const on = isOn(i), id = 'chip:' + y + ':' + it, pr = a.isPressed(id);
      fillRR(ctx, x, y, w, 36, 18, on ? C.red : pr ? C.beige : C.card);
      if (!on) strokeRR(ctx, x + 0.5, y + 0.5, w - 1, 35, 18, C.line, 1);
      text(ctx, it, x + w / 2, y + 18, font(14, on ? '600' : 'normal'), on ? C.cream : C.ink2, 'center');
      a.hit(id, x, y, w, 36, () => onPick(i));
      x += w + 8;
    });
    return y + 36 + 10;
  }

  input(ctx, y, key, placeholder, multiple) {
    const a = this.app, x = 16, w = a.W - 32;
    const v = this.f[key];
    const focused = a.editing && a.editing.key === key;
    ctx.font = font(16);
    const lines = v ? wrap(ctx, v, w - 32) : [placeholder];
    const h = multiple ? Math.max(84, 28 + lines.length * 24) : 52;
    const bad = this.err[key];
    fillRR(ctx, x, y, w, h, 12, bad ? '#fbefed' : C.card);
    strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 12, bad || focused ? C.red : C.line, focused ? 1.5 : 1);
    const ty = multiple ? y + 26 : y + h / 2;
    const shown = multiple ? lines : [lines[lines.length - 1]];
    shown.forEach((ln, i) => text(ctx, ln, x + 16, ty + i * 24, font(16), v ? C.ink : C.ink4));
    if (focused && Date.now() % 1000 < 550) {
      const last = v ? shown[shown.length - 1] : '';
      const cx = x + 16 + ctx.measureText(last).width + 1;
      const cy = ty + (shown.length - 1) * 24;
      ctx.fillStyle = C.red; ctx.fillRect(cx, cy - 11, 1.5, 22);
    }
    this.fieldY[key] = y + this.scroll.pos;
    a.hit('input:' + key, x, y, w, h, () => this.edit(key, multiple));
    let ny = y + h;
    if (bad) { text(ctx, '⚠ ' + bad, x, ny + 16, font(12), C.red); ny += 26; }
    return ny + 8;
  }

  edit(key, multiple) {
    const a = this.app;
    a.edit({
      key, value: this.f[key], multiple, maxLength: key === 'note' ? 200 : 60,
      confirmType: multiple ? 'done' : 'next',
      onInput: v => { this.f[key] = v; this.err[key] = ''; this.check(key); },
      onConfirm: () => { if (key === 'place') setTimeout(() => this.edit('note', true), 50); },
    });
    // 拿得到键盘高度时，把字段滚到键盘上方
    setTimeout(() => {
      const top = a.menu.bottom + 8;
      const target = this.fieldY[key] - top - 60;
      if (target > this.scroll.pos || a.kbHeight) this.scroll.scrollTo(target);
      a.redraw();
    }, 250);
  }

  check(key) {
    const hit = data.bannedWord(this.f[key]);
    if (hit) this.err[key] = '请勿涉及金钱：这里只约纯娱乐局';
    return !hit;
  }

  // ── 按住说话 ──
  drawVoice(ctx, y) {
    const a = this.app, x = 16, w = a.W - 32, h = 64;
    const on = !!this.rec;
    fillRR(ctx, x, y, w, h, 18, on ? C.redDeep : C.redTint);
    circle(ctx, x + 34, y + h / 2, 18, on ? C.red : C.card);
    text(ctx, '🎙', x + 34, y + h / 2 + 1, font(17), C.red, 'center');
    text(ctx, on ? '松手结束，上滑取消' : '按住说话，蛋蛋帮你填', x + 64, y + 24, font(15, '700'), on ? C.cream : C.red);
    text(ctx, '例：「周四晚上七点半 PIK，还差三个」', x + 64, y + 45, font(12), on ? 'rgba(255,247,236,0.8)' : C.ink3);
    this.voiceRect = { x, y, w, h };
    // 用热区占位，真正的按住逻辑在 onTouchStart / onTouchEnd
    a.hit('voice', x, y, w, h, null);
    return y + h + 12;
  }

  onTouchStart(x, y, hit) {
    if (hit && hit.id === 'voice') { this.startRec(); return true; }
    return false;
  }
  onTouchMove(x, y, tc) {
    if (this.rec) this.rec.cancel = tc.y0 - y > 60;
  }
  onTouchEnd() {
    if (this.rec) this.stopRec();
  }

  startRec() {
    const a = this.app;
    if (!this.recorder) {
      this.recorder = wx.getRecorderManager();
      this.recorder.onStop(res => {
        const r = this.lastRec || {};
        if (r.cancel) { a.toast('已取消'); return; }
        const sec = ((res.duration || 0) / 1000).toFixed(1);
        if ((res.duration || 0) < 600) { a.toast('说得太短了，再按住说一次'); return; }
        // 正式版：wx.uploadFile → POST /api/voice → 文字交给 /api/agent 出草稿
        a.toast(`录到 ${sec} 秒（${Math.round((res.fileSize || 0) / 1024)} KB），原型不上传`, 2600);
      });
      this.recorder.onError(e => { this.rec = null; a.toast('录音失败：' + (e.errMsg || '')); });
    }
    this.rec = { t0: Date.now(), cancel: false };
    this.recorder.start({ duration: 15000, sampleRate: 16000, numberOfChannels: 1, encodeBitRate: 48000, format: 'mp3' });
    wx.vibrateShort && wx.vibrateShort({ type: 'medium' });
  }

  stopRec() {
    this.lastRec = this.rec;
    this.rec = null;
    this.recorder.stop();
    this.app.redraw();
  }

  drawRecording(ctx) {
    const a = this.app, W = a.W, cy = a.H * 0.45;
    ctx.fillStyle = C.scrim;
    ctx.fillRect(0, 0, W, a.H);
    const t = (Date.now() - this.rec.t0) / 1000;
    const cancel = this.rec.cancel;
    fillRR(ctx, W / 2 - 110, cy - 90, 220, 180, 22, cancel ? C.ink2 : C.card);
    // 简单的声波
    for (let i = 0; i < 9; i++) {
      const hgt = 10 + 26 * Math.abs(Math.sin(t * 6 + i * 0.9));
      fillRR(ctx, W / 2 - 64 + i * 15, cy - 30 - hgt / 2, 8, hgt, 4, cancel ? C.khaki : C.red);
    }
    text(ctx, `${Math.max(0, 15 - t).toFixed(0)} 秒`, W / 2, cy + 22, font(22, '900', true), cancel ? C.cream : C.ink, 'center');
    text(ctx, cancel ? '松手取消' : '上滑取消', W / 2, cy + 56, font(13), cancel ? C.cream : C.ink3, 'center');
  }

  drawBar(ctx) {
    const a = this.app, W = a.W, y = a.H - BAR_H - a.safeBottom;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, y, W, BAR_H + a.safeBottom);
    const ok = this.f.area && this.f.place.trim() && !this.err.place && !this.err.note;
    const pr = a.isPressed('submit');
    fillRR(ctx, 16, y + 12, W - 32, 52, 26, ok ? (pr ? C.redDeep : C.red) : C.khaki);
    text(ctx, '发起并分享', W / 2, y + 38, font(17, '700'), C.cream, 'center');
    a.hit('submit', 16, y + 12, W - 32, 52, () => this.submit());
  }

  submit() {
    const a = this.app, f = this.f;
    if (!f.area) { this.err.area = '选一个商圈'; this.scroll.scrollTo(0); return; }
    if (!f.place.trim()) { this.edit('place', false); return; }
    if (!this.check('place') || !this.check('note')) { a.toast('请勿涉及金钱'); return; }
    if (!a.me) a.me = '我';
    const d = DAYS[f.day];
    const g = data.create({ day: d[0], date: d[1], week: d[2], time: f.time, area: f.area, kind: f.kind,
      place: f.place.trim(), note: f.note.trim() }, a.me);
    this.open.created(g.code);
  }
}

module.exports = Create;
