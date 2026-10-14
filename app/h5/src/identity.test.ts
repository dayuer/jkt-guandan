import { afterEach, describe, expect, it, vi } from "vitest";
import { hasIdentityMarker, redeemClaimFromURL } from "./identity";

// 这些用例通过 window.location 驱动，和真实页面一致。
function visit(search: string): void {
  window.history.replaceState(null, "", "/" + search);
}

function stubFetch(status: number, body: unknown) {
  const mock = vi.fn(async () => ({ ok: status >= 200 && status < 300, status, json: async () => body }));
  vi.stubGlobal("fetch", mock);
  return mock;
}

function sentBody(mock: ReturnType<typeof vi.fn>): unknown {
  const [, init] = mock.mock.calls[0] as unknown as [string, RequestInit];
  return JSON.parse(String(init.body));
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

    expect(await redeemClaimFromURL()).toEqual({ kind: "none" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("兑换成功：用 POST、只写非敏感标记、清掉地址栏里的 claim", async () => {
    const mock = stubFetch(200, { user: { id: "u1", nickname: "老王" } });
    visit("?claim=abc123&keep=1");

    expect(await redeemClaimFromURL()).toEqual({ kind: "ok", user: { id: "u1", nickname: "老王" } });

    const [path, init] = mock.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe("/api/claim/redeem");
    // 必须是 POST：GET 不能消耗一次性 token（§10.3）。
    expect(init.method).toBe("POST");
    expect(sentBody(mock)).toEqual({ token: "abc123" });

    // 只写非敏感标记；凭证留在 HttpOnly cookie 里，前端不存。
    expect(hasIdentityMarker()).toBe(true);
    expect(localStorage.length).toBe(1);

    // claim 参数清掉了，其它参数保留。
    expect(window.location.search).toBe("?keep=1");
  });

  it("已有身份时服务端返回 409：带回对方称呼，什么也不动", async () => {
    stubFetch(409, { error: { code: "claim_conflict" }, other_nickname: "小李" });
    visit("?claim=abc123");

    expect(await redeemClaimFromURL()).toEqual({ kind: "conflict", otherNickname: "小李" });
    expect(hasIdentityMarker()).toBe(false);
    // 参数留着：用户确认之后还要用它再提交一次。
    expect(window.location.search).toBe("?claim=abc123");
  });

  it("用户确认时带上 confirm: true", async () => {
    const mock = stubFetch(200, { user: { id: "u2", nickname: "小李" } });
    visit("?claim=abc123");

    await redeemClaimFromURL(true);
    expect(sentBody(mock)).toEqual({ token: "abc123", confirm: true });
  });

  it("其它失败返回 failed，不写标记、不清参数", async () => {
    stubFetch(400, { error: { code: "claim_invalid" } });
    visit("?claim=bad");

    expect(await redeemClaimFromURL()).toEqual({ kind: "failed" });
    expect(hasIdentityMarker()).toBe(false);
    expect(window.location.search).toBe("?claim=bad");
  });
});
