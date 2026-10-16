import { describe, expect, it } from "vitest";

import {
  benignKeyboardFail,
  keyboardAction,
  keyboardOpen,
  keyboardSettingsMatch,
  KeyboardSession,
  HIDE_FALLBACK_MS,
  type EditOptions,
  type KeyboardApi,
} from "./keyboard";

// 真机反馈（#165 验收）：点输入框会弹「键盘没拉起来」。
// 根因是 `wx.showKeyboard` **键盘已经开着的时候再调会失败**，而两个调用点
// （`core/widgets.ts` 的 `field()`、`scenes/create.ts` 的 `focus()`）每次点都重新 show。

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

// ── 下面是复审要的那一部分：假的 wx，把**事件顺序**三种都试一遍 ──────────────────
//
// 纯函数测不到这里出过的错（#209 第一次复审发现的串字段），因为错全在回调时序上：
// 「收起旧键盘」和「拉起新键盘」之间的事件顺序不定。所以造一个能手动派发事件的假 wx。

const open = (o: Partial<EditOptions> & { key: string }) => ({
  value: "", maxLength: 40, multiple: false, confirmType: "done" as const, onInput: () => {}, ...o,
});

type FakeOpts = { updateKeyboard?: boolean; failHide?: boolean };

function fakeWx(opts: FakeOpts = {}) {
  const handlers: Record<string, (r: any) => void> = {};
  const calls: string[] = [];
  let shown: any = null;
  const api: KeyboardApi = {
    showKeyboard(o: any) { calls.push("show:" + o.defaultValue); shown = o; o.success?.({}) },
    hideKeyboard(o?: any) {
      calls.push("hide");
      // 有些基础库/机型上 hideKeyboard 会失败（复审第三种）
      if (opts.failHide) o?.fail?.({ errMsg: "hideKeyboard:fail" });
      else o?.success?.({});
    },
    ...(opts.updateKeyboard === false ? {} : { updateKeyboard(o: any) { calls.push("update:" + o.value) } }),
    onKeyboardInput(fn) { handlers.input = fn },
    onKeyboardConfirm(fn) { handlers.confirm = fn },
    onKeyboardComplete(fn) { handlers.complete = fn },
    onKeyboardHeightChange(fn) { handlers.height = fn },
    onHide(fn) { handlers.hide = fn },
  };
  return {
    api, calls,
    input: (v: string) => handlers.input({ value: v }),
    complete: (v: string) => handlers.complete({ value: v }),
    height: (h: number) => handlers.height({ height: h }),
    hideApp: () => handlers.hide(undefined),
    shownSettings: () => shown,
  };
}

function harness(opts: FakeOpts = {}) {
  const wx = fakeWx(opts);
  const timers: Array<() => void> = [];
  const toasts: string[] = [];
  const s = new KeyboardSession(wx.api, {
    redraw: () => {},
    toast: (m) => toasts.push(m),
    setTimeout: (fn) => { timers.push(fn); return timers.length },
    clearTimeout: (id) => { timers[Number(id) - 1] = () => {} },
  });
  return { s, wx, toasts, flushTimers: () => { const t = timers.splice(0); t.forEach((f) => f()) } };
}

// 开一局页那两个字段：地点（40 字单行「下一项」）→ 备注（200 字多行）。
const place = (onInput: (v: string) => void) =>
  open({ key: "place", maxLength: 40, confirmType: "next", onInput });
const memo = (onInput: (v: string) => void) =>
  open({ key: "memo", maxLength: 200, multiple: true, onInput });

/** 拉到「地点」开着、然后点了「备注」。返回两边收到的字。 */
function fieldSwitch() {
  const h = harness();
  const got: Record<string, string[]> = { place: [], memo: [] };
  h.s.focus(place((v) => got.place.push(v)));
  h.wx.height(336); // 键盘起来了
  h.s.focus(memo((v) => got.memo.push(v)));
  return { ...h, got };
}

describe("KeyboardSession：换字段时收尾事件的三种顺序（复审要求）", () => {
  it("① 换字段先收起旧键盘，收起期间 editing 还是旧的（迟到的 complete 才写得对地方）", () => {
    const { s, wx } = fieldSwitch();
    expect(wx.calls).toEqual(["show:", "hide"]);
    expect(s.isEditing("place")).toBe(true);
    expect(s.isEditing("memo")).toBe(false);
  });

  it("② **complete 先到**：地点的字写回地点，之后再拉备注，两边互不串", () => {
    const { got, wx, s } = fieldSwitch();
    wx.complete("去楼下星巴克");           // 旧键盘（地点）的收尾
    expect(got.place).toEqual(["去楼下星巴克"]);
    expect(got.memo).toEqual([]);
    expect(wx.calls.at(-1)).toBe("show:");  // 备注这才拉起来（带着自己的默认值）
    expect(s.isEditing("memo")).toBe(true);
    expect(wx.shownSettings().maxLength).toBe(200);   // 不能还是地点的 40/单行
    expect(wx.shownSettings().multiple).toBe(true);
    wx.input("少冰\n两杯");                 // 用户在备注里打字
    expect(got.memo).toEqual(["少冰\n两杯"]);
    expect(got.place).toEqual(["去楼下星巴克"]);
  });

  it("③ **高度归零先到**：备注起来了，之后迟到的那条 complete 属于旧键盘，不许写进备注", () => {
    const { got, wx, s } = fieldSwitch();
    wx.height(0);                          // 旧的收起了
    expect(s.isEditing("memo")).toBe(true);
    wx.complete("去楼下星巴克");            // 迟到的 old complete：**必须丢掉**
    expect(got.memo).toEqual([]);
    expect(got.place).toEqual([]);          // 也不该回头再写一遍旧的（每次输入就已经给过了）
    expect(s.isEditing("memo")).toBe(true); // 关键：不能把新会话关掉
    wx.height(336);                        // 备注真的起来了
    wx.input("少冰");
    expect(got.memo).toEqual(["少冰"]);
    wx.complete("少冰");                    // 这次是备注自己的，要收下
    expect(got.memo).toEqual(["少冰", "少冰"]);
    expect(s.isEditing("memo")).toBe(false);
  });

  it("④ **hide 失败**（有的基础库）→ 不干等，直接拉新的；之后的 complete 也不会串", () => {
    const h = harness({ failHide: true });
    const got: Record<string, string[]> = { place: [], memo: [] };
    h.s.focus(place((v) => got.place.push(v)));
    h.wx.height(336);
    h.s.focus(memo((v) => got.memo.push(v)));
    expect(h.wx.calls).toEqual(["show:", "hide", "show:"]); // 没卡住
    expect(h.s.isEditing("memo")).toBe(true);
    h.wx.complete("去楼下星巴克");
    expect(got.memo).toEqual([]);
    h.wx.height(336);
    h.wx.input("少冰");
    expect(got.memo).toEqual(["少冰"]);
  });

  it("⑤ 两个收尾事件**都不来** → 500ms 兜底拉起新键盘；之后旧 complete 仍被吃掉", () => {
    const h = harness();
    const got: Record<string, string[]> = { place: [], memo: [] };
    h.s.focus(place((v) => got.place.push(v)));
    h.wx.height(336);
    h.s.focus(memo((v) => got.memo.push(v)));
    expect(h.wx.calls).toEqual(["show:", "hide"]);   // 还在等
    h.flushTimers();                                  // 兜底到点
    expect(h.wx.calls.at(-1)).toBe("show:");          // 硬拉起来，用户不会卡在没键盘的状态
    expect(h.s.isEditing("memo")).toBe(true);
    h.wx.complete("去楼下星巴克");                     // 迟到的旧 complete
    expect(got.memo).toEqual([]);
    expect(h.s.isEditing("memo")).toBe(true);         // 不许把新的关掉
    h.wx.height(336); h.wx.input("少冰");
    expect(got.memo).toEqual(["少冰"]);
  });

  it("⑥ 吞掉的只是「迟到的那一个」：新键盘自己报的 complete 照收（不做一次性开关）", () => {
    const h = harness();
    const got: string[] = [];
    h.s.focus(place(() => {}));
    h.wx.height(336);
    h.s.focus(memo((v) => got.push(v)));
    h.wx.height(0);          // 旧的收起
    h.wx.height(336);        // 新的起来 → 解除吞
    h.wx.complete("少冰");    // 新的自己的
    expect(got).toEqual(["少冰"]);
    expect(h.s.isEditing("memo")).toBe(false);
  });
});

describe("KeyboardSession：点同一个字段 / 收起 / 切后台", () => {
  it("键盘开着再点同一字段 → 不动键盘（真机上原来就是这里报的 already show）", () => {
    const h = harness();
    h.s.focus(place(() => {}));
    h.wx.height(336);
    h.s.focus(place(() => {}));
    expect(h.wx.calls).toEqual(["show:"]);   // 没有第二次 show
    expect(h.toasts).toEqual([]);            // 也就没有「键盘没拉起来」
  });

  it("设置一样的两个字段 → update（不收起，界面不跳）", () => {
    const h = harness();
    h.s.focus(open({ key: "a", value: "甲", maxLength: 40, onInput: () => {} }));
    h.wx.height(336);
    h.s.focus(open({ key: "b", value: "乙", maxLength: 40, onInput: () => {} }));
    expect(h.wx.calls).toEqual(["show:甲", "update:乙"]);
    expect(h.s.isEditing("b")).toBe(true);
  });

  it("blur() 收起：会话立刻作废，之后来的 complete 不会再写字段", () => {
    const h = harness();
    const got: string[] = [];
    h.s.focus(place((v) => got.push(v)));
    h.wx.height(336);
    h.s.blur();
    expect(h.s.isEditing("place")).toBe(false);
    h.wx.complete("半截字");
    expect(got).toEqual([]);
  });

  it("切后台 → 一律作废（键盘可能已经不在了，事件不会来）", () => {
    const h = harness();
    h.s.focus(place(() => {}));
    h.wx.height(336);
    h.wx.hideApp();
    expect(h.s.isEditing("place")).toBe(false);
    expect(h.s.editing).toBe(null);
  });

  it("真拉不起来时报错；「已经开着」这种不报", () => {
    const toasts: string[] = [];
    const host = { redraw: () => {}, toast: (m: string) => toasts.push(m), setTimeout: () => 1, clearTimeout: () => {} };
    const bad = fakeWx();
    bad.api.showKeyboard = (o: any) => o.fail?.({ errMsg: "showKeyboard:fail not support" });
    new KeyboardSession(bad.api, host).focus(place(() => {}));
    expect(toasts).toEqual(["键盘没拉起来：showKeyboard:fail not support"]);

    const benign = fakeWx();
    benign.api.showKeyboard = (o: any) => o.fail?.({ errMsg: "showKeyboard:fail already show" });
    new KeyboardSession(benign.api, host).focus(place(() => {}));
    expect(toasts).toHaveLength(1); // 没多一条
  });
});

describe("HIDE_FALLBACK_MS", () => {
  it("兜底时长是个正数常量（真机上 hideKeyboard 不回回调时靠它）", () => {
    expect(HIDE_FALLBACK_MS).toBeGreaterThan(0);
  });
});
