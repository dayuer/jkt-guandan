import { describe, expect, it } from "vitest";
import { LESSONS, doneLessons, markDone } from "./learn";

// 小游戏自己没有 localStorage（shim.ts 才用 wx 的存储垫一层），所以这里显式喂一个假存储 ——
// #253 P6 之后 doneLessons / markDone 的存储是调用方给的，正好能这么测。
function fakeStore() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => {
      m.set(k, v);
    },
    /** 测试用：存进去的原始内容，用来钉存储键和 JSON 形状。 */
    raw: m,
  };
}

describe("学习进度（#253 P6：正文在 app/shared，存储由各端给）", () => {
  it("记过的课就是学过了，重复记一次不会记两遍", () => {
    const s = fakeStore();
    expect(doneLessons(s).size).toBe(0);
    markDone("overview", s);
    markDone("overview", s);
    expect([...doneLessons(s)]).toEqual(["overview"]);
  });

  // 键名和 JSON 形状是**跨版本**的东西（用户手机上已经存着了），改了小游戏的旧进度就读不出来 ——
  // 这里钉死，免得「改键名」这种改动一路绿到底（auditor 复审提的）。
  it("存在 gd_learn_done 里，值是课号数组的 JSON", () => {
    const s = fakeStore();
    markDone("overview", s);
    expect([...s.raw.entries()]).toEqual([["gd_learn_done", '["overview"]']]);
    markDone("dealing", s);
    expect(s.raw.get("gd_learn_done")).toBe('["overview","dealing"]');
  });

  it("拿不到存储（null）时当作没学过，也不抛", () => {
    const s = fakeStore();
    markDone("overview", s);
    expect(doneLessons(s).size).toBe(1);
    expect(doneLessons(null).size).toBe(0);
  });

  it("课本正文是从共享层转出来的", () => {
    expect(LESSONS.length).toBeGreaterThan(0);
  });
});
