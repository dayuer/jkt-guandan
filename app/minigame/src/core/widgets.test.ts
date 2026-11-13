// timePicker / nextSlot：「改一下这一局」和开一局「自定义」共用的选时间逻辑。只测数据状态。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nextSlot, timePicker } from "./widgets";
import { wibAt } from "../lib/fmt";

// 2026-10-21 周三 20:10 WIB = 13:10 UTC
const NOW = Date.UTC(2026, 9, 21, 13, 10);
const app = {} as any;

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW); });
afterEach(() => { vi.useRealTimers(); });

describe("timePicker", () => {
  it("选项：今天起 8 天、9–23 点、0/15/30/45 分；dayCount 可以放宽", () => {
    const p = timePicker(app, wibAt(1, 19, 30), "t");
    expect(p.days).toEqual([0, 1, 2, 3, 4, 5, 6, 7]);
    expect(p.hours).toEqual(Array.from({ length: 15 }, (_, i) => 9 + i));
    expect(p.minutes).toEqual([0, 15, 30, 45]);
    expect(p.st).toEqual({ day: 1, hour: 19, minute: 30 });
    expect(p.labels.day().slice(0, 3)).toEqual(["今天", "明天", "周五 10/23"]);
    expect(timePicker(app, wibAt(1, 19, 30), "t", 30).days).toHaveLength(30);
  });

  it("原来的值不在范围里：插到每一行最前面并选中", () => {
    const p = timePicker(app, wibAt(9, 8, 10), "t");
    expect(p.days[0]).toBe(9);
    expect(p.hours[0]).toBe(8);
    expect(p.minutes[0]).toBe(10);
    expect(p.unix()).toBe(wibAt(9, 8, 10));
    expect(p.labels.day()[0]).toBe("周五 10/30");
    expect(timePicker(app, wibAt(9, 8, 10), "t", 30).days[0]).toBe(0); // 30 天里就不用插
  });

  it("今天已经过去的钟点不能选（:45 也过了才算），原来的值和别的天不受影响", () => {
    const p = timePicker(app, wibAt(0, 18, 0), "t");
    expect(p.hourOff(0, 19)).toBe(true);
    expect(p.hourOff(0, 20)).toBe(false); // 20:45 还没到
    expect(p.hourOff(0, 18)).toBe(false); // 原来的值
    expect(p.hourOff(1, 9)).toBe(false);
  });

  it("切回今天时钟点已经过了：跳到下一个能选的", () => {
    const p = timePicker(app, wibAt(1, 10, 0), "t");
    p.pickDay(p.days.indexOf(0));
    expect(p.st).toEqual({ day: 0, hour: 20, minute: 0 });
    const q = timePicker(app, wibAt(1, 22, 15), "t");
    q.pickDay(q.days.indexOf(0));
    expect(q.st.hour).toBe(22); // 还没过，不动
  });
});

describe("nextSlot", () => {
  it("选项网格里最早一个至少 10 分钟后的时间", () => {
    expect(nextSlot()).toBe(wibAt(0, 20, 30)); // 20:15 只差 5 分钟
    const late = wibAt(0, 23, 50);
    expect(nextSlot(late)).toBe(wibAt(1, 9, 0, late));
    expect(nextSlot(wibAt(0, 6, 0))).toBe(wibAt(0, 9, 0));
  });
});
