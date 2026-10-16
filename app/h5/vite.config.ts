import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import preact from "@preact/preset-vite";
import { defineConfig, type Plugin } from "vitest/config";

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
  plugins: [preact(), keepDistPlaceholder()],
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
