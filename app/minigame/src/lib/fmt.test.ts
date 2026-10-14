import { describe, expect, it } from "vitest";
import { cardDay, dayLabel, dayShort, lastSeenLabel, slashDate, timeHM, weekday, wibAt, wibParts } from "./fmt";

// 2026-10-14 周三 12:00 WIB = 05:00 UTC
const NOW = Date.UTC(2026, 9, 14, 5, 0) / 1000;

describe("fmt（不用 Intl，按 UTC+7 算）", () => {
  it("雅加达时间的时分、日期、星期", () => {
    const t = Date.UTC(2026, 9, 14, 12, 30) / 1000; // 19:30 WIB
    expect(timeHM(t)).toBe("19:30");
    expect(slashDate(t)).toBe("10/14");
    expect(weekday(t)).toBe("周三");
  });

  it("UTC 跨日但 WIB 还是同一天", () => {
    const t = Date.UTC(2026, 9, 13, 18, 0) / 1000; // 10/14 01:00 WIB
    expect(slashDate(t)).toBe("10/14");
    expect(dayShort(t, NOW)).toBe("今天");
  });

  it("今天 / 明天 / 之后", () => {
    expect(dayLabel(NOW + 3600, NOW)).toBe("今天 10/14 周三");
    expect(dayLabel(NOW + 86400, NOW)).toBe("明天 10/15 周四");
    expect(dayShort(NOW + 3 * 86400, NOW)).toBe("10月17日");
    expect(cardDay(NOW + 3 * 86400, NOW)).toBe("10/17");
  });

  it("wibAt 和 wibParts 互逆", () => {
    const t = wibAt(2, 19, 30, NOW);
    expect(timeHM(t)).toBe("19:30");
    expect(slashDate(t)).toBe("10/16");
    expect(wibParts(t, NOW)).toEqual({ dayOffset: 2, hour: 19, minute: 30 });
  });

  it("设备最后活跃时间", () => {
    expect(lastSeenLabel(NOW - 30, NOW)).toBe("刚刚");
    expect(lastSeenLabel(NOW - 5 * 60, NOW)).toBe("5 分钟前");
    expect(lastSeenLabel(NOW - 26 * 3600, NOW)).toBe("昨天");
  });
});
