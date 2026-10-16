#!/usr/bin/env node
/**
 * 共享层守门（#253）：`app/shared/src` 里**文件之间**只许 `import type`（以及 `export type … from`），
 * 值 import / 值 re-export / `require` / 动态 import / npm 包一律不许。
 *
 * 为什么要有这道门：`tsc` 和 `isolatedModules` 都拦不住值 import —— 它编译得过、测试也全绿，
 * 但共享层就在运行期互相牵连了：
 *   - 两端各自打包时，会把「都是共享的」当成「都可以进来」，一端的需要拖进另一端的产物；
 *   - 更要紧的是**顶层求值**：小游戏的 vitest 跑在 node 里、没有 `window`，
 *     共享层里任何一句顶层浏览器 API，只要它在 import 链上，import 那一刻就整片炸
 *     （不是等到调用才炸）。类型 import 编译后擦掉，链就走不到那里。
 * 副作用 import（`import "./store"`）是这里面最纯的一种耦合：它**只为**跑对方的顶层代码。
 *
 * 实现用的是 TypeScript 编译器 API，不是正则。正则版本被审出过一串漏网：副作用 import、
 * 不写分号时 `export type Foo = string` 下一行的值 import、`import x = require()`、`require()`，
 * 报错行号还会偏一行。
 *
 * 编译器**写死取 app/minigame 那份**（它的 devDependency 里有 `typescript ^5.6`）：
 * app/h5 装的是 typescript 7（native 版），**没有 JS API** —— `createSourceFile` 是 undefined。
 *
 * 用法：node scripts/check-shared-imports.mjs [要扫的目录，默认 app/shared/src]
 * 退出码：0 通过；1 有违规；2 环境不对（找不到编译器 / 目标目录不存在）。
 * 规矩和理由的正文见 app/shared/README.md。
 */

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const SOURCE_EXTENSIONS = [".ts", ".tsx", ".mts", ".cts"];

let tsCache = null;
/** 找一个**带 JS API** 的 typescript：app/h5 那份是 native 版，没有 createSourceFile，跳过。 */
function loadTypeScript() {
  if (tsCache) return tsCache;
  for (const pkg of ["app/minigame/package.json", "app/rules/package.json"]) {
    try {
      const require = createRequire(path.join(root, pkg));
      const ts = require("typescript");
      if (typeof ts.createSourceFile === "function") {
        tsCache = ts;
        return ts;
      }
    } catch {
      // 这个包装没装，试下一个
    }
  }
  return null;
}

/** 递归收集要扫的文件；跳过 .d.ts（声明文件里的 import 本身就擦掉，没有运行期含义）。 */
function collect(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules") continue;
      out.push(...collect(full));
      continue;
    }
    if (entry.name.endsWith(".d.ts")) continue;
    if (SOURCE_EXTENSIONS.includes(path.extname(entry.name))) out.push(full);
  }
  return out;
}

function scriptKindOf(ts, file) {
  if (file.endsWith(".tsx")) return ts.ScriptKind.TSX;
  return ts.ScriptKind.TS;
}

const isRelative = (spec) => spec.startsWith("./") || spec.startsWith("../");

/** 从句是不是纯类型：`import { type A, type B }`（每个都带 type，且没有默认导入）。 */
function namedBindingsAreTypeOnly(ts, clause) {
  const nb = clause.namedBindings;
  if (!nb || !ts.isNamedImports(nb) || nb.elements.length === 0) return false;
  return clause.name === undefined && nb.elements.every((e) => e.isTypeOnly);
}

function checkFile(ts, file, violations) {
  const text = fs.readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, scriptKindOf(ts, file));
  const lineText = (line) => (text.split("\n")[line - 1] ?? "").trim();

  if (sf.parseDiagnostics.length > 0) {
    const d = sf.parseDiagnostics[0];
    const at = sf.getLineAndCharacterOfPosition(d.start);
    violations.push({
      file,
      line: at.line + 1,
      column: at.character + 1,
      lineText: lineText(at.line + 1),
      why: `这个文件解析不了（${ts.flattenDiagnosticMessageText(d.messageText, " ")}）—— 守门扫不动，先修它`,
    });
    return;
  }

  const report = (node, why) => {
    const at = sf.getLineAndCharacterOfPosition(node.getStart(sf));
    violations.push({ file, line: at.line + 1, column: at.character + 1, lineText: lineText(at.line + 1), why });
  };

  // 相对路径 → 值依赖的说明；裸模块名 → npm 包那条说明
  const checkSpecifier = (specNode, whyRelative) => {
    report(specNode, isRelative(specNode.text) ? whyRelative : "共享层不引 npm 包（零运行期依赖）");
  };

  const visit = (node) => {
    if (ts.isImportDeclaration(node)) {
      const specNode = node.moduleSpecifier;
      if (!ts.isStringLiteral(specNode)) {
        report(specNode, "import 的模块名不是字符串字面量，守门看不出来 —— 共享层别这么写");
        return;
      }
      if (node.importClause === undefined) {
        checkSpecifier(specNode, "副作用 import：它只是为了跑对方模块的顶层代码，正是这道门要拦的");
        return;
      }
      if (node.importClause.isTypeOnly || namedBindingsAreTypeOnly(ts, node.importClause)) {
        // 纯类型：相对路径随便用；npm 包也放行（擦得掉，而且 app/shared 没有 node_modules，
        // 真写 `import type … from "vitest"` 时 tsc -p app/shared 会先报 TS2307）。
        return;
      }
      checkSpecifier(specNode, "改成 `import type { … }`，或者把值搬到需要它的那一端");
      return;
    }

    if (ts.isExportDeclaration(node) && node.moduleSpecifier) {
      const specNode = node.moduleSpecifier;
      if (!ts.isStringLiteral(specNode)) {
        report(specNode, "export 的模块名不是字符串字面量，守门看不出来 —— 共享层别这么写");
        return;
      }
      const namedTypeOnly =
        node.exportClause && ts.isNamedExports(node.exportClause)
          ? node.exportClause.elements.length > 0 && node.exportClause.elements.every((e) => e.isTypeOnly)
          : false;
      if (node.isTypeOnly || namedTypeOnly) return;
      checkSpecifier(specNode, "`export … from` 是值依赖（`export type … from` 才行），把值搬到需要它的那一端");
      return;
    }

    if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference)) {
      const ref = node.moduleReference.expression;
      if (ref && ts.isStringLiteral(ref) && !node.isTypeOnly) {
        checkSpecifier(ref, "`import x = require(…)` 是值依赖，共享层里别用");
      }
      return;
    }

    if (ts.isCallExpression(node)) {
      if (node.expression.kind === ts.SyntaxKind.ImportKeyword) {
        const arg = node.arguments[0];
        if (arg && ts.isStringLiteral(arg)) checkSpecifier(arg, "动态 import 一定是值依赖");
        return;
      }
      if (ts.isIdentifier(node.expression) && node.expression.text === "require") {
        const arg = node.arguments[0];
        if (arg && ts.isStringLiteral(arg)) checkSpecifier(arg, "`require(…)` 一定是值依赖");
        return;
      }
    }

    // 注意：`type A = import("./x").Foo` 在 AST 里是 ImportTypeNode，不是 CallExpression，
    // 编译后擦得掉，所以照上面这条路不会被误报。

    ts.forEachChild(node, visit);
  };

  visit(sf);
}

/**
 * 扫一个目录。返回 { code, stdout, stderr }：
 *   code 0 = 通过，1 = 有违规，2 = 环境不对（找不到编译器 / 目录不存在）。
 * 拆成函数是为了让自测能直接在进程内跑（每个用例起一次 node + 加载编译器要 3 秒，34 个用例受不了）。
 */
export function runCli(dir, { cwd = root } = {}) {
  const rel = (p) => path.relative(cwd, p) || p;

  if (!fs.existsSync(dir)) {
    return { code: 2, stdout: "", stderr: `共享层守门：${rel(dir)} 不存在\n` };
  }
  const ts = loadTypeScript();
  if (!ts) {
    return {
      code: 2,
      stdout: "",
      stderr:
        "共享层守门：找不到带 JS API 的 typescript 编译器。\n" +
        "先在 app/minigame（或 app/rules）里 npm ci，或者跑 make shared-check（它会先装依赖）。\n",
    };
  }

  const files = collect(dir);
  const violations = [];
  for (const file of files) checkFile(ts, file, violations);

  if (violations.length > 0) {
    const lines = [
      "共享层守门失败：app/shared/src 里文件之间只许 `import type`（值 import 不许）。",
      "（规矩和理由见 app/shared/README.md）",
      "",
    ];
    for (const v of violations) {
      lines.push(`  ${rel(v.file)}:${v.line}:${v.column}`);
      if (v.lineText) lines.push(`    ${v.lineText}`);
      lines.push(`    → ${v.why}`, "");
    }
    lines.push(`共 ${violations.length} 处，扫了 ${files.length} 个文件。`);
    return { code: 1, stdout: "", stderr: lines.join("\n") + "\n" };
  }

  return {
    code: 0,
    stdout: `共享层守门通过：扫了 ${files.length} 个文件，没有值 import / 值 re-export / npm 包。\n`,
    stderr: "",
  };
}

// 直接跑这个文件时才是 CLI（被 import 时不执行，自测要用上面那个函数）。
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const target = process.argv[2] ? path.resolve(process.argv[2]) : path.join(root, "app/shared/src");
  const { code, stdout, stderr } = runCli(target);
  if (stdout) process.stdout.write(stdout);
  if (stderr) process.stderr.write(stderr);
  process.exit(code);
}
