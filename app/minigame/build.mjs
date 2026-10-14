// 打包小游戏：src/main.ts → game.js（开发者工具直接读这个文件）。
//   node build.mjs           正式版，连 https://guandan.synon.ai
//   node build.mjs --dev     开发版，连本机 GUANDAN_DEV=1 的服务端，用 dev: 登录码
//   加 --watch 改了自动重打
// 复用 app/h5/src 里的纯逻辑；其中 format.ts 依赖 Intl 时区（安卓微信不一定有），换成 src/lib/fmt.ts。
import * as esbuild from "esbuild";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const h5 = path.resolve(here, "../h5/src");
const dev = process.argv.includes("--dev");
const watch = process.argv.includes("--watch");
const selftest = process.argv.includes("--selftest");
const apiBase = process.env.API_BASE || (dev ? "http://127.0.0.1:8080" : "https://guandan.synon.ai");

const swapFormat = {
  name: "swap-h5-format",
  setup(b) {
    b.onResolve({ filter: /^\.\/format$/ }, (args) =>
      args.importer.startsWith(h5) ? { path: path.join(here, "src/lib/fmt.ts") } : undefined,
    );
  },
};

const ctx = await esbuild.context({
  entryPoints: [path.join(here, "src/main.ts")],
  outfile: path.join(here, "game.js"),
  bundle: true,
  format: "iife",
  target: "es2017",
  charset: "utf8",
  sourcemap: dev ? "inline" : false,
  minify: !dev,
  legalComments: "none",
  define: { __API_BASE__: JSON.stringify(apiBase), __DEV__: String(dev), __SELFTEST__: String(selftest) },
  plugins: [swapFormat],
  logLevel: "info",
});
if (watch) await ctx.watch();
else { await ctx.rebuild(); await ctx.dispose(); }
