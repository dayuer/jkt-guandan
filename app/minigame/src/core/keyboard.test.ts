import { describe, expect, it } from "vitest";

import { benignKeyboardFail, keyboardAction } from "./app";

// 真机反馈（#165 验收）：点输入框会弹「键盘没拉起来」的错误提示。
// 原因是 `wx.showKeyboard` **键盘已经开着的时候再调会失败**，而两个字段的调用点
// （`core/widgets.ts` 的 `field()`、`scenes/create.ts` 的 `focus()`）每次点都重新 show。
// 点第一个字段、再点第二个，或者同一个字段点两下，都会走到那条。
// 真机上没法单测 showKeyboard 的成败，所以把「该怎么动」抽成纯函数测。
describe("keyboardAction：点输入框时该怎么动键盘", () => {
  it("键盘没开 → 正常拉起来", () => {
    expect(keyboardAction({ open: false, sameKey: false }, true)).toBe("show");
    expect(keyboardAction({ open: false, sameKey: true }, true)).toBe("show");
  });

  it("同一个字段再点一下 → 什么都不做（键盘本来就在）", () => {
    expect(keyboardAction({ open: true, sameKey: true }, true)).toBe("none");
    // 没有 updateKeyboard 也一样：已经在这个字段上，不是「没有键盘」
    expect(keyboardAction({ open: true, sameKey: true }, false)).toBe("none");
  });

  it("**换字段**、键盘开着 → update，不要重新 show（真机就是这条 fail 的）", () => {
    expect(keyboardAction({ open: true, sameKey: false }, true)).toBe("update");
  });

  it("换字段但基础库没有 updateKeyboard → 退回 show（老基础库上没法更好了）", () => {
    expect(keyboardAction({ open: true, sameKey: false }, false)).toBe("show");
  });
});

describe("benignKeyboardFail：哪些失败信息不算错误", () => {
  it("「已经开着」不报错给用户", () => {
    expect(benignKeyboardFail("showKeyboard:fail already show")).toBe(true);
    expect(benignKeyboardFail("showKeyboard:fail 键盘已经拉起")).toBe(true);
  });

  it("真的失败照样报", () => {
    expect(benignKeyboardFail("showKeyboard:fail not support")).toBe(false);
    expect(benignKeyboardFail("")).toBe(false);
    expect(benignKeyboardFail(undefined as unknown as string)).toBe(false);
  });
});
