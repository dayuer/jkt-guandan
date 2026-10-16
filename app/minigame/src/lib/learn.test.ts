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
