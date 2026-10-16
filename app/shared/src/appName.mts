/**
 * 产品名（产品改名那一件）：**只有这一处写死**。
 *
 * 两端都从这里拿：
 *   - H5：`app/h5/src/title.ts` 转出（页面标题）、`pages/Home.tsx` 首页大字、
 *     `components/ShareSheet.tsx` 系统分享标题；
 *   - 小游戏：`scenes/home.ts` 首页大字、`main.ts` 转发卡片的兜底标题；
 *   - 测试与 e2e 断言也引用它，不写死字面量。
 *
 * 静态文件编译不过去，所以写占位符 `%APP_NAME%`，由 `app/h5/vite.config.ts` 里的小插件
 * 在构建时（dev server 上也一样）替换：
 *   - `app/h5/index.html` 的 `<title>`
 *   - `app/h5/public/manifest.json` 的 `name`
 *   - `app/h5/public/sw.js` 里推送通知的标题
 *
 * 改名时：改这一行 → 重新构建（H5 的 dist 和小游戏的 game.js 都要重打）。
 *
 * 为什么这个文件是 `.mts`（全仓唯一一个）：`app/h5/vite.config.ts` 也要读它（静态文件的占位符
 * 是那个配置里的插件替换的），而 vite 的 config loader 会用 **Node** 去加载配置文件 ——
 * `app/shared` 没有 `package.json`，Node 会把这里的 `.ts` 当 CJS，于是报
 * 「ESM syntax in a file loaded as CommonJS」。`.mts` 按扩展名就是 ESM，不必为此在共享层
 * 里放一个 `package.json`。功能上它和 `.ts` 一样（tsc / esbuild / 守门都认）。
 * 服务端（OG 标签、海报文字、约局助手的人设）另有一套，见那个 issue 的说明。
 */
export const APP_NAME = "随时掼掼蛋";
