import { describe, expect, it } from "vitest";
import type { GameDetail } from "./api";
import { conflictingFields, describeConflict, patchedFields } from "./conflict";

function detail(over: Partial<GameDetail["game"]> = {}): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "ABC123",
      starts_at: 1791986400,
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      note: "",
      status: "open",
      version: 1,
      capacity: 4,
      ...over,
    },
    host: { id: "u1", nickname: "老王" },
    seats: [],
    waitlist: [],
    missing: 4,
    me: { role: "none" },
  };
}

describe("版本冲突的判断（K4 / K5）", () => {
  it("补丁涉及的字段没被别人动过：不算冲突（K5，可以自动重提）", () => {
    const patch = { venue: "Grand Indonesia" };
    const baseline = detail();
    // 期间有人报名：版本号 +1、座位变了，但地点没动。
    const latest = detail({ version: 2 });

    expect(conflictingFields(patch, baseline, latest)).toEqual([]);
  });

  it("同一个字段被两个人改了：算冲突（K4，要用户选）", () => {
    const patch = { venue: "Grand Indonesia" };
    const baseline = detail();
    const latest = detail({ version: 2, venue: "PIK Avenue" });

    expect(conflictingFields(patch, baseline, latest)).toEqual(["venue"]);
  });

  it("只看补丁涉及的字段：别人改了别的字段不算冲突", () => {
    const patch = { venue: "Grand Indonesia" };
    const baseline = detail();
    const latest = detail({ version: 2, area: "PIK", kind: "no-level" });

    expect(conflictingFields(patch, baseline, latest)).toEqual([]);
  });

  it("改时间、商圈、玩法、备注都能识别出冲突", () => {
    const patch = { starts_at: 1791986400 + 3600, area: "PIK", kind: "teaching", note: "我的" };
    const baseline = detail({ note: "" });
    const latest = detail({
      version: 2,
      starts_at: 1791986400 + 7200,
      area: "Kemang",
      kind: "no-level",
      note: "别人的",
    });

    expect(conflictingFields(patch, baseline, latest).sort()).toEqual(["area", "kind", "note", "starts_at"]);
  });

  it("备注的空串和缺省值等价", () => {
    const patch = { note: "有话说" };
    const baseline = detail({ note: "" });
    const latest = detail({ version: 2 });
    expect(conflictingFields(patch, baseline, latest)).toEqual([]);
  });

  it("patchedFields 只列出补丁里真的有的字段", () => {
    expect(patchedFields({ venue: "X" })).toEqual(["venue"]);
    expect(patchedFields({})).toEqual([]);
  });

  it("对比表给出字段名、我的值和服务端的当前值", () => {
    const patch = { venue: "Grand Indonesia" };
    const baseline = detail();
    const latest = detail({ version: 2, venue: "PIK Avenue" });

    expect(describeConflict(patch, baseline, latest)).toEqual([
      { field: "venue", label: "地点", mine: "Grand Indonesia", theirs: "PIK Avenue" },
    ]);
  });
});
