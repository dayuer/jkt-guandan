/**
 * 设备身份的前端部分（docs/h5-pwa-plan.md §4、docs/server-design.md §10.3）。
 *
 * 身份本身由服务端下发：`device_token` 放在 HttpOnly cookie 里，JS 读不到。
 * 这里做的两件事：
 *  1. 新建身份时把服务端回传的明文 token 镜像一份到 localStorage（双写）；
 *  2. URL 里带 `?claim=` 时，用 POST 去兑换，成功后立刻把参数从地址栏清掉。
 *
 * 打开带 claim 的页面（GET）不会消耗 token——微信和 WhatsApp 的链接预览抓取器只发 GET，
 * 所以一次性 token 不会在用户还没点进来的时候就被用掉。
 */

const TOKEN_KEY = "gd_device_token";

export interface User {
  id: string;
  nickname: string;
}

/** 把服务端回传的 device_token 镜像到 localStorage。 */
export function rememberDeviceToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token);
  } catch {
    // 无痕模式或禁用存储时写不进去。cookie 才是主路径，不影响使用。
  }
}

export function storedDeviceToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

/**
 * URL 里带 `?claim=…` 时用 POST 兑换，成功后清掉地址栏参数。
 * 返回兑换到的用户；没有参数或兑换失败时返回 null。
 */
export async function redeemClaimFromURL(currentURL?: URL): Promise<User | null> {
  const url = currentURL ?? new URL(window.location.href);
  const token = url.searchParams.get("claim");
  if (!token) return null;

  const resp = await fetch("/api/claim/redeem", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (!resp.ok) return null;

  const body = (await resp.json()) as { user: User; device_token?: string };
  if (body.device_token) rememberDeviceToken(body.device_token);

  // 兑换成功，参数没有用了，立刻从地址栏去掉（§10.3）。
  stripClaimParam(url);
  return body.user;
}

function stripClaimParam(url: URL): void {
  url.searchParams.delete("claim");
  const qs = url.searchParams.toString();
  window.history.replaceState(null, "", url.pathname + (qs ? `?${qs}` : "") + url.hash);
}
