#!/usr/bin/env node
// 共享层守门（#253）：`app/shared/src` 里**文件之间**只许 `import type`，值 import 一律不许。
//
// 为什么要有这道门：`tsc` 和 `isolatedModules` 都拦不住值 import —— 它编译得过、测试也全绿，
// 但共享层就在运行期互相牵连了：
//   - 两端各自打包时，会把「都是共享的」当成「都可以进来」，一端的需要拖进另一端的产物；
//   - 更要紧的是**顶层求值**：小游戏的 vitest 跑在 node 里、没有 `window`，
//     共享层里任何一句顶层浏览器 API，只要它在 import 链上，import 那一刻就整片炸
//     （不是等到调用才炸）。类型 import 编译后擦掉，链就走不到那里。
//
// 用法：node scripts/check-shared-imports.mjs（`make shared-check` 里调用）。
// 规矩与理由的正文见 app/shared/README.md。

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const dir = path.join(root, "app/shared/src");

/** 去掉注释，但字符串里的内容原样留着（`https://…` 里的 `//` 不是注释）。 */
function stripComments(text) {
  let out = "";
  let mode = null; // null | "'" | '"' | "`" | "//" | "/*"
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    const next = text[i + 1];
    if (mode === null) {
      if (c === "/" && next === "/") { mode = "//"; i++; continue; }
      if (c === "/" && next === "*") { mode = "/*"; i++; continue; }
      if (c === "'" || c === '"' || c === "`") { mode = c; out += c; continue; }
      out += c;
    } else if (mode === "//") {
      if (c === "\n") { mode = null; out += c; }
    } else if (mode === "/*") {
      if (c === "\n") out += c; // 保留换行，行号才对得上
      if (c === "*" && next === "/") { mode = null; i++; }
    } else { // 在字符串里
      if (c === "\\") { out += c + (next ?? ""); i++; continue; }
      if (c === mode) mode = null;
      out += c;
    }
  }
  return out;
}

/** 这段从句是不是「全是类型」：`type {…}`，或者 `{ type A, type B }`。 */
function isTypeOnlyClause(clause) {
  const c = clause.trim();
  if (/^type\s/.test(c)) return true;
  const m = c.match(/^\{([\s\S]*)\}$/);
  if (!m) return false; // 默认导入 / `* as ns` 都是值
  const specifiers = m[1].split(",").map((s) => s.trim()).filter(Boolean);
  return specifiers.length > 0 && specifiers.every((s) => /^type\s/.test(s));
}

const files = fs.readdirSync(dir).filter((f) => f.endsWith(".ts")).sort();
const bad = [];
let checked = 0;

// 语句可能跨行，所以整份文件一起扫，再按匹配位置换算行号。
const FROM_RE = /(^|[\s;}])(import|export)\s+([^;]*?)\bfrom\s*(["'])(\.[^"']*)\4/g;
const DYNAMIC_RE = /(^|[\s;=(])import\s*\(\s*(["'])(\.[^"']*)\2\s*\)/g;

for (const f of files) {
  const raw = fs.readFileSync(path.join(dir, f), "utf8");
  const text = stripComments(raw);
  const lineAt = (i) => text.slice(0, i).split("\n").length;

  for (const m of text.matchAll(FROM_RE)) {
    checked++;
    const [, , keyword, clause, , spec] = m;
    if (keyword === "export") {
      // `export type { … } from` / `export type * from` 编译后擦掉，是可以的；
      // `export * from` / `export { 值 } from` 是值依赖，不行。
      if (isTypeOnlyClause(clause)) continue;
      bad.push({ file: f, line: lineAt(m.index), text: m[0].trim(), why: "`export … from` 是值依赖（`export type … from` 才行），把值搬到需要它的那一端" });
      continue;
    }
    if (isTypeOnlyClause(clause)) continue;
    bad.push({ file: f, line: lineAt(m.index), text: m[0].trim(), why: "改成 `import type { … }`，或者把值搬到需要它的那一端" });
  }
  for (const m of text.matchAll(DYNAMIC_RE)) {
    if (m[3] === undefined) continue;
    checked++;
    bad.push({ file: f, line: lineAt(m.index), text: m[0].trim(), why: "动态 import 一定是值依赖" });
  }
}

if (bad.length > 0) {
  console.error("共享层守门失败：app/shared/src 里文件之间只许 `import type`（值 import 不许）。");
  console.error("（规矩和理由见 app/shared/README.md）\n");
  for (const b of bad) {
    console.error(`  app/shared/src/${b.file}:${b.line}`);
    console.error(`    ${b.text}`);
    console.error(`    → ${b.why}\n`);
  }
  process.exit(1);
}

console.log(`共享层守门通过：${files.length} 个文件，${checked} 条相对 import 全是 import type。`);
