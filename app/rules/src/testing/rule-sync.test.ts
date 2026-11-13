// 两端的条款号正则必须逐字相同（#87 配套，auditor-229 的建议）。
//
// 牌例是两端共用的：Go 端 `server/internal/rules/fixtures_test.go` 的 `ruleRe` 和这边
// `fixtures.ts` 的 `RULE_RE` 只要差一个字符，同一条牌例就会一端过、一端挂。原来靠注释约定
// 和复审时人工比对；这里把两份源码当文本读进来，抽出正则原串直接比 —— Go 改了而 TS 没跟上
// （或者反过来），这条当场红。scripts/local-ci-lib.sh 里，改 Go 那个文件也会触发 local-ci/rules。
//
// 比的是源码里的字面量（`fixtures.ts?raw`），不依赖它的运行时导出。
import { describe, expect, it } from "vitest";
import goSrc from "../../../../server/internal/rules/fixtures_test.go?raw";
import tsSrc from "./fixtures.ts?raw";

/** 从 Go 源码里抽 `var ruleRe = regexp.MustCompile(`...`)` 的原串。 */
export function goRuleRe(src: string): string[] {
  return [...src.matchAll(/var ruleRe = regexp\.MustCompile\(\s*`([^`]*)`\s*\)/g)].map((m) => m[1]);
}

/** 从 TS 源码里抽 `const RULE_RE = /.../flags;` 的正文和标志。 */
export function tsRuleRe(src: string): { source: string; flags: string }[] {
  return [...src.matchAll(/(?:export\s+)?const RULE_RE\s*=\s*\/((?:\\.|[^/\\\n])+)\/([a-z]*)\s*;/g)].map((m) => ({
    source: m[1],
    flags: m[2],
  }));
}

describe("条款号正则：两端逐字相同", () => {
  it("两份源码里各有且只有一处定义（改了名字或写法要来这里同步改抽取规则）", () => {
    expect(goRuleRe(goSrc)).toHaveLength(1);
    expect(tsRuleRe(tsSrc)).toHaveLength(1);
  });

  it("正文逐字相同", () => {
    const [go] = goRuleRe(goSrc);
    const [ts] = tsRuleRe(tsSrc);
    expect(ts.source).toBe(go);
  });

  it("TS 端不加任何标志：u / m / i 都会让语义和 Go 的 RE2 不一样", () => {
    const [ts] = tsRuleRe(tsSrc);
    expect(ts.flags).toBe("");
  });
});

// 抽取规则自己的单测：抽错了的话，上面那条比较就成了「空串等于空串」的假绿。
describe("抽取规则", () => {
  it("Go：跨行写的反引号原串也能抽到", () => {
    const src = "var ruleRe = regexp.MustCompile(\n\t`^第[一二]+条$`)\n";
    expect(goRuleRe(src)).toEqual(["^第[一二]+条$"]);
  });

  it("TS：跨行写的字面量、正文里的转义斜杠都能抽到，标志单独拿出来", () => {
    const src = "const RULE_RE =\n  /^a\\/b( [0-9]+)?$/u;\n";
    expect(tsRuleRe(src)).toEqual([{ source: "^a\\/b( [0-9]+)?$", flags: "u" }]);
  });

  it("两边不一样时比较会红（反证：改一个字符就不等）", () => {
    const go = goRuleRe("var ruleRe = regexp.MustCompile(`^第[一二]+条$`)")[0];
    const ts = tsRuleRe("const RULE_RE = /^第[一二三]+条$/;")[0];
    expect(ts.source).not.toBe(go);
  });
});
