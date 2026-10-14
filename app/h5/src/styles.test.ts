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

// vitest 的工作目录是 app/h5/。
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

  // 这几条是 #55 / #56 的直接症状：空座位的样式跑到了页面空状态文字上，反过来
  // 页面空状态的内边距又把空座位撑成了椭圆。
  it("空座位的虚线框只属于 .disc.empty", () => {
    const dashed = rules.filter((r) => /2\.5px dashed var\(--khaki\)/.test(r.body));
    expect(dashed.map((r) => r.selector)).toEqual([".disc.empty"]);
  });

  it("空座位是 64×64 的圆：.disc.empty 显式重置 padding（#55）", () => {
    const disc = rules.find((r) => r.selector === ".disc.empty");
    expect(disc).toBeDefined();
    expect(disc!.body).toMatch(/padding:\s*0/);
  });

  it("页面空状态叫 .empty-state，不带边框、不带背景", () => {
    const empty = rules.find((r) => r.selector === ".empty-state");
    expect(empty).toBeDefined();
    expect(empty!.body).not.toMatch(/border/);
    expect(empty!.body).not.toMatch(/background/);
  });

  it("没有任何样式规则再用裸 .empty 当选择器（只允许 .disc.empty）", () => {
    // 裸 `.empty` 是这次撞名的根源：谁再写一条，就又和空座位绑在一起了。
    const bare = rules.filter((r) => /(^|[\s,>+~])\.empty($|[\s,:{])/.test(r.selector));
    expect(bare.map((r) => r.selector)).toEqual([]);
  });

  it("页面里用的是 .empty-state，没有残留旧类名", () => {
    // 改名的半成品比不改更糟：样式在 .empty-state 上，页面还写着 .empty。
    const files = [
      "src/App.tsx",
      "src/pages/Home.tsx",
      "src/pages/Detail.tsx",
      "src/pages/CreateGame.tsx",
      "src/pages/Privacy.tsx",
      "src/pages/Me.tsx",
      "src/pages/Devices.tsx",
    ];
    const leftover: string[] = [];
    let usages = 0;
    for (const file of files) {
      let text: string;
      try {
        text = readFileSync(resolve(process.cwd(), file), "utf8");
      } catch {
        continue;
      }
      if (/class="empty"/.test(text)) leftover.push(file);
      usages += (text.match(/class="empty-state"/g) ?? []).length;
    }
    expect(leftover).toEqual([]);
    expect(usages).toBeGreaterThan(0);
  });
});

describe("视口高度（#130 第 2 条）", () => {
  it("body 的最小高度用 100dvh：安卓 Chrome 的 100vh 比可视区域高，短列表也能滚出一截", () => {
    const body = rules.find((r) => r.selector === "body" && /min-height/.test(r.body));
    expect(body?.body).toMatch(/min-height:\s*100dvh/);
  });

  it("viewport 声明 interactive-widget=resizes-content：键盘弹出时贴底的弹层跟着上移", () => {
    const html = readFileSync(resolve(process.cwd(), "index.html"), "utf8");
    expect(html).toMatch(/name="viewport"[^>]*interactive-widget=resizes-content/);
  });
});

describe("输入框字号（P0 键盘问题）", () => {
  const SMALL = /var\(--fs-(15|body|13|small|tiny)\)|1[0-5]px/;

  it("所有输入框默认至少 16px（--fs-h3）：iOS 小于 16px 会在聚焦时放大页面", () => {
    const base = rules.find((r) => /^input,\s*textarea,\s*select$/.test(r.selector));
    expect(base?.body).toMatch(/font-size:\s*var\(--fs-h3\)/);
  });

  it("没有哪条规则把输入框的字号改回 16px 以下", () => {
    const offenders = rules.filter(
      (r) => /\b(input|textarea|select)\b/.test(r.selector) && /font-size:\s*[^;]+/.test(r.body) &&
        SMALL.test(/font-size:\s*([^;]+)/.exec(r.body)![1]),
    );
    expect(offenders.map((r) => r.selector)).toEqual([]);
  });
});
