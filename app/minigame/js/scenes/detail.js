// 牌局详情（对应设计稿 B1 / B3 / W1）
// 报名按钮在 touchend 里同步调 wx.requestSubscribeMessage（#85 的限制），再落座。

const { C, font, fillRR, strokeRR, card, text, wrap, circle, Scroller, ease } = require('../ui');
const data = require('../data');

// 正式版从服务端拿；原型里是假的，调用会失败，但能验证「必须在点按里同步调」这条约束
const TMPL_IDS = ['TMPL_FULL_PLACEHOLDER', 'TMPL_T1H_PLACEHOLDER'];
const BAR_H = 76;

class Detail {
  constructor(app, code, open) {
    this.app = app;
    this.code = code;
    this.open = open;
    this.scroll = new Scroller();
    this.pop = {}; // 座位落座的回弹进度
  }

  get g() { return data.find(this.code); }

  scrollerAt(x, y) {
    const a = this.app;
    return y > a.menu.bottom + 8 && y < a.H - BAR_H - a.safeBottom ? { s: this.scroll, axis: 'y' } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx) {
    const a = this.app, W = a.W, g = this.g;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);

    const top = a.menu.bottom + 8, bottom = a.H - BAR_H - a.safeBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 12 - this.scroll.pos;

    text(ctx, `${g.day} ${g.date} ${g.week}`, W / 2, y + 10, font(14, '700'), C.red, 'center');
    text(ctx, g.time, W / 2, y + 50, font(44, '900', true), C.ink, 'center');
    text(ctx, `雅加达时间 WIB · ${g.kind}`, W / 2, y + 84, font(13), C.ink3, 'center');
    y += 116;

    y = this.drawTable(ctx, g, y);
    y = this.drawNames(ctx, g, y);
    y = this.drawInfo(ctx, g, y);
    text(ctx, '不涉及任何金钱输赢 · 纯娱乐约局', W / 2, y + 14, font(11), C.ink4, 'center');
    y += 40;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);

    this.drawNav(ctx);
    this.drawBar(ctx, g);
  }

  drawNav(ctx) {
    const a = this.app;
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, a.W, a.menu.bottom + 8);
    // 返回放左边；右上角是胶囊，设计稿里的分享图标挪到底部操作栏
    const pressed = a.isPressed('back');
    if (pressed) circle(ctx, 28, a.navY, 18, C.beige);
    text(ctx, '‹', 28, a.navY - 2, font(30, '300'), C.ink, 'center');
    a.hit('back', 4, a.navY - 22, 56, 44, () => a.pop());
    text(ctx, '牌局详情', a.W / 2, a.navY, font(17, '700'), C.ink, 'center');
  }

  drawTable(ctx, g, y) {
    const a = this.app, cx = a.W / 2, size = 196, x = cx - size / 2;
    // 牌桌：深红面 + 内描边 + 金色虚线
    fillRR(ctx, x, y + 34 + 6, size, size, 24, 'rgba(142,42,35,0.25)');
    fillRR(ctx, x, y + 34, size, size, 24, C.redDeep);
    strokeRR(ctx, x + 3, y + 37, size - 6, size - 6, 21, C.redInk, 6);
    strokeRR(ctx, x + 22, y + 56, size - 44, size - 44, 14, 'rgba(233,196,106,0.45)', 1, [4, 3]);
    const mid = y + 34 + size / 2;
    text(ctx, `${g.players.length}/4`, cx, mid - 6, font(40, '900', true), C.cream, 'center');
    text(ctx, '对家为搭档', cx, mid + 26, font(12), 'rgba(255,247,236,0.75)', 'center');

    // 座位顺序：上（局头）、右、下、左
    const pos = [[cx, y + 34], [x + size, mid], [cx, y + 34 + size], [x, mid]];
    const me = a.me;
    pos.forEach(([sx, sy], i) => {
      const p = g.players[i];
      const k = this.pop[i] != null ? this.pop[i] : 1;
      if (p) {
        const r = 32 * k;
        const isMe = me && p === me, host = i === 0;
        circle(ctx, sx, sy, r + 4, C.paper);
        circle(ctx, sx, sy, r, isMe ? C.gold : host ? C.redTint : C.beige, host ? '#e3b5ae' : '#e6dccd', 3);
        text(ctx, p.slice(0, 1), sx, sy + 1, font(26 * k, '900', true), host ? C.red : C.ink, 'center');
      } else {
        circle(ctx, sx, sy, 30, null, C.khaki, 2, [5, 4]);
        if (!g.players.includes(me) || !me) {
          text(ctx, '+', sx, sy, font(24, '300'), C.khaki, 'center');
          a.hit('seat:' + i, sx - 32, sy - 32, 64, 64, () => this.join());
        }
      }
    });
    return y + 34 + size + 50;
  }

  drawNames(ctx, g, y) {
    const a = this.app;
    const names = g.players.map((p, i) => (i === 0 ? `局头 · ${p}` : p) + (a.me && p === a.me ? '（我）' : ''));
    ctx.font = font(13);
    const ws = names.map(n => ctx.measureText(n).width + 24);
    const total = ws.reduce((s, w) => s + w, 0) + 8 * (ws.length - 1);
    let x = (a.W - total) / 2;
    names.forEach((n, i) => {
      fillRR(ctx, x, y, ws[i], 30, 15, C.card);
      strokeRR(ctx, x + 0.5, y + 0.5, ws[i] - 1, 29, 15, C.line, 1);
      text(ctx, n, x + ws[i] / 2, y + 15, font(13), C.ink2, 'center');
      x += ws[i] + 8;
    });
    if (g.wait) text(ctx, `另有 ${g.wait} 人在候补`, a.W / 2, y + 46, font(12), C.ink3, 'center');
    return y + (g.wait ? 66 : 46);
  }

  drawInfo(ctx, g, y) {
    const a = this.app, x = 16, w = a.W - 32, labelW = 64, vw = w - 32 - labelW;
    const rows = [['地点', g.place], ['商圈', g.area], ['局头', g.host], ['备注', g.note || '—']];
    ctx.font = font(15);
    const lines = rows.map(r => wrap(ctx, r[1], vw));
    const rowH = lines.map(l => Math.max(52, 20 + l.length * 22 + 10));
    const h = rowH.reduce((s, v) => s + v, 0);
    card(ctx, x, y, w, h, 18);
    let ry = y;
    rows.forEach((r, i) => {
      text(ctx, r[0], x + 16, ry + 26, font(13), C.ink3);
      lines[i].forEach((ln, j) => text(ctx, ln, x + 16 + labelW, ry + 26 + j * 22, font(15), C.ink));
      ry += rowH[i];
      if (i < rows.length - 1) { ctx.fillStyle = C.line2; ctx.fillRect(x + 16, ry, w - 32, 1); }
    });
    return y + h + 18;
  }

  drawBar(ctx, g) {
    const a = this.app, W = a.W;
    const y = a.H - BAR_H - a.safeBottom;
    ctx.fillStyle = C.card;
    ctx.fillRect(0, y, W, BAR_H + a.safeBottom);
    ctx.fillStyle = C.line;
    ctx.fillRect(0, y, W, 1);

    const bx = 16, by = y + 12, bh = 52, sw = 110;
    const sp = a.isPressed('share');
    fillRR(ctx, bx, by, sw, bh, 16, sp ? C.beige : C.card);
    strokeRR(ctx, bx + 1, by + 1, sw - 2, bh - 2, 15, C.ink, 1.5);
    text(ctx, '分享', bx + sw / 2, by + bh / 2, font(16, '700'), C.ink, 'center');
    a.hit('share', bx, by, sw, bh, () => this.share());

    const mx = bx + sw + 12, mw = W - mx - 16;
    const joined = a.me && g.players.includes(a.me);
    const full = g.players.length >= 4;
    let label, color = C.red, fg = C.cream, onTap;
    if (joined && g.players[0] === a.me) { label = '我是局头 · 叫人来'; color = C.beige; fg = C.ink; onTap = () => this.share(); }
    else if (joined) { label = '已报名 · 退出'; color = C.beige; fg = C.ink2; onTap = () => this.leave(); }
    else if (full) { label = `排候补 · 第 ${g.wait + 1} 位`; onTap = () => a.toast('原型：候补流程没做'); }
    else { label = '报名占座'; onTap = () => this.join(); }
    const mp = a.isPressed('main');
    fillRR(ctx, mx, by, mw, bh, 16, mp ? (color === C.red ? C.redDeep : C.line2) : color);
    text(ctx, label, mx + mw / 2, by + bh / 2, font(17, '700'), fg, 'center');
    a.hit('main', mx, by, mw, bh, onTap);
  }

  // ── 动作 ──

  join() {
    const a = this.app;
    if (!a.me) { this.askNickname(); return; }
    this.subscribeThenSeat();
  }

  // 必须在 touchend 回调里同步调用，前面不能有 await 或网络请求
  subscribeThenSeat() {
    const a = this.app;
    const seat = () => this.takeSeat();
    if (wx.requestSubscribeMessage) {
      wx.requestSubscribeMessage({
        tmplIds: TMPL_IDS,
        success: () => seat(),
        fail: e => { a.toast('订阅没开（原型模板 ID 是假的）：' + (e.errCode || e.errMsg)); seat(); },
      });
    } else seat();
  }

  takeSeat() {
    const a = this.app, g = this.g;
    if (g.players.length >= 4) { a.toast('刚刚被人抢先坐满了'); return; }
    g.players.push(a.me);
    const i = g.players.length - 1;
    this.pop[i] = 0;
    a.animate(360, p => { this.pop[i] = ease.spring(p); }, () => { delete this.pop[i]; });
    wx.vibrateShort && wx.vibrateShort({ type: 'light' });
    a.toast(g.players.length === 4 ? '坐满了，开打！' : `报上了，还差 ${4 - g.players.length} 人`);
  }

  leave() {
    const a = this.app, g = this.g;
    wx.showModal({
      title: '退出这一局？', content: '座位会让给候补的人', confirmText: '退出', confirmColor: C.red,
      success: r => { if (r.confirm) { g.players = g.players.filter(p => p !== a.me); a.toast('已退出'); a.redraw(); } },
    });
  }

  share() {
    const g = this.g, n = data.need(g);
    // 卡片图：正式版由服务端按局渲染 5:4 的图（一期 OG 图是 1.91:1，不能直接用）
    wx.shareAppMessage({
      title: `${g.day} ${g.time} ${g.area} 掼蛋，${n > 0 ? `还差 ${n} 人` : '已满，可排候补'}`,
      query: 'g=' + g.code,
    });
  }

  // 称呼弹层：点输入框拉起原生键盘；「好了」按钮的点按里接着发订阅请求
  askNickname() {
    const a = this.app;
    const st = { value: '' };
    const sheet = {
      height: 236,
      draw: (ctx, y) => {
        const W = a.W;
        text(ctx, '怎么称呼你？', 20, y + 34, font(20, '900', true), C.ink);
        text(ctx, '牌友看到的名字，之后可以在「我的」里改', 20, y + 62, font(13), C.ink3);
        const fy = y + 84, focused = a.editing === st.ed;
        fillRR(ctx, 20, fy, W - 40, 52, 12, C.paper);
        strokeRR(ctx, 20.5, fy + 0.5, W - 41, 51, 12, focused ? C.red : C.line, focused ? 1.5 : 1);
        if (st.value) text(ctx, st.value, 36, fy + 26, font(16), C.ink);
        else text(ctx, '比如：老王、Lily', 36, fy + 26, font(16), C.ink4);
        if (focused && Date.now() % 1000 < 550) {
          ctx.font = font(16);
          const cx = 36 + (st.value ? ctx.measureText(st.value).width + 1 : 0);
          ctx.fillStyle = C.red; ctx.fillRect(cx, fy + 15, 1.5, 22);
        }
        a.hit('nick-input', 20, fy, W - 40, 52, () => openKb());
        const ok = st.value.trim().length > 0, by = fy + 70, pr = a.isPressed('nick-ok');
        fillRR(ctx, 20, by, W - 40, 52, 16, ok ? (pr ? C.redDeep : C.red) : C.khaki);
        text(ctx, '好了，报名', W / 2, by + 26, font(17, '700'), C.cream, 'center');
        a.hit('nick-ok', 20, by, W - 40, 52, () => {
          if (!ok) { openKb(); return; }
          a.me = st.value.trim().slice(0, 12);
          wx.hideKeyboard({});
          a.closeSheet();
          this.subscribeThenSeat(); // 仍在这次点按的 touchend 里
        });
      },
    };
    const openKb = () => {
      st.ed = { value: st.value, maxLength: 12, confirmType: 'done', onInput: v => { st.value = v; } };
      a.edit(st.ed);
    };
    a.openSheet(sheet);
  }
}

module.exports = Detail;
