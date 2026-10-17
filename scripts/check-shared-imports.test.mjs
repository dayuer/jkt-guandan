// 共享层守门（scripts/check-shared-imports.mjs）的自测（#253）。
//
// 复审那轮把正则版本打穿过一串写法（副作用 import、不写分号时被 type alias 藏住、
// `import = require()`、`require()`、行号偏一行、只扫顶层 *.ts），所以这里把那些写法
// 全部做成探针：**该红的必须红，不该红的必须不红**。
//
// 跑法：node --test scripts/check-shared-imports.test.mjs（make shared-check 里有一条）。
// 用的是进程内的 runCli（起一次 node + 加载编译器要 3 秒，每个用例起一次受不了）。

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { runCli } from "./check-shared-imports.mjs";

const script = fileURLToPath(new URL("./check-shared-imports.mjs", import.meta.url));
const repoRoot = path.resolve(path.dirname(script), "..");

/** 把 files 写进一个临时目录，拿它当共享层 src 跑一次守门。 */
function runGuard(files) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "shared-guard-"));
  try {
    for (const [name, content] of Object.entries(files)) {
      const full = path.join(dir, name);
      fs.mkdirSync(path.dirname(full), { recursive: true });
      fs.writeFileSync(full, content);
    }
    return runCli(dir, { cwd: repoRoot });
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const one = (src) => ({ "a.ts": src });

// 该红的写法：必须退出码 1，且报错里提到那个模块名（证明是它被逮住的）
const RED = [
  ["副作用 import（带分号）", one('import "./store";\n'), "./store"],
  ["副作用 import（不带分号）", one('import "./store"\n'), "./store"],
  ["具名值 import", one('import { x } from "./store";\nexport const v = x;\n'), "./store"],
  ["默认导入", one('import D from "./store";\n'), "./store"],
  ["命名空间导入", one('import * as m from "./store";\n'), "./store"],
  ["混合（type + 值）", one('import { type A, B } from "./store";\nexport const v = B;\nexport type X = A;\n'), "./store"],
  ["默认 + type", one('import D, { type A } from "./store";\n'), "./store"],
  ["多行混合", one('import {\n  type A,\n  B,\n} from "./store";\nexport const v = B;\n'), "./store"],
  ["export { x } from", one('export { x } from "./store";\n'), "./store"],
  ["export * from", one('export * from "./store";\n'), "./store"],
  ["export * as ns from", one('export * as ns from "./store";\n'), "./store"],
  ["import x = require()", one('import x = require("./store");\n'), "./store"],
  ["require()", one('const y = require("./store");\n'), "./store"],
  ["动态 import", one('export const p = import("./store");\n'), "./store"],
  ["await 动态 import", one('export const p = await import("./store");\n'), "./store"],
  ["../ 开头的相对路径", one('import { x } from "../shared/store";\n'), "../shared/store"],
  ["不写分号时被 type alias 藏住", one('export type Foo = string\nimport { x } from "./store"\nexport const v = x;\n'), "./store"],
  ["npm 包（值）", one('import { describe } from "vitest";\n'), "vitest"],
  ["npm 包（require）", one('const v = require("vitest");\n'), "vitest"],
  ["子目录里的文件", { "nested/deep.ts": 'import { x } from "./store";\n' }, "./store"],
  [".tsx 文件", { "view.tsx": 'import { x } from "./store";\nexport const V = () => x;\n' }, "./store"],
  [".mts 文件", { "mod.mts": 'import { x } from "./store";\n' }, "./store"],
  [".js 文件", { "legacy.js": 'import { x } from "./store";\nexport const v = x;\n' }, "./store"],
  ["非字面量的动态 import", one("export const p = import(name);\n"), "不是字符串字面量"],
  ["非字面量的 require", one("export const p = require(name);\n"), "不是字符串字面量"],
  ["非字面量的 import = require", one("import x = require(name);\n"), "不是字符串字面量"],
];

for (const [name, files, specifier] of RED) {
  test(`该红：${name}`, () => {
    const r = runGuard(files);
    assert.equal(r.code, 1, `期望退出码 1，实际 ${r.code}\n${r.stdout}${r.stderr}`);
    assert.ok(r.stderr.includes(specifier), `报错里没提到 ${specifier}：\n${r.stderr}`);
  });
}

// 不该红的写法
const GREEN = [
  ["import type", one('import type { A } from "./store";\nexport const x: A | null = null;\n')],
  ["具名里每个都带 type", one('import { type A, type B } from "./store";\nexport type X = A | B;\n')],
  ["多行 import type", one('import type {\n  A,\n  B,\n} from "./store";\nexport type X = A | B;\n')],
  ["export type … from", one('export type { A } from "./store";\n')],
  ["export type * from", one('export type * from "./store";\n')],
  ["export { type A } from", one('export { type A } from "./store";\n')],
  ["type 位置上的 import()", one('export type A = import("./store").Foo;\n')],
  ["字符串 / 模板串 / 注释里的 import 文本", one('export const s = \'import { x } from "./store"\'; // import { y } from "./store"\nexport const t = `require("./store")`;\n/* import "./store" */\n')],
  ["字符串里的 //（不是注释）", one('export const url = "https://guandan.id/g/8K2QXM"; // 这才是注释\n')],
  ["npm 包的 import type（擦得掉；真写出来 tsc 会先报 TS2307）", one('import type { A } from "vitest";\nexport type X = A;\n')],
  ["没有 import 的文件", one("export const n = 1;\n")],
  [".d.ts 声明文件（里面的 import 本来就擦掉）", { "types.d.ts": 'import { x } from "./store";\nexport declare const y: typeof x;\n' }],
];

for (const [name, files] of GREEN) {
  test(`不该红：${name}`, () => {
    const r = runGuard(files);
    assert.equal(r.code, 0, `期望通过，实际 ${r.code}\n${r.stdout}${r.stderr}`);
  });
}

test("行号列号报的是出问题那条 import 的模块名（不是上一行）", () => {
  // 第 6 行那条 import 的 "./store" 从第 19 列开始
  const src =
    "// 1\n// 2\n\n// 4\nimport type { A } from \"./ok\";\nimport { x } from \"./store\";\nexport const v = x;\nexport type Y = A;\n";
  const r = runGuard(one(src));
  assert.equal(r.code, 1);
  assert.ok(r.stderr.includes("a.ts:6:19"), `行号/列不对：\n${r.stderr}`);
});

test("解析不了的文件直接红，不静默放过", () => {
  const r = runGuard(one('import { from "./store";\n'));
  assert.equal(r.code, 1, `期望退出码 1，实际 ${r.code}\n${r.stdout}${r.stderr}`);
  assert.ok(r.stderr.includes("解析不了"), r.stderr);
});

test("目录不存在时是环境错误（退出码 2）", () => {
  const r = runCli("/definitely/not/here", { cwd: repoRoot });
  assert.equal(r.code, 2);
});

test("CLI 跑真仓库时通过（含退出码这条契约）", () => {
  const out = execFileSync(process.execPath, [script], { encoding: "utf8" });
  assert.ok(out.includes("共享层守门通过"), out);
});

test("真仓库里那棵树是干净的", () => {
  const r = runCli(path.join(repoRoot, "app/shared/src"), { cwd: repoRoot });
  assert.equal(r.code, 0, r.stderr);
  // 不写死文件数：共享层加一个文件（比如产品名的常量）不该让这条自测红。
  // 只要求它真扫到了东西 —— 扫 0 个文件说明守门等于没跑，那才是真问题。
  const scanned = Number(/扫了 (\d+) 个文件/.exec(r.stdout)?.[1] ?? "0");
  assert.ok(scanned > 0, `守门一个文件都没扫到：${r.stdout}`);
});
