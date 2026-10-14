import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 样式表的完整性检查。
 *
 * 起因（#55、#56）：往 styles.css 里插一段新样式时，锚点选的是 `.empty {`，
 * 而它在文件里第一次出现的位置是 **`.disc.empty {` 的中间**——于是那条规则被劈成两半：
 * 前面留下一个没带块的裸 `.disc`，后面多出一条 `.empty`（空座位的虚线框样式）
 * 反而套到了所有空状态文字上。CSS 语法完全合法，肉眼和构建都不会报错，
 * 只有截图才能看出来。所以这里盯着几条不变量。
 */

// vitest 的工作目录是 web/。
const css = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8");

/** 去掉注释，只看规则本身。 */
function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, "");
}

interface Rule {
  selector: string;
  body: string;
}

/** 把样式表切成顶层规则（at-rule 里的嵌套块原样留在 body 里）。 */
function topLevelRules(text: string): Rule[] {
  const rules: Rule[] = [];
  let i = 0;
  while (i < text.length) {
    const open = text.indexOf("{", i);
    if (open < 0) break;
    const selector = text.slice(i, open);
    // 找到与它配对的 }
    let depth = 1;
    let j = open + 1;
    while (j < text.length && depth > 0) {
      if (text[j] === "{") depth++;
      else if (text[j] === "}") depth--;
      j++;
    }
    rules.push({ selector: selector.trim(), body: text.slice(open + 1, j - 1) });
    i = j;
  }
  return rules;
}

const rules = topLevelRules(stripComments(css));

describe("styles.css 的完整性", () => {
  it("大括号是配平的", () => {
    const text = stripComments(css);
    const opens = (text.match(/\{/g) ?? []).length;
    const closes = (text.match(/\}/g) ?? []).length;
    expect(opens).toBe(closes);
  });

  it("没有规则被从中间截断（选择器不会莫名其妙地跨行拼接）", () => {
    const broken: string[] = [];
    for (const rule of rules) {
      // 本项目的写法是「选择器一行一条，多行时用逗号结尾」。所以除最后一行外，
      // 每一行都要以逗号结尾——不然就是有东西插进了这条规则中间（#56）。
      const lines = rule.selector.split("\n").filter((l) => l.trim() !== "");
      for (const line of lines.slice(0, -1)) {
        if (!line.trimEnd().endsWith(",")) {
          broken.push(`${line.trim()} → ${rule.selector.split("\n")[0].trim()}`);
        }
      }
      // 样式规则的块里再出现 { 也是同类问题（at-rule 例外：@media / @supports 里面本来就能嵌）。
      if (!rule.selector.startsWith("@") && rule.body.includes("{")) {
        broken.push(`块里嵌了块：${rule.selector.split("\n")[0].trim()}`);
      }
    }
    expect(broken).toEqual([]);
  });

  // 这两条是 #56 的直接症状：空座位的虚线框跑到了所有空状态文字上。
  it("空座位的虚线框只属于 .disc.empty", () => {
    const dashed = rules.filter((r) => /2\.5px dashed var\(--khaki\)/.test(r.body));
    expect(dashed.map((r) => r.selector)).toEqual([".disc.empty"]);
  });

  it("空状态文字（.empty）不带边框、不带背景", () => {
    const empty = rules.find((r) => r.selector === ".empty");
    expect(empty).toBeDefined();
    expect(empty!.body).not.toMatch(/border/);
    expect(empty!.body).not.toMatch(/background/);
  });
});
