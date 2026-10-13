/**
 * 身份相关的前端逻辑（docs/h5-pwa-plan.md §4、docs/server-design.md §10.3）。
 *
 * 设备凭证**只存在于 HttpOnly cookie 里**：服务端通过 Set-Cookie 下发，JS 读不到，
 * 也不在 localStorage 或任何 JS 能读的地方留副本。一旦有明文副本，
 * 任何一次 XSS 都能把它偷走，HttpOnly 就白设了——§10.3 的原话是「cookie 被盗等于身份被盗」。
 *
 * cookie 丢了（清缓存、换浏览器环境）就走 claim token 或 4 位认领码找回，
 * 这正是 §5.2 的兜底，不需要前端自己存凭证。
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

/**
 * URL 里带 `?claim=…` 时用 POST 兑换，成功后清掉地址栏参数。
 * 返回兑换到的用户；没有参数或兑换失败时返回 null。
 *
 * 只有 POST 会消耗 token：打开页面（GET）不会，链接预览抓取器也就不会把它用掉（§10.3）。
 */
export async function redeemClaimFromURL(): Promise<User | null> {
  const url = new URL(window.location.href);
  const token = url.searchParams.get("claim");
  if (!token) return null;

  const resp = await fetch("/api/claim/redeem", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (!resp.ok) return null;

  // 凭证已经通过 Set-Cookie 下来了，这里只留一个非敏感标记。
  markHasIdentity();

  const body = (await resp.json()) as { user: User };

  // 兑换成功，参数没有用了，立刻从地址栏去掉（§10.3）。
  url.searchParams.delete("claim");
  const qs = url.searchParams.toString();
  window.history.replaceState(null, "", url.pathname + (qs ? `?${qs}` : "") + url.hash);

  return body.user;
}
