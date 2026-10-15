// 各页共用的弹层：填称呼、认领码认回、报名成功、候补转正、加日历、分享、选牌友。

import type { App, Sheet } from "../core/app";
// #144/#155 的转发卡片用 measure（core/ui），main 上的补位弹层用 icon（core/widgets）—— 两边都留。
import { C, Ctx, circle, ellipsize, fillRR, font, initial, measure, paragraph, strokeRR, text, wrap, Scroller } from "../core/ui";
import { avatar, button, copy, field, icon, sheetButtons, sheetHeader, simpleSheet, textLink } from "../core/widgets";
import { ApiError, API_BASE, listFriends, posterURL, shareURL, type Friend, type GameDetail, type UserRef } from "../lib/api";
import { dayShort, kindLabel, slashDate, timeHM, weekday, wibParts } from "../lib/fmt";
import { currentUser, linkWithCode, signUp } from "../lib/session";
import { track } from "../lib/telemetry";
import { shareText } from "../../../h5/src/share";

// ── 填称呼（B2）。返回填好的身份；用户关掉返回 null ──
//
// onSubmitTap 在用户点「就用这个名字」「认回我」的那次点按里同步调用：报名时没有身份，
// 「报名占座」那一下要用来弹称呼层，订阅消息只能放在这一下里问（#137）。

export interface IdentityOpts {
  onSubmitTap?: () => void;
}

export function requireIdentity(app: App, opts: IdentityOpts = {}): Promise<UserRef | null> {
  const now = currentUser();
  if (now) return Promise.resolve(now);
  return new Promise((resolve) => {
    let value = "", busy = false, error = "", settled = false;
    const finish = (u: UserRef | null) => { if (!settled) { settled = true; resolve(u); } };
    const submit = async () => {
      if (busy || !value.trim()) return;
      busy = true; error = ""; app.redraw();
      try {
        const u = await signUp(value.trim());
        finish(u); // 先交出结果：closeSheet 会触发 onClose → finish(null)
        app.closeSheet(s);
      } catch (err) {
        error = err instanceof ApiError ? err.message : "提交失败，请再试一次";
      } finally {
        busy = false; app.redraw();
      }
    };
    const s: Sheet = {
      onClose: () => finish(null),
      draw(ctx, y, w) {
        let cy = sheetHeader(ctx, y, w, "怎么称呼你？", "填一次就好，之后在微信里自动认得出你");
        cy += field(app, ctx, 20, cy, w - 40, {
          key: "nickname", value, placeholder: "比如：老王", maxLength: 12, error,
          onChange: (v) => { value = v; error = ""; }, onConfirm: () => void submit(),
        }) + 14;
        cy = sheetButtons(app, ctx, cy, w, [
          {
            label: busy ? "正在保存…" : "就用这个名字", style: "primary", disabled: busy || !value.trim(),
            onTap: () => { if (!busy && value.trim()) opts.onSubmitTap?.(); void submit(); },
          },
          { label: "我在网页上报过名", style: "link", onTap: () => { app.closeSheet(s); linkSheet(app, opts).then(finish); } },
          { label: "先看看", style: "link", onTap: () => app.closeSheet(s) },
        ]);
        return cy - y + 8;
      },
    };
    app.openSheet(s);
  });
}

// ── 我在网页上报过名：输入 H5「我的 › 在别的设备上登录」给的 6 位认领码（#84 B2）──

export function linkSheet(app: App, opts: IdentityOpts = {}): Promise<UserRef | null> {
  return new Promise((resolve) => {
    let value = "", busy = false, error = "", settled = false;
    const finish = (u: UserRef | null) => { if (!settled) { settled = true; resolve(u); } };
    const submit = async (confirm = false) => {
      const code = value.replace(/\D/g, "");
      if (busy || code.length !== 6) { error = code.length === 6 ? "" : "认领码是 6 位数字"; app.redraw(); return; }
      busy = true; error = ""; app.redraw();
      try {
        const u = await linkWithCode(code, confirm);
        finish(u);
        app.closeSheet(s);
        app.toast(`认出来了，${u.nickname}`);
      } catch (err) {
        if (err instanceof ApiError && err.code === "claim_conflict" && !confirm) {
          const web = err.body?.other_nickname || "网页上的你";
          busy = false;
          simpleSheet(app, {
            dialog: true,
            title: `换成「${web}」？`,
            sub: `这个微信在小游戏里已经有一个身份了。换成网页上的「${web}」后，原来那个身份的报名记录还在，但这个微信不再认它。`,
            buttons: (d) => [
              { label: "保持现在的身份", style: "soft", onTap: () => { app.closeSheet(d); app.closeSheet(s); finish(null); } },
              { label: `换成「${web}」`, style: "primary", onTap: () => { app.closeSheet(d); void submit(true); } },
            ],
          });
          return;
        }
        error = err instanceof ApiError && (err.code === "claim_invalid" || err.status === 400)
          ? "认领码不对或已经过期，回网页上重新拿一个"
          : err instanceof ApiError && err.status === 429 ? "试得太多了，过一会儿再来" : "没认回来，稍后再试";
      } finally {
        busy = false; app.redraw();
      }
    };
    const s: Sheet = {
      onClose: () => finish(null),
      draw(ctx, y, w) {
        let cy = sheetHeader(ctx, y, w, "我在网页上报过名", "在网页版「我的 › 在别的设备上登录」里拿到 6 位认领码，填在这里。要和网页在同一个网络（同一个 Wi-Fi 或同一张卡）");
        cy += field(app, ctx, 20, cy, w - 40, {
          key: "link-code", value, placeholder: "6 位数字", maxLength: 6, error,
          onChange: (v) => { value = v; error = ""; }, onConfirm: () => void submit(),
        }) + 14;
        cy = sheetButtons(app, ctx, cy, w, [
          {
            label: busy ? "正在认…" : "认回我", style: "primary", disabled: busy,
            onTap: () => { if (!busy && value.replace(/\D/g, "").length === 6) opts.onSubmitTap?.(); void submit(); },
          },
          { label: "算了", style: "link", onTap: () => app.closeSheet(s) },
        ]);
        return cy - y + 8;
      },
    };
    app.openSheet(s);
  });
}

// ── 小牌桌行（报名成功弹层里那一条）──

function miniRow(ctx: Ctx, d: GameDetail, meID: string | undefined, x: number, y: number, w: number): number {
  const g = d.game;
  fillRR(ctx, x, y, w, 64, 14, C.paper);
  text(ctx, `${weekday(g.starts_at)} ${slashDate(g.starts_at)} ${timeHM(g.starts_at)}`, x + 14, y + 22, font(16, "900", true), C.ink);
  text(ctx, ellipsize(ctx, g.venue, w - 170, font(12)), x + 14, y + 44, font(12), C.ink3);
  const seated = [...d.seats].sort((a, b) => a.seat_no - b.seat_no);
  let cx = x + w - 52 - 26 * 3;
  for (let i = 0; i < g.capacity; i++) {
    const s = seated[i];
    if (s) avatar(ctx, cx, y + 32, 11, initial(s.user.nickname), s.user.id === d.host.id ? "host" : s.user.id === meID ? "me" : "other");
    else circle(ctx, cx, y + 32, 10.5, null, C.khaki, 1.2, [3, 3]);
    cx += 22;
  }
  text(ctx, `${seated.length}/${g.capacity}`, x + w - 14, y + 32, font(14, "900", true), C.ink, "right");
  return 64;
}

// ── 报名成功（B4–B6）：同意了订阅消息的「开局提醒」就告诉他会提醒；没同意给「加到日历」（#137）──

export function joinedSheet(app: App, d: GameDetail, onCalendar: () => void, reminded = false) {
  const full = d.game.status === "full" || d.missing <= 0;
  const s: Sheet = {
    draw(ctx, y, w) {
      circle(ctx, w / 2, y + 44, 22, C.greenTint);
      ctx.beginPath(); ctx.moveTo(w / 2 - 9, y + 44); ctx.lineTo(w / 2 - 2, y + 51); ctx.lineTo(w / 2 + 10, y + 37);
      ctx.strokeStyle = C.green; ctx.lineWidth = 3; ctx.lineCap = "round"; ctx.stroke();
      let cy = sheetHeader(ctx, y + 52, w, full ? `成局了！${d.game.capacity} 人到齐` : "占上了！", undefined, true);
      cy += miniRow(ctx, d, currentUser()?.id, 20, cy, w - 40) + 12;
      text(ctx, reminded ? "开局前 1 小时会提醒你" : "局头改了时间，这里和日历都会跟着更新", w / 2, cy + 8, font(12), reminded ? C.green : C.ink3, "center");
      cy = sheetButtons(app, ctx, cy + 26, w, [
        { label: "好的，到时见", style: "primary", onTap: () => app.closeSheet(s) },
        ...(reminded ? [] : [{ label: "加到日历", style: "ghost" as const, onTap: () => { app.closeSheet(s); onCalendar(); } }]),
      ]);
      return cy - y + 8;
    },
  };
  app.openSheet(s);
}

// ── 候补转正（W3）──

export function promotedSheet(app: App, g: { starts_at: number; venue: string }, h: { onOk: () => void; onCalendar: () => void; onLeave: () => void }) {
  let decided = false;
  const s: Sheet = {
    onClose: () => { if (!decided) h.onOk(); },
    draw(ctx, y, w) {
      circle(ctx, w / 2, y + 46, 24, C.redTint);
      icon.heart(ctx, w / 2, y + 47, 24, C.red);
      let cy = sheetHeader(ctx, y + 56, w, "有人退出，你补上了！", `${dayShort(g.starts_at)} ${timeHM(g.starts_at)} · ${g.venue}\n轮到你了，已经给你占上座`, true);
      cy = sheetButtons(app, ctx, cy + 4, w, [
        { label: "好的，到时见", style: "primary", onTap: () => app.closeSheet(s) },
        { label: "加到日历", style: "ghost", onTap: () => { decided = true; app.closeSheet(s); h.onCalendar(); } },
        { label: "去不了了，让给下一位", style: "link", onTap: () => { decided = true; app.closeSheet(s); h.onLeave(); } },
      ]);
      return cy - y + 8;
    },
  };
  app.openSheet(s);
}

// ── 加日历（#6）：基础库有 addPhoneCalendar 就直接加，否则复制日历链接去浏览器打开 ──

export function addToCalendar(app: App, d: GameDetail) {
  const g = d.game;
  if (wx.addPhoneCalendar) {
    wx.addPhoneCalendar({
      title: `掼蛋 · ${g.venue}`,
      startTime: g.starts_at,
      endTime: g.starts_at + 3 * 3600,
      location: g.venue,
      description: `${g.area} · 局头 ${d.host.nickname} · ${shareURL(g.short_code)}`,
      alarm: true,
      alarmOffset: 3600,
      success: () => app.toast("加好了，开局前 1 小时提醒你"),
      fail: (e: any) => {
        if (!/cancel/i.test(e?.errMsg || "")) calendarFallback(app, g.short_code);
      },
    });
    return;
  }
  calendarFallback(app, g.short_code);
}

function calendarFallback(app: App, code: string) {
  simpleSheet(app, {
    title: "加入日历",
    sub: "小游戏里加不了日历。复制下面的链接，粘到手机浏览器里打开，再点「加入日历」",
    buttons: (s) => [
      { label: "复制链接", style: "primary", onTap: () => { copy(app, shareURL(code), "链接已复制，去浏览器里打开"); app.closeSheet(s); } },
      { label: "知道了", style: "link", onTap: () => app.closeSheet(s) },
    ],
  });
}

// ── 分享（C1）：发给微信好友 / 群、保存海报、复制话术 ──

export function shareSheet(app: App, d: GameDetail) {
  const g = d.game;
  const textS = shareText(d, Math.floor(Date.now() / 1000), API_BASE);
  const channel = (c: "wechat" | "poster" | "copy") => track("share_channel", { game: g.short_code, channel: c });
  let poster: any = null, posterFailed = false;
  const img = wx.createImage();
  img.onload = () => { poster = img; app.redraw(); };
  img.onerror = () => { posterFailed = true; app.redraw(); };
  img.src = posterURL(g.short_code, g.version);

  const savePoster = () => {
    channel("poster");
    wx.downloadFile({
      url: posterURL(g.short_code, g.version),
      success: (r: any) => {
        if (r.statusCode !== 200) { app.toast("海报没下载下来，先复制话术"); return; }
        wx.saveImageToPhotosAlbum({
          filePath: r.tempFilePath,
          success: () => app.toast("海报存到相册了"),
          // 没给相册权限：打开大图，长按也能保存或转发
          fail: () => wx.previewImage({ urls: [r.tempFilePath], showmenu: true }),
        });
      },
      fail: () => app.toast("海报没下载下来，先复制话术"),
    });
  };

  const s: Sheet = {
    draw(ctx, y, w) {
      // 海报 188×250
      const pw = 150, ph = 200, px = (w - pw) / 2, py = y + 26;
      fillRR(ctx, px, py + 3, pw, ph, 12, C.line);
      if (poster) {
        ctx.save(); ctx.beginPath(); ctx.rect(px, py, pw, ph); ctx.clip();
        ctx.drawImage(poster, px, py, pw, ph); ctx.restore();
      } else {
        fillRR(ctx, px, py, pw, ph, 12, C.redDeep);
        paragraph(ctx, posterFailed ? "海报没加载出来\n先把话术复制到群里" : "海报生成中…", px + 10, py + ph / 2 - 20, pw - 20, font(13), C.cream, 20, "center");
      }
      let cy = py + ph + 18;
      const outlets: [string, string, () => void, boolean][] = [
        ["发到微信", "💬", () => { channel("wechat"); shareToChat(app, d); }, false],
        ["保存海报", "🖼️", savePoster, posterFailed],
        ["复制话术", "📋", () => { channel("copy"); copy(app, textS, "话术已复制"); }, false],
      ];
      const ow = (w - 40) / outlets.length;
      outlets.forEach(([label, ic, fn, dis], i) => {
        const ox = 20 + i * ow, id = `outlet:${i}`;
        circle(ctx, ox + ow / 2, cy + 22, 22, app.isPressed(id) ? C.line2 : C.beige);
        text(ctx, ic, ox + ow / 2, cy + 23, font(18), dis ? C.ink4 : C.ink, "center");
        text(ctx, label, ox + ow / 2, cy + 58, font(12), dis ? C.ink4 : C.ink2, "center");
        app.hit(id, ox, cy, ow, 70, dis ? null : fn);
      });
      cy += 82;
      const tl = Math.min(4, 1 + Math.ceil(textS.length / 22));
      fillRR(ctx, 20, cy, w - 40, 24 + tl * 20, 14, C.paper);
      paragraph(ctx, textS, 34, cy + 12, w - 68, font(13), C.ink2, 20);
      cy += 36 + tl * 20;
      button(app, ctx, "share-cancel", 20, cy, w - 40, 48, "取消", "soft", () => app.closeSheet(s));
      return cy + 56 - y;
    },
  };
  app.openSheet(s);
}

/** 转发卡片的标题，和卡片图上的信息一致。 */
export function shareTitle(d: GameDetail): string {
  const g = d.game, n = d.missing;
  return `${dayShort(g.starts_at)} ${timeHM(g.starts_at)} ${g.area} 掼蛋，${g.status === "full" || n <= 0 ? "已满，可排候补" : `还差 ${n} 人`}`;
}

/** 转发卡片：5:4 的图在画布上现画，query 带牌局短码。 */
export function shareToChat(app: App, d: GameDetail) {
  wx.shareAppMessage(sharePayload(app, d));
}

/** 「发到微信」和右上角「···」转发共用。 */
export function sharePayload(app: App, d: GameDetail): { title: string; imageUrl?: string; query: string } {
  return { title: shareTitle(d), imageUrl: renderShareCard(app, d), query: `g=${encodeURIComponent(d.game.short_code)}` };
}

/**
 * 画转发卡片图，返回临时文件路径。
 * 小游戏的离屏画布在真机上导不出图片（imageUrl 变空，微信就去截当前屏幕的上半截），
 * 所以画在主画布左上角、按 5:4 裁出来，再立刻把当前页面重画回去：同一个 tick 里完成，用户看不到。
 * 版面见 drawShareCard。
 */
export function renderShareCard(app: App, d: GameDetail): string | undefined {
  const W = 500, H = 400;
  const canvas = app.canvas, ctx: Ctx = app.ctx;
  const px = Math.min(canvas.width, 1000);
  const k = px / W;
  try {
    ctx.save();
    ctx.setTransform(k, 0, 0, k, 0, 0);
    drawShareCard(ctx, d, W, H);
    ctx.restore();
    return canvas.toTempFilePathSync({ x: 0, y: 0, width: px, height: Math.round(px * H / W), destWidth: 750, destHeight: 600, fileType: "png" });
  } catch {
    return undefined;
  } finally {
    app.repaint();
  }
}

/**
 * 转发卡片 5:4（适配稿第五节）：满版牌桌红；日期、时间、地点、次要信息作为一块在 [36, H − 118] 里垂直居中；
 * 底部座位和状态签。差人时状态签醒目（金底），满座、已取消、已结束变弱；已取消、已结束整张变灰。
 */
export function drawShareCard(ctx: Ctx, d: GameDetail, W = 500, H = 400) {
  const g = d.game;
  const closed = g.status === "cancelled" || g.status === "done";
  // 满版，不留纸色边：群聊里卡片本身有白框
  ctx.fillStyle = closed ? "#8a7d70" : C.redDeep; ctx.fillRect(0, 0, W, H);
  strokeRR(ctx, 8, 8, W - 16, H - 16, 22, closed ? "#6f645a" : C.redInk, 8);
  strokeRR(ctx, 24, 24, W - 48, H - 48, 14, "rgba(233,196,106,0.45)", 2, [6, 5]);
  const CW = W - 96; // 文字区：左右各留 48
  // 地点：26 起，最多两行；放不下每次缩 2，最小 20；还放不下第二行末尾省略
  let fs = 26, vl = wrap(ctx, g.venue, CW, font(fs, "700"));
  while (vl.length > 2 && fs > 20) { fs -= 2; vl = wrap(ctx, g.venue, CW, font(fs, "700")); }
  if (vl.length > 2) vl = [vl[0], ellipsize(ctx, vl.slice(1).join(""), CW, font(fs, "700"))];
  const vlh = Math.round(fs * 1.3);
  // 信息块：日期 30 + 时间 84 + 地点 n × 行高 + 间距 10 + 次要信息 22
  const blockH = 30 + 84 + vl.length * vlh + 10 + 22;
  let y = 36 + Math.max(0, (H - 118 - 36 - blockH) / 2);
  const rel = ["今天", "明天", "后天"][wibParts(g.starts_at).dayOffset];
  const date = `${weekday(g.starts_at)} ${slashDate(g.starts_at)}`;
  text(ctx, rel ? `${rel} · ${date}` : date, W / 2, y + 15, font(22, "700"), closed ? C.cream : C.gold, "center");
  y += 30;
  text(ctx, timeHM(g.starts_at), W / 2, y + 44, font(76, "900", true), C.cream, "center");
  y += 84;
  vl.forEach((ln, i) => text(ctx, ln, W / 2, y + vlh / 2 + i * vlh, font(fs, "700"), C.cream, "center"));
  y += vl.length * vlh + 10;
  // 次要信息：局头称呼最宽 104（约 6 个汉字），整行最宽 404，超出都省略
  const host = ellipsize(ctx, d.host.nickname, 104, font(17));
  text(ctx, ellipsize(ctx, `${g.area} · ${kindLabel(g.kind)} · 局头 ${host}`, CW, font(17)), W / 2, y + 11, font(17), "rgba(255,247,236,0.75)", "center");
  // 座位：圆心 y = H − 70，从 x = 70 起间距 52；卡片给别人看，不显示「我」的金色
  const seated = [...d.seats].sort((a, b) => a.seat_no - b.seat_no);
  const cy = H - 70;
  for (let i = 0; i < 4; i++) {
    const cx = 70 + i * 52;
    const st = seated[i];
    if (st) {
      const isHost = st.user.id === d.host.id;
      circle(ctx, cx, cy, 22, isHost ? C.redTint : C.beige);
      text(ctx, initial(st.user.nickname), cx, cy + 2, font(20, "900", true), isHost ? C.red : C.ink, "center");
    } else circle(ctx, cx, cy, 21, null, "rgba(255,247,236,0.65)", 2.5, [6, 5]);
  }
  // 状态签：右边对齐 W − 48，左边不越过 x = 262；字号放不下从起始值往下缩，最小 14
  const full = g.status === "full" || d.missing <= 0;
  const label = g.status === "cancelled" ? "已取消" : g.status === "done" ? "已结束"
    : full ? (d.waitlist.length > 0 ? `已满 · ${d.waitlist.length} 人候补` : "已满 · 可排候补") : `还差 ${d.missing} 人`;
  const loud = !closed && !full;
  const weight = loud ? "900" : "700";
  const room = W - 48 - 262;
  let ls = loud ? 24 : 20, lf = font(ls, weight);
  while (measure(ctx, label, lf) + 32 > room && ls > 14) lf = font(--ls, weight);
  const lw = Math.min(room, measure(ctx, label, lf) + 32), lx = W - 48 - lw;
  fillRR(ctx, lx, cy - 22, lw, 44, 22, loud ? C.gold : "rgba(255,247,236,0.16)");
  text(ctx, label, lx + lw / 2, cy + 1, lf, loud ? C.redInk : C.cream, "center");
}

// ── 选牌友（08「邀请谁」，#115）──

export function friendPicker(
  app: App,
  o: { selected?: string[]; inGame?: Map<string, string>; confirmLabel: (n: number) => string; allowNone?: boolean; onConfirm: (picked: Friend[], sheet: Sheet) => void; busy?: () => boolean },
): Sheet {
  let friends: Friend[] | null = null, failed = false;
  const picked = new Set(o.selected ?? []);
  const scroll = new Scroller();
  let listTop = 0, listH = 0;
  listFriends().then((f) => { friends = f; app.redraw(); }).catch((err) => {
    friends = [];
    if (!(err instanceof ApiError && err.status === 401)) failed = true;
    app.redraw();
  });
  const s: Sheet = {
    scrollerAt: (_x, y) => (y >= listTop && y <= listTop + listH ? { s: scroll, axis: "y" } : null),
    step: () => scroll.step(),
    draw(ctx, y, w) {
      let cy = sheetHeader(ctx, y, w, "邀请谁", "只能邀请一起打过局的牌友；他们会收到一条通知");
      if (friends === null) { text(ctx, "正在找牌友…", w / 2, cy + 20, font(14), C.ink3, "center"); cy += 44; }
      else if (failed) { text(ctx, "牌友没拉到，稍后再试", w / 2, cy + 20, font(14), C.red, "center"); cy += 44; }
      else if (friends.length === 0) { cy += paragraph(ctx, "还没有牌友。一起打过局的人会出现在这里，也可以先把链接发到群里。", 20, cy, w - 40, font(14), C.ink3, 22, "center") + 16; }
      else {
        const rowH = 58;
        listTop = cy; listH = Math.min(friends.length * rowH, app.H * 0.42);
        scroll.setMax(friends.length * rowH - listH);
        ctx.save(); ctx.beginPath(); ctx.rect(0, listTop, w, listH); ctx.clip();
        friends.forEach((f, i) => {
          const ry = listTop + i * rowH - scroll.pos;
          if (ry + rowH < listTop || ry > listTop + listH) return;
          const on = picked.has(f.user.id);
          const already = o.inGame?.get(f.user.id);
          avatar(ctx, 42, ry + rowH / 2, 18, initial(f.user.nickname), f.starred ? "star" : "other");
          text(ctx, ellipsize(ctx, f.user.nickname, w - 150, font(15, "600")), 70, ry + 21, font(15, "600"), already ? C.ink4 : C.ink);
          text(ctx, already ?? `一起打过 ${f.together_count} 局`, 70, ry + 40, font(12), C.ink3);
          const bx = w - 46, by = ry + rowH / 2;
          if (on) { circle(ctx, bx, by, 12, C.red); icon.check(ctx, bx, by, C.cream); }
          else circle(ctx, bx, by, 11.5, null, already ? C.line : C.khaki, 1.5);
          const vy = Math.max(ry, listTop), vh = Math.min(ry + rowH, listTop + listH) - vy;
          if (!already && vh > 0) app.hit(`pick:${f.user.id}`, 0, vy, w, vh, () => {
            if (picked.has(f.user.id)) picked.delete(f.user.id);
            else if (picked.size < 20) picked.add(f.user.id);
            else app.toast("一次最多邀请 20 位");
          });
        });
        ctx.restore();
        cy = listTop + listH + 12;
      }
      const chosen = (friends ?? []).filter((f) => picked.has(f.user.id));
      cy = sheetButtons(app, ctx, cy, w, [
        { label: o.confirmLabel(chosen.length), style: "primary", disabled: (o.busy?.() ?? false) || (!o.allowNone && chosen.length === 0), onTap: () => o.onConfirm(chosen, s) },
        { label: "取消", style: "soft", onTap: () => app.closeSheet(s) },
      ]);
      return cy - y + 4;
    },
  };
  app.openSheet(s);
  return s;
}

export { textLink };
