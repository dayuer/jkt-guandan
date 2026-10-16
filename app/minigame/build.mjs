// 打包小游戏：src/main.ts → game.js（开发者工具直接读这个文件）。
//   node build.mjs           正式版，连 https://guandan.synon.ai
//   node build.mjs --dev     开发版，连本机 GUANDAN_DEV=1 的服务端，用 dev: 登录码
//   加 --watch 改了自动重打
// 复用的纯逻辑已经全部搬进 app/shared（#253），小游戏对 app/h5 的 import 是 0 ——
// 所以原来那个「打包时把 H5 模块里的 ./format 换成 src/lib/fmt.ts」的插件（安卓微信不一定有
// Intl 时区）再也没有输入了，删掉；时间写法由 app/shared 的调用方注入，小游戏用 src/lib/fmt.ts。
import * as esbuild from "esbuild";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const dev = process.argv.includes("--dev");
const watch = process.argv.includes("--watch");
const selftest = process.argv.includes("--selftest");
// 自检往哪台收日志（只在 --selftest 包里用）。默认 18675；**改端口是为了不和别的会话
// 同时跑的自检撞车** —— 两个会话各跑一个接收端时，端口一样就会把对方的日志/截图收走。
// 用法：SELFTEST_SINK_PORT=18676 node build.mjs --dev --selftest
const sinkPort = Number(process.env.SELFTEST_SINK_PORT || 18675);
// 走查场景：SELFTEST_SCENARIO=165 时编进 #165 验收的那套步骤（默认跑原来的切 Tab 自检）。
const scenario = process.env.SELFTEST_SCENARIO || "";
const apiBase = process.env.API_BASE || (dev ? "http://127.0.0.1:8080" : "https://guandan.synon.ai");

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
  define: {
    __API_BASE__: JSON.stringify(apiBase),
    __DEV__: String(dev),
    __SELFTEST__: String(selftest),
    __SELFTEST_SINK__: JSON.stringify(`http://127.0.0.1:${sinkPort}/`),
    __SELFTEST_SCENARIO__: JSON.stringify(scenario),
  },
  logLevel: "info",
});
if (watch) await ctx.watch();
else { await ctx.rebuild(); await ctx.dispose(); }
