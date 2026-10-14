// 首页 · 牌局列表（对应设计稿 A2）
// 标题区和筛选固定，列表区单独滚动；商圈胶囊可以横向滑动。

const { C, font, fillRR, strokeRR, card, text, ellipsize, circle, Scroller } = require('../ui');
const data = require('../data');

const DAYS = ['全部', '今天', '明天', '周末'];
const CARD_H = 116, GAP = 14, TAB_H = 56;

class Home {
  constructor(app, open) {
    this.app = app;
    this.open = open;   // { detail(code), create() }
    this.day = '全部';
    this.area = '全部';
    this.list = new Scroller();
    this.chips = new Scroller();
  }

  filtered() {
    return data.games.filter(g =>
      (this.day === '全部' || (this.day === '周末' ? ['周六', '周日'].includes(g.week) : g.day === this.day)) &&
      (this.area === '全部' || g.area === this.area));
  }

  layout() {
    const a = this.app;
    // 标题放在胶囊下方：胶囊所在的那一行只留给导航，不放内容
    this.titleY = a.menu.bottom + 26;
    this.segY = this.titleY + 46;
    this.chipY = this.segY + 56;
    this.listTop = this.chipY + 46;
    this.listBottom = a.H - TAB_H - a.safeBottom;
  }

  scrollerAt(x, y) {
    if (y >= this.chipY - 4 && y <= this.chipY + 38) return { s: this.chips, axis: 'x' };
    if (y >= this.listTop && y <= this.listBottom) return { s: this.list, axis: 'y' };
    return null;
  }

  step() { return this.list.step() | this.chips.step(); }

  draw(ctx) {
    const a = this.app, W = a.W;
    this.layout();
    ctx.fillStyle = C.paper;
    ctx.fillRect(0, 0, W, a.H);

    // 胶囊那一行：左边放地区，避开右上角
    text(ctx, '📍 Jakarta · WIB', 16, a.navY, font(13), C.ink3);

    text(ctx, '随时掼掼蛋', 16, this.titleY, font(28, '900', true), C.red);
    text(ctx, '找人凑一桌，就差你一个', 16, this.titleY + 28, font(13), C.ink3);
    this.drawMiniCards(ctx, W - 86, this.titleY - 22);

    // 日期分段控件
    const sx = 16, sw = W - 32, sh = 44;
    fillRR(ctx, sx, this.segY, sw, sh, 16, C.beige);
    const cw = (sw - 8) / DAYS.length;
    DAYS.forEach((d, i) => {
      const x = sx + 4 + i * cw;
      const on = this.day === d;
      if (on) fillRR(ctx, x, this.segY + 4, cw, sh - 8, 13, C.card);
      text(ctx, d, x + cw / 2, this.segY + sh / 2, font(15, on ? '700' : '500'), on ? C.ink : C.ink3, 'center');
      a.hit('day:' + d, x, this.segY, cw, sh, () => { this.day = d; this.list.scrollTo(0); });
    });

    this.drawChips(ctx);
    this.drawList(ctx);
    this.drawTabs(ctx);
  }

  drawMiniCards(ctx, x, y) {
    const one = (dx, rot, suit, color) => {
      ctx.save();
      ctx.translate(x + dx + 18, y + 24);
      ctx.rotate(rot);
      fillRR(ctx, -17, -23, 34, 46, 5, C.line);
      fillRR(ctx, -18, -24, 34, 46, 5, C.card);
      strokeRR(ctx, -18, -24, 34, 46, 5, C.line2, 1);
      text(ctx, suit, -1, -1, font(18), color, 'center');
      ctx.restore();
    };
    one(0, -0.12, '♠', C.ink);
    one(36, 0.1, '♥', C.red);
  }

  drawChips(ctx) {
    const a = this.app;
    const items = ['全部'].concat(data.AREAS);
    ctx.save();
    ctx.beginPath(); ctx.rect(0, this.chipY - 4, a.W, 46); ctx.clip();
    let x = 16 - this.chips.pos;
    ctx.font = font(14);
    for (const it of items) {
      const w = ctx.measureText(it).width + 28;
      const on = this.area === it;
      fillRR(ctx, x, this.chipY, w, 36, 18, on ? C.red : C.card);
      if (!on) strokeRR(ctx, x + 0.5, this.chipY + 0.5, w - 1, 35, 18, C.line, 1);
      text(ctx, it, x + w / 2, this.chipY + 18, font(14, on ? '600' : 'normal'), on ? C.cream : C.ink2, 'center');
      a.hit('area:' + it, x, this.chipY, w, 36, () => { this.area = it; this.list.scrollTo(0); });
      x += w + 8;
    }
    ctx.restore();
    this.chips.setMax(x + this.chips.pos - 8 + 16 - a.W);
  }

  drawList(ctx) {
    const a = this.app, W = a.W;
    const games = this.filtered();
    const top = this.listTop, bottom = this.listBottom;
    ctx.save();
    ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top - this.list.pos;

    text(ctx, '可报名的局', 16, y + 18, font(20, '900', true), C.ink);
    text(ctx, `${games.filter(g => data.need(g) > 0).length} 桌在等人`, W - 16, y + 20, font(12), C.ink3, 'right');
    y += 44;

    if (!games.length) {
      text(ctx, '这个时间段还没有局', W / 2, y + 60, font(15), C.ink3, 'center');
      text(ctx, '点下面的「蛋」开一局', W / 2, y + 86, font(13), C.ink4, 'center');
      y += 140;
    }
    for (const g of games) {
      if (y + CARD_H > top - 10 && y < bottom + 10) this.drawCard(ctx, g, 16, y, W - 32);
      y += CARD_H + GAP;
    }
    text(ctx, '不涉及任何金钱输赢 · 纯娱乐约局', W / 2, y + 10, font(11), C.ink4, 'center');
    y += 40;
    ctx.restore();
    this.list.setMax(y + this.list.pos - bottom);
  }

  drawCard(ctx, g, x, y, w) {
    const a = this.app, id = 'card:' + g.code;
    const pressed = a.isPressed(id);
    ctx.save();
    if (pressed) { ctx.translate(x + w / 2, y + CARD_H / 2); ctx.scale(0.98, 0.98); ctx.translate(-x - w / 2, -y - CARD_H / 2); }
    card(ctx, x, y, w, CARD_H, 18);

    // 时间块
    fillRR(ctx, x + 14, y + 14, 82, CARD_H - 28, 10, C.paper);
    text(ctx, g.day, x + 55, y + 34, font(12, '700'), C.red, 'center');
    text(ctx, g.time, x + 55, y + 59, font(26, '900', true), C.ink, 'center');
    text(ctx, g.date, x + 55, y + 84, font(11), C.ink3, 'center');

    const rx = x + 110, rw = w - 110 - 14;
    ctx.font = font(12);
    const tw = ctx.measureText(g.area).width + 14;
    fillRR(ctx, rx, y + 16, tw, 22, 6, C.sand);
    text(ctx, g.area, rx + tw / 2, y + 27, font(12), C.ink2, 'center');
    text(ctx, g.kind, rx + tw + 8, y + 27, font(12), C.ink3);
    ctx.font = font(16, '700');
    text(ctx, ellipsize(ctx, g.place, rw), rx, y + 55, font(16, '700'), C.ink);

    // 座位
    for (let i = 0; i < 4; i++) {
      const cx = rx + 14 + i * 32, cy = y + 88;
      const p = g.players[i];
      if (p) {
        const host = i === 0, me = p === a.me && a.me;
        circle(ctx, cx, cy, 13, me ? C.gold : host ? C.redTint : C.beige);
        text(ctx, p.slice(0, 1), cx, cy + 1, font(13, '900', true), host ? C.red : C.ink, 'center');
      } else {
        circle(ctx, cx + 0, cy, 12.5, null, C.khaki, 1.2, [3, 3]);
      }
    }
    // 状态
    const n = data.need(g);
    const label = n > 0 ? `差 ${n} 人` : g.wait ? `满 · 候补 ${g.wait}` : '满 · 4/4';
    ctx.font = font(13, '700');
    const bw = ctx.measureText(label).width + 22;
    const bx = x + w - 14 - bw;
    const strong = n === 1;
    fillRR(ctx, bx, y + 74, bw, 28, 8, n === 0 ? C.beige : strong ? C.red : C.redTint);
    text(ctx, label, bx + bw / 2, y + 88, font(13, '700'), n === 0 ? C.ink3 : strong ? C.cream : C.red, 'center');
    ctx.restore();

    // 只登记可见部分，避免点到被裁掉的卡片
    const vy = Math.max(y, this.listTop), vh = Math.min(y + CARD_H, this.listBottom) - vy;
    if (vh > 0) a.hit(id, x, vy, w, vh, () => this.open.detail(g.code));
  }

  drawTabs(ctx) {
    const a = this.app, W = a.W;
    const y = a.H - TAB_H - a.safeBottom;
    ctx.fillStyle = C.card;
    ctx.fillRect(0, y, W, TAB_H + a.safeBottom);
    ctx.fillStyle = C.line;
    ctx.fillRect(0, y, W, 1);
    const tabs = [['♠', '牌局'], ['♣', '学牌'], null, ['♦', '牌友'], ['♥', '我的']];
    const tw = W / 5;
    tabs.forEach((t, i) => {
      const cx = tw * i + tw / 2;
      if (!t) return;
      const on = i === 0;
      text(ctx, t[0], cx, y + 20, font(22), on ? C.red : C.ink3, 'center');
      text(ctx, t[1], cx, y + 42, font(11, on ? '700' : 'normal'), on ? C.red : C.ink3, 'center');
      if (!on) a.hit('tab:' + i, tw * i, y, tw, TAB_H, () => a.toast(`原型只做了牌局页，「${t[1]}」没做`));
    });
    // 中间的「蛋」：开局入口
    const cx = W / 2, cy = y + 6, pressed = a.isPressed('tab:egg');
    circle(ctx, cx, cy, 31, C.card);
    circle(ctx, cx, cy, pressed ? 25 : 27, pressed ? C.redDeep : C.red);
    text(ctx, '蛋', cx, cy + 1, font(24, '900', true), C.cream, 'center');
    text(ctx, '约局', cx, y + 46, font(11), C.ink3, 'center');
    a.hit('tab:egg', cx - 34, cy - 34, 68, 90, () => this.open.create());
  }
}

module.exports = Home;
