import { describe, expect, it } from "vitest";

import { benignKeyboardFail, keyboardAction, keyboardOpen, keyboardSettingsMatch } from "./app";

// 真机反馈（#165 验收）：点输入框会弹「键盘没拉起来」。
// 根因是 `wx.showKeyboard` **键盘已经开着的时候再调会失败**，而两个调用点
// （`core/widgets.ts` 的 `field()`、`scenes/create.ts` 的 `focus()`）每次点都重新 show。
// 真机上没法单测 showKeyboard 的成败，所以把「该怎么动」抽成纯函数测。
describe("keyboardAction：点输入框时该怎么动键盘", () => {
  const st = (o: Partial<{ open: boolean; sameKey: boolean; settingsMatch: boolean }>) =>
    ({ open: false, sameKey: false, settingsMatch: true, ...o });

  it("键盘没开 → 正常拉起来", () => {
    expect(keyboardAction(st({}), true)).toBe("show");
    expect(keyboardAction(st({ sameKey: true }), true)).toBe("show");
  });

  it("同一个字段再点一下 → 什么都不做（键盘本来就在）", () => {
    expect(keyboardAction(st({ open: true, sameKey: true }), true)).toBe("none");
    expect(keyboardAction(st({ open: true, sameKey: true }), false)).toBe("none");
  });

  it("换字段、键盘开着、设置一样 → update（真机就是这条原来 fail 的）", () => {
    expect(keyboardAction(st({ open: true }), true)).toBe("update");
  });

  it("换字段但**设置不一样** → reshow（updateKeyboard 只换文字，换不了 maxLength/multiple）", () => {
    // 开一局页：地点（40 字、单行、下一项）→ 备注（200 字、多行）。
    // 走 update 的话备注会被限成 40 字单行 —— 所以必须收起再拉。
    expect(keyboardAction(st({ open: true, settingsMatch: false }), true)).toBe("reshow");
  });

  it("换字段但基础库没有 updateKeyboard → 也 reshow（不能串字段）", () => {
    expect(keyboardAction(st({ open: true }), false)).toBe("reshow");
  });

  it("键盘收起之后再点同一个字段 → 必须重新 show", () => {
    // 收起之后 `open` 就是 false（见 keyboardOpen），于是走 show。
    expect(keyboardAction(st({ open: false, sameKey: true }), true)).toBe("show");
  });
});

describe("keyboardOpen：键盘到底开没开", () => {
  it("自己记的开着、高度也有 → 开着", () => {
    expect(keyboardOpen(true, 336)).toBe(true);
  });

  it("**高度归零就当没开** —— kbOpen 卡在 true 时（切后台、安卓返回键、没触发 complete）全靠这条救回来", () => {
    expect(keyboardOpen(true, 0)).toBe(false);
  });

  it("没标记开着 → 没开", () => {
    expect(keyboardOpen(false, 336)).toBe(false);
  });
});

describe("keyboardSettingsMatch：设置一不一样（updateKeyboard 换不了这些）", () => {
  const o = (x: Partial<{ maxLength: number; multiple: boolean; confirmType: "done" | "next" }>) => ({
    key: "k", value: "", onInput: () => {}, maxLength: 40, multiple: false, confirmType: "done" as const, ...x,
  });

  it("三项都一样 → 一样（省略的按默认算）", () => {
    expect(keyboardSettingsMatch(o({}), o({ maxLength: 40, confirmType: "done" }))).toBe(true);
  });

  it("字数上限不一样 → 不一样（地点 40 / 备注 200）", () => {
    expect(keyboardSettingsMatch(o({}), o({ maxLength: 200 }))).toBe(false);
  });

  it("多行不一样 → 不一样", () => {
    expect(keyboardSettingsMatch(o({}), o({ multiple: true }))).toBe(false);
  });

  it("回车键不一样 → 不一样（地点是「下一项」）", () => {
    expect(keyboardSettingsMatch(o({}), o({ confirmType: "next" }))).toBe(false);
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
