/**
 * 身份相关的前端逻辑（docs/h5-pwa-plan.md §4、docs/server-design.md §10.3、§5.2 修订）。
 *
 * 设备凭证**只存在于 HttpOnly cookie 里**：服务端通过 Set-Cookie 下发，JS 读不到，
 * 也不在 localStorage 或任何 JS 能读的地方留副本。一旦有明文副本，
 * 任何一次 XSS 都能把它偷走，HttpOnly 就白设了——§10.3 的原话是「cookie 被盗等于身份被盗」。
 *
 * cookie 丢了（清缓存、换浏览器环境）就走 claim token 或认领码找回，这正是 §5.2 的兜底。
 *
 * 这里唯一写 localStorage 的是一个**非敏感**标记，只用来决定首屏显示
 * 「填称呼」还是「欢迎回来」，绝不能当凭证用。
 */

const MARKER_KEY = "gd_has_identity";

export interface User {
  id: string;
  nickname: string;
}

/** 记下「这台浏览器已经有身份了」，只影响首屏 UI，不是凭证。 */
export function markHasIdentity(): void {
  try {
    localStorage.setItem(MARKER_KEY, "1");
  } catch {
    // 无痕模式或禁用存储时写不进去，忽略即可。
  }
}

/** 首屏判断用。它只说明「以前填过称呼」，不代表服务端认得你——真伪由 cookie 决定。 */
export function hasIdentityMarker(): boolean {
  try {
    return localStorage.getItem(MARKER_KEY) === "1";
  } catch {
    return false;
  }
}

export function clearIdentityMarker(): void {
  try {
    localStorage.removeItem(MARKER_KEY);
  } catch {
    // 忽略。
  }
}

/** URL 里有没有等着兑换的认领链接。 */
export function pendingClaimToken(): string | null {
  return new URL(window.location.href).searchParams.get("claim");
}

export type RedeemResult =
  | { kind: "none" }
  | { kind: "ok"; user: User }
  | { kind: "conflict"; otherNickname: string }
  | { kind: "failed" };

/**
 * 用 URL 里的 claim token 兑换身份。
 *
 * `confirm=false`（默认）：服务端在「这台设备已经有别人身份」时只会返回 409，
 * **不会**消耗 token，也不会改动任何东西。所以这个请求本身是只读的，
 * 它只是用来问出「这个链接属于谁」，好把确认屏渲染出来。
 * `confirm=true`：用户在确认屏上点了「切换成 X」之后才发，这时服务端才会
 * 消耗 token 并把这台设备的身份切过去。
 *
 * 打开带 claim 的页面（GET）不消耗 token——微信和 WhatsApp 的链接预览抓取器只发 GET，
 * 所以一次性 token 不会在用户还没点进来的时候就被用掉（§10.3）。
 */
export async function redeemClaimFromURL(confirm = false): Promise<RedeemResult> {
  const url = new URL(window.location.href);
  const token = url.searchParams.get("claim");
  if (!token) return { kind: "none" };

  const resp = await fetch("/api/claim/redeem", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(confirm ? { token, confirm: true } : { token }),
  });

  if (resp.status === 409) {
    const body = (await resp.json()) as { other_nickname?: string };
    // 冲突时保留地址栏参数：用户确认完还要用这个 token 再提交一次。
    return { kind: "conflict", otherNickname: body.other_nickname ?? "" };
  }
  if (!resp.ok) return { kind: "failed" };

  // 凭证已经通过 Set-Cookie 下来了，这里只留一个非敏感标记。
  markHasIdentity();

  const body = (await resp.json()) as { user: User };

  // 兑换成功，参数没有用了，立刻从地址栏去掉（§10.3）。
  url.searchParams.delete("claim");
  const qs = url.searchParams.toString();
  window.history.replaceState(null, "", url.pathname + (qs ? `?${qs}` : "") + url.hash);

  return { kind: "ok", user: body.user };
}
