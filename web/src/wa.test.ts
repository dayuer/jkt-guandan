import { describe, expect, it } from "vitest";
import { maskWaPhone, normalizeWaPhone } from "./wa";

describe("normalizeWaPhone", () => {
  it.each([
    ["0812-3456-7890", "+6281234567890"],
    ["0812 3456 7890", "+6281234567890"],
    ["62 812 3456 7890", "+6281234567890"],
    ["+62 812-3456-7890", "+6281234567890"],
    ["812 3456 7890", "+6281234567890"],
    ["0065 9123 4567", "+6591234567"],
    ["+86 138 0013 8000", "+8613800138000"],
  ])("%s → %s", (input, want) => {
    expect(normalizeWaPhone(input)).toBe(want);
  });

  it.each(["", "abc", "12", "+0123456789", "+62812345678901234567"])("认不出来：%s", (input) => {
    expect(normalizeWaPhone(input)).toBeNull();
  });
});

describe("maskWaPhone", () => {
  it("印尼号码按设计稿写法脱敏", () => {
    expect(maskWaPhone("+6281234567890")).toBe("+62 812****890");
  });
  it("其他国家只露头尾", () => {
    expect(maskWaPhone("+8613800138000")).toBe("+861****000");
  });
});
