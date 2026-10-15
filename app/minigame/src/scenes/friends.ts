// 09 · 牌友（设计稿 F1 / F2，#108）：群横幅、牌友列表、星标、约 TA。

import type { App, Scene, Sheet } from "../core/app";
import { C, Ctx, Scroller, circle, ellipsize, fillRR, font, initial, strokeRR, text } from "../core/ui";
import { PAD, avatar, button, chevron, copy, disclaimer, emptyState, errorLine, icon, nav, sheetHeader, textLink } from "../core/widgets";
import { ApiError, API_BASE, getGroupInfo, listFriends, setStar, type Friend, type GroupInfo } from "../lib/api";
import { inviteText } from "../../../h5/src/invite";
import { TAB_H } from "./home";
import type { Nav } from "./nav";

function sorted(list: Friend[]): Friend[] {
  return [...list].sort((a, b) => Number(b.starred) - Number(a.starred) || b.together_count - a.together_count || b.last_together_at - a.last_together_at);
}

export class Friends implements Scene {
  tab = "friends";
  private friends: Friend[] | null = null;
  private group: GroupInfo | null = null;
  private error = "";
  private scroll = new Scroller();

  constructor(private app: App, private nav: Nav) {
    this.scroll.onPullRefresh = () => void this.load();
  }

  onEnter() { void this.load(); getGroupInfo().then((g) => { this.group = g; this.app.redraw(); }).catch(() => {}); }
  onResume() { void this.load(); }

  private async load() {
    try {
      this.friends = sorted(await listFriends());
      this.error = "";
    } catch (err) {
      this.friends = [];
      this.error = err instanceof ApiError && err.status === 401 ? "" : "牌友没拉到，下拉再试";
    } finally { this.app.redraw(); }
  }

  private async toggle(f: Friend) {
    const next = !f.starred;
    const apply = (v: boolean) => { this.friends = this.friends && sorted(this.friends.map((x) => (x.user.id === f.user.id ? { ...x, starred: v } : x))); this.app.redraw(); };
    apply(next);
    try { await setStar(f.user.id, next); } catch { apply(!next); this.app.toast("没标上，稍后再试"); }
  }

  private inviteSheet(f: Friend) {
    const app = this.app;
    const msg = inviteText(f.user.nickname, API_BASE);
    const opts: [string, string, string, () => void][] = [
      ["📋", "复制邀请话术", `${f.user.nickname}，最近有空打一局掼蛋吗？`, () => { copy(app, msg, "话术已复制，发给 TA 吧"); app.closeSheet(s); }],
      ["💬", "发到 WhatsApp", "话术复制好了，打开 WhatsApp 粘贴给 TA", () => { copy(app, msg, "话术已复制，去 WhatsApp 粘贴"); app.closeSheet(s); }],
      ["♥", "开一局再叫 TA", "先定时间地点，开好就能邀请", () => { app.closeSheet(s); this.nav.create(); }],
    ];
    const s: Sheet = {
      draw: (ctx, y, w) => {
        let cy = sheetHeader(ctx, y, w, `约 ${f.user.nickname} 打一局`);
        opts.forEach(([ic, t, sub, fn], i) => {
          const id = `inv-opt:${i}`;
          fillRR(ctx, 20, cy, w - 40, 64, 14, app.isPressed(id) ? C.beige : C.paper);
          circle(ctx, 50, cy + 32, 18, i === 1 ? "#dff5e6" : i === 2 ? C.redTint : C.card);
          if (ic === "♥") icon.heart(ctx, 50, cy + 33, 16, C.red);
          else text(ctx, ic, 50, cy + 33, font(15, "700"), i === 1 ? "#1f8f4e" : C.ink2, "center");
          text(ctx, t, 80, cy + 23, font(15, "600"), C.ink);
          text(ctx, ellipsize(ctx, sub, w - 140, font(12)), 80, cy + 43, font(12), C.ink3);
          chevron(ctx, w - 38, cy + 32, C.ink4);
          app.hit(id, 20, cy, w - 40, 64, fn);
          cy += 72;
        });
        button(app, ctx, "inv-cancel", 20, cy + 4, w - 40, 48, "取消", "soft", () => app.closeSheet(s));
        return cy + 60 - y;
      },
    };
    app.openSheet(s);
  }

  scrollerAt(_x: number, y: number) {
    return y > this.app.contentTop && y < this.app.H - TAB_H - this.app.safeBottom ? { s: this.scroll, axis: "y" as const } : null;
  }
  step() { return this.scroll.step(); }

  draw(ctx: Ctx) {
    const a = this.app, W = a.W, top = a.contentTop, bottom = a.H - TAB_H - a.safeBottom;
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, a.H);
    ctx.save(); ctx.beginPath(); ctx.rect(0, top, W, bottom - top); ctx.clip();
    let y = top + 4 - this.scroll.pos;

    const g = this.group;
    if (g && (g.wechat_id || g.whatsapp_group_url)) {
      const h = 96;
      fillRR(ctx, PAD, y, W - PAD * 2, h, 16, C.redDeep);
      text(ctx, "雅加达掼蛋群", PAD + 16, y + 26, font(16, "700"), C.cream);
      text(ctx, "新局都在群里发，加群主拉你进群", PAD + 16, y + 48, font(12), "rgba(255,247,236,0.8)");
      let bx = PAD + 16;
      if (g.wechat_id) {
        button(a, ctx, "grp-wx", bx, y + 60, 110, 28, "复制微信号", "ghost", () => copy(a, g.wechat_id, `微信号 ${g.wechat_id} 已复制，加群主拉你进群`), { size: 13 });
        bx += 118;
      }
      if (g.whatsapp_group_url) button(a, ctx, "grp-wa", bx, y + 60, 130, 28, "复制 WhatsApp 群链接", "ghost", () => copy(a, g.whatsapp_group_url, "群链接已复制，去浏览器或 WhatsApp 打开"), { size: 13 });
      y += h + 14;
    }
    if (this.error) y += errorLine(a, ctx, y, this.error);
    const fs = this.friends;
    if (fs === null) y += emptyState(a, ctx, y, ["正在找牌友…"]);
    else if (fs.length === 0 && !this.error) {
      y += emptyState(a, ctx, y, ["还没和谁一起打过局。", "报一局、坐一桌，打过的人会出现在这里。"]);
      textLink(a, ctx, "go-games", W / 2, y + 4, "去找一局 ›", () => this.nav.tab("games"), C.red);
      y += 44;
    } else {
      for (const f of fs) {
        const h = 68, x = PAD, w = W - PAD * 2;
        fillRR(ctx, x, y + 2, w, h, 16, C.line);
        fillRR(ctx, x, y, w, h, 16, C.card);
        strokeRR(ctx, x + 0.5, y + 0.5, w - 1, h - 1, 16, C.line, 1);
        avatar(ctx, x + 34, y + h / 2, 20, initial(f.user.nickname), f.starred ? "star" : "other");
        if (f.starred) circle(ctx, x + 34, y + h / 2, 21, null, C.gold, 2);
        text(ctx, ellipsize(ctx, f.user.nickname, w - 210, font(15, "700")), x + 64, y + 26, font(15, "700"), C.ink);
        text(ctx, `一起打过 ${f.together_count} 局`, x + 64, y + 46, font(12), C.ink3);
        const sx = x + w - 120;
        text(ctx, f.starred ? "★" : "☆", sx, y + h / 2, font(22), f.starred ? C.gold : C.khaki, "center");
        a.hit("star:" + f.user.id, sx - 22, y, 44, h, () => void this.toggle(f));
        button(a, ctx, "yue:" + f.user.id, x + w - 88, y + 18, 74, 32, "约 TA", "primary", () => this.inviteSheet(f), { size: 13 });
        y += h + 10;
      }
    }
    y += disclaimer(a, ctx, y) + 12;
    ctx.restore();
    this.scroll.setMax(y + this.scroll.pos - bottom);
    nav(a, ctx, "牌友");
  }
}
