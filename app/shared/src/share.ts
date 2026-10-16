/**
 * 分享话术与链接（两端共用：#253 从 app/h5/src/share.ts 搬过来的**纯逻辑**部分）。
 *
 * 这里只拼文案、拼 URL —— 时间的写法由调用方给（`when` 参数）：H5 用 `app/h5/src/format.ts`
 * （`Intl` + Asia/Jakarta），小游戏用 `src/lib/fmt.ts`（安卓微信不一定有 `Intl`，见 build.mjs
 * 里那个换模块的插件）。所以这个文件**不 import 任何日期实现**，也就不需要构建期替换。
 *
 * 平台相关的那几件（`navigator.share` 的系统分享、复制到剪贴板）留在各自那边：
 * H5 是 `app/h5/src/share.ts`（它再把这里的入口按 H5 的时间写法包一层，所以那边的调用点没动），
 * 小游戏在自己的场景里用 wx 的接口。
 *
 * #253 P8 起 `origin` 由调用方**必填**：这一层不再读 `window.location`（原来是
 * `typeof window !== "undefined" && window.location?.origin`）。默认值留在各端的壳里
 * （H5 在 `app/h5/src/share.ts` 用当前站点，小游戏传 `API_BASE`），
 * 配合 `app/shared/tsconfig.json` 不带 DOM lib —— 共享层再冒出浏览器 API 就是编译错误。
 */

/** 牌局链接：`https://<站点>/g/<短码>`。 */
export function shareURL(code: string, origin: string): string {
  return `${origin}/g/${code}`;
}

/**
 * 海报地址（服务端渲染的 1080×1440 竖版图）。
 *
 * 带上版本号是为了满足「海报内容和座位状态实时一致」：座位一变牌局 version 就 +1，
 * URL 跟着变，中转缓存和微信的图片缓存不会把上一版端出来。
 */
export function posterURL(code: string, version: number, origin: string): string {
  return `${origin}/g/${code}/poster.png?v=${version}`;
}

/**
 * 话术要用到的字段（#253 的规矩：共享层只声明自己真读的那几个字段，不反向依赖某一端的 api）。
 * 传整份 `GameDetail` 进来照样成立 —— TS 是结构化的。
 */
export interface ShareLike {
  game: { short_code: string; venue: string; status: string };
  missing: number;
}

/**
 * 一句话话术。缺人时是「差 N 人」，满了是「已经满了，可以排候补」——
 * 完成标志里「话术随缺几人变化」就是这一条。
 *
 * `when` 是已经拼好的「今天 19:30」这种字符串，由调用方按自己那端的时间写法给它。
 */
export function shareText(detail: ShareLike, when: string, origin: string): string {
  return compose(detail, when, origin, true);
}

/**
 * 系统分享用的正文：**不带链接**。
 *
 * `navigator.share` 会把 `url` 和 `text` 一起交给目标 App，正文里再写一遍链接，
 * 对方那边就会看到两遍（iOS 尤其明显）。链接交给 `url` 字段。
 */
export function systemShareText(detail: ShareLike, when: string): string {
  return compose(detail, when, null, false);
}

/** 话术的骨架：先写这一局是什么情况，再决定要不要缀上链接。 */
function compose(detail: ShareLike, when: string, origin: string | null, withLink: boolean): string {
  const { game } = detail;
  const where = game.venue;
  // 不带链接的那种（系统分享）根本用不到 link，所以 origin 给 null 就够，别逼调用方传。
  const link = withLink ? shareURL(game.short_code, origin ?? "") : "";

  let base: string;
  let linkSuffix: string;
  switch (game.status) {
    case "cancelled":
      base = `${when} ${where} 这一局取消了，下次再约`;
      linkSuffix = ` 👉 ${link}`;
      break;
    case "done":
      base = `${when} ${where} 这一局打完了`;
      linkSuffix = ` 👉 ${link}`;
      break;
    case "full":
      base = `${when} ${where} 已经满了，想打可以排候补`;
      linkSuffix = `，点这里 👉 ${link}`;
      break;
    default:
      base = `${when} ${where} ${missingWords(detail.missing)}`;
      linkSuffix = `，来的点 👉 ${link}`;
      break;
  }
  return withLink ? base + linkSuffix : base;
}

function missingWords(missing: number): string {
  if (missing <= 0) return "就等开打了";
  return `差 ${missing} 人`;
}

/** WhatsApp 的分享入口。`wa.me/?text=` 不带号码时让用户自己选群或人。 */
export function whatsappURL(text: string): string {
  return `https://wa.me/?text=${encodeURIComponent(text)}`;
}
