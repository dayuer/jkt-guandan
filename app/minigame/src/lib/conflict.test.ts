import { describe, expect, it } from "vitest";
import { conflictingFields, describeConflict } from "./conflict";

// 用很久以后的绝对时间：fmt 的 dayLabel 会给「今天/明天」加前缀，挑远一点，断言才能钉死。
const T_MINE = 1810000000;
const T_THEIRS = 1810086400;

function game(over: Partial<{ starts_at: number; venue: string; area: string; kind: string; note: string }> = {}) {
  return { game: { starts_at: T_MINE, venue: "Pacific Place", area: "SCBD", kind: "classic", ...over } };
}

describe("K4 对比表（小游戏的时间写法，#253 P9）", () => {
  it("时间那一栏：标签是「时间」，两边各自渲染（小游戏用 lib/fmt，不读 Intl）", () => {
    const rows = describeConflict({ starts_at: T_MINE }, game(), game({ starts_at: T_THEIRS }));
    expect(rows).toEqual([
      { field: "starts_at", label: "时间", mine: "05/11 周二 08:46", theirs: "05/12 周三 08:46" },
    ]);
  });

  it("玩法那一栏：标签是「玩法」，值是中文玩法名", () => {
    const rows = describeConflict({ kind: "teaching" }, game(), game({ kind: "no-level" }));
    expect(rows).toEqual([
      { field: "kind", label: "玩法", mine: "带新手", theirs: "不升级" },
    ]);
  });

  it("没冲突就不出对比表（走 K5 自动重提）", () => {
    expect(conflictingFields({ venue: "Grand Indonesia" }, game(), game({ area: "PIK" }))).toEqual([]);
    expect(describeConflict({ venue: "Grand Indonesia" }, game(), game({ area: "PIK" }))).toEqual([]);
  });
});
