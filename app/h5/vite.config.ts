import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import preact from "@preact/preset-vite";
import { defineConfig, type Plugin } from "vitest/config";
import { APP_NAME } from "../shared/src/appName";

/**
 * dist 里的 .gitkeep 是给 Go 的 embed 用的：没有它，全新 clone 上 `go build`
 * 会因为 embed 匹配不到目录而直接失败。vite 构建前会清空 dist（emptyOutDir），
 * 所以构建结束再把占位文件补回来。
 */
function keepDistPlaceholder(): Plugin {
  return {
    name: "keep-dist-placeholder",
    closeBundle() {
      writeFileSync(fileURLToPath(new URL("./dist/.gitkeep", import.meta.url)), "");
    },
  };
}

/** 应用名只有一个来源（#273）：见 app/shared/src/appName.ts 的说明。 */
const APP_NAME_PLACEHOLDER = "%APP_NAME%";

/**
 * 把静态文件里的 `%APP_NAME%` 换成共享层里那个常量（#273）。
 *
 * `index.html`、`public/manifest.json`、`public/sw.js` 是静态文件 —— 它们不进模块图，
 * 编译不过去，所以名字在它们里是占位符：
 *
 *   - `index.html`：dev 和构建都走 `transformIndexHtml`，两边都换得掉；
 *   - `public/` 下那两个：构建时原样拷进 dist，所以拷完再补一次；dev 下是原样发的，
 *     这里挂一段中间件顺手换掉，免得开发时在浏览器里看到占位符。
 */
function appName(): Plugin {
  const fill = (code: string) => code.split(APP_NAME_PLACEHOLDER).join(APP_NAME);
  const files = ["manifest.json", "sw.js"];
  const publicFile = (url: string) => files.find((f) => (url.split("?")[0] ?? "") === `/${f}`);

  return {
    name: "app-name",
    transformIndexHtml: (html) => fill(html),
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const file = publicFile(req.url ?? "");
        if (!file) return next();
        res.setHeader("Content-Type", file.endsWith(".json") ? "application/json" : "text/javascript");
        res.end(fill(readFileSync(fileURLToPath(new URL(`./public/${file}`, import.meta.url)), "utf8")));
      });
    },
    closeBundle() {
      for (const file of files) {
        const path = fileURLToPath(new URL(`./dist/${file}`, import.meta.url));
        writeFileSync(path, fill(readFileSync(path, "utf8")));
      }
    },
  };
}

export default defineConfig({
  plugins: [preact(), keepDistPlaceholder(), appName()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // 微信内置浏览器（Android XWeb / iOS WKWebView）的较老版本也要能跑。
    target: ["es2019", "safari13", "chrome80"],
  },
  server: {
    // 本地开发时前端只负责页面，API 由 Go 进程提供（make run，默认 :8080）。
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
      "/g": "http://localhost:8080",
      // 实时连接（§5.5）：要透传 Upgrade 头。
      "/ws": { target: "ws://localhost:8080", ws: true },
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/setupTests.ts"],
  },
});
