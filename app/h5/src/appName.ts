/**
 * H5 这一侧的应用名入口（产品改名那一件）：实体在 `app/shared/src/appName.ts`。
 *
 * 为什么绕这一下：`vite.config.ts` 也要用它（静态文件里的 `%APP_NAME%` 占位符是那个插件替换的），
 * 而配置文件由 vite 的 config loader 加载、要用 Node 解析 —— 走 h5 自己的 `src/`
 * （`package.json` 里有 `"type": "module"`）最稳；共享层那份写成 `.mts`，按扩展名就是 ESM。
 */
export { APP_NAME } from "../../shared/src/appName.mts";
