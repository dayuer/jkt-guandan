import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import preact from "@preact/preset-vite";
import { defineConfig, type Plugin } from "vitest/config";
import { APP_NAME } from "./src/appName.ts";

/**
 * 静态文件里的应用名（产品改名那一件）：`index.html`、`public/manifest.json`、`public/sw.js`
 * 编译不过去，所以那边写占位符 `%APP_NAME%`，在这里替换成 `app/shared/src/appName.ts` 那一个常量。
 * 构建时：`transformIndexHtml` 管 HTML，`closeBundle` 管拷进 dist 的那两个 public 文件
 * （它们不走 rollup 的 bundle 图）；dev server 上没有产物，所以另挂一层中间件按需替换。
 */
function appName(): Plugin {
  const fill = (text: string) => text.replaceAll("%APP_NAME%", APP_NAME);
  const publicFiles = ["manifest.json", "sw.js"];
  const readPublic = (name: string) => readFileSync(fileURLToPath(new URL(`./public/${name}`, import.meta.url)), "utf8");
  return {
    name: "app-name",
    transformIndexHtml: (html) => fill(html),
    // public/ 里的文件不走 rollup 的 bundle 图（vite 是原样拷进 dist 的），
    // 所以只能在拷完之后改盘上的那两个文件 —— 和上面 keepDistPlaceholder 一个路子。
    closeBundle() {
      for (const name of publicFiles) {
        const file = fileURLToPath(new URL(`./dist/${name}`, import.meta.url));
        writeFileSync(file, fill(readFileSync(file, "utf8")));
      }
    },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const name = publicFiles.find((f) => (req.url ?? "").split("?")[0] === `/${f}`);
        if (!name) return next();
        res.setHeader("content-type", name.endsWith(".json") ? "application/json" : "text/javascript");
        res.end(fill(readPublic(name)));
      });
    },
  };
}

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

export default defineConfig({
  plugins: [preact(), appName(), keepDistPlaceholder()],
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
