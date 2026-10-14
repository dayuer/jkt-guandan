import { describe, expect, it } from "vitest";
import { dayLabel, missingLabel, timeHM } from "./format";

// 雅加达是 UTC+7，没有夏令时，所以 UTC 时间加 7 小时就是 WIB。
const at = (iso: string) => Math.floor(Date.parse(iso) / 1000);

describe("format", () => {
  it("按 WIB 显示时间", () => {
    // 12:30 UTC = 19:30 WIB
    expect(timeHM(at("2026-10-17T12:30:00Z"))).toBe("19:30");
  });

  it("今天 / 明天 / 更远的日子", () => {
    const now = at("2026-10-15T05:00:00Z"); // WIB 10 月 15 日 12:00
    expect(dayLabel(at("2026-10-15T12:00:00Z"), now)).toBe("今天 10/15 周四");
    expect(dayLabel(at("2026-10-16T12:00:00Z"), now)).toBe("明天 10/16 周五");
    expect(dayLabel(at("2026-10-17T12:00:00Z"), now)).toBe("10/17 周六");
  });

  it("跨过 WIB 的午夜才算下一天", () => {
    // now = WIB 10 月 15 日 23:00（= 16:00 UTC）
    const now = at("2026-10-15T16:00:00Z");
    // WIB 10 月 16 日 01:00 —— 已经是「明天」了，虽然 UTC 还是 15 日 18:00
    expect(dayLabel(at("2026-10-15T18:00:00Z"), now)).toBe("明天 10/16 周五");
  });

  it("差几个人", () => {
    expect(missingLabel(2)).toBe("差 2 人");
    expect(missingLabel(0)).toBe("已满");
  });
});
