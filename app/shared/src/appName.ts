/**
 * 应用名（#273）。
 *
 * 全站只有这一个来源：H5 的页面标题和首页大字、系统分享的标题、小游戏的首页大字和转发
 * 卡片兜底，都取它。三个静态文件（`app/h5/index.html`、`app/h5/public/manifest.json`、
 * `app/h5/public/sw.js`）编译不过去，所以在它们里写占位符 `%APP_NAME%`，由
 * `app/h5/vite.config.ts` 里的 app-name 插件在构建（和 dev server）时替换成这里的值。
 *
 * 改名的时侯改这一行就够了。
 *
 * 纯数据：没有 import、不碰 DOM —— 共享层里一句顶层的浏览器 API，会让小游戏那套跑在
 * node 里的测试在 import 那一刻就整片炸掉。
 */
export const APP_NAME = "随时掼掼蛋";
