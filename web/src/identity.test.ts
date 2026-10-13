import { afterEach, describe, expect, it, vi } from "vitest";
import { hasIdentityMarker, redeemClaimFromURL } from "./identity";

// 这些用例通过 window.location 驱动，和真实页面一致。
function visit(search: string): void {
  window.history.replaceState(null, "", "/" + search);
}

describe("redeemClaimFromURL", () => {
  afterEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
    visit("");
  });

  it("URL 里没有 claim 参数时不发请求", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    expect(await redeemClaimFromURL()).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("用 POST 兑换，只留一个非敏感标记，并清掉地址栏里的 claim", async () => {
    const fetchMock = vi.fn(async () => ({
      ok: true,
      json: async () => ({ user: { id: "u1", nickname: "老王" } }),
    }));
    vi.stubGlobal("fetch", fetchMock);
    visit("?claim=abc123&keep=1");

    const user = await redeemClaimFromURL();

    expect(user).toEqual({ id: "u1", nickname: "老王" });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const [path, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe("/api/claim/redeem");
    // 必须是 POST：GET 不能消耗一次性 token（§10.3）。
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ token: "abc123" });

    // 只写非敏感标记；凭证留在 HttpOnly cookie 里，前端不存。
    expect(hasIdentityMarker()).toBe(true);
    expect(localStorage.length).toBe(1);

    // claim 参数清掉了，其它参数保留。
    expect(window.location.search).toBe("?keep=1");
  });

  it("兑换失败时返回 null，不写标记，地址栏参数留着以便重试", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: false })));
    visit("?claim=bad");

    expect(await redeemClaimFromURL()).toBeNull();
    expect(hasIdentityMarker()).toBe(false);
    expect(window.location.search).toBe("?claim=bad");
  });
});
