// 身份：小游戏里「我是谁」只有这一份（对应 H5 App.tsx 的 me 状态）。
// 打开时先用本地的 device_token 问 /api/me；没有 token 就用 wx.login 静默登录，
// 服务端认得这个微信就直接下发 token，认不得就等用户第一次报名 / 开局时填称呼。

import { ApiError, getMe, getToken, onUnauthorized, setToken, wxLink, wxLogin, type UserRef, type WxLoginResponse } from "./api";

let me: UserRef | null = null;
let ready = false;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((fn) => fn());

export const currentUser = () => me;
export const isReady = () => ready;
export function onSessionChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function setUser(u: UserRef | null) {
  me = u;
  emit();
}

onUnauthorized(() => {
  setToken("");
  setUser(null);
});

/** 开发版没有 AppSecret，服务端认 `dev:<任意>` 当作 openid（#84）；这台设备固定用同一个。 */
function devCode(): string {
  let id = "";
  try {
    id = wx.getStorageSync("gd_dev_openid") || "";
  } catch {
    // ignore
  }
  if (!id) {
    id = "mg" + Math.random().toString(36).slice(2, 10);
    wx.setStorageSync("gd_dev_openid", id);
  }
  return "dev:" + id;
}

/** 每个 wx.login 的 code 只能用一次，所以每次请求前现取。 */
export function wxCode(): Promise<string> {
  if (__DEV__) return Promise.resolve(devCode());
  return new Promise((resolve, reject) =>
    wx.login({
      timeout: 8000,
      success: (r: any) => (r.code ? resolve(r.code) : reject(new ApiError(0, "wx_login", "微信登录没成功"))),
      fail: () => reject(new ApiError(0, "wx_login", "微信登录没成功，稍后再试")),
    }),
  );
}

function accept(res: WxLoginResponse): UserRef | null {
  if (res.user && res.device_token) {
    setToken(res.device_token);
    setUser(res.user);
    return res.user;
  }
  return null;
}

/** 打开小游戏时调一次。失败不报错：没身份也能看局。 */
export async function boot(): Promise<void> {
  try {
    if (getToken()) {
      try {
        const body = await getMe();
        setUser(body.user);
        return;
      } catch (err) {
        if (!(err instanceof ApiError && err.status === 401)) return; // 网络问题：留着 token，下次再认
        setToken("");
      }
    }
    accept(await wxLogin(await wxCode()));
  } catch {
    // 静默登录失败：当访客
  } finally {
    ready = true;
    emit();
  }
}

/** 第一次填称呼：用新的 wx code 建身份。 */
export async function signUp(nickname: string): Promise<UserRef> {
  const res = await wxLogin(await wxCode(), nickname);
  const u = accept(res);
  if (!u) throw new ApiError(500, "unknown", "没建成身份，请再试一次");
  return u;
}

/**
 * 凭网页上拿到的 6 位认领码认回身份（#84 B2）。
 * 网页上那个人的称呼在 409 claim_conflict 的 other_nickname 里；确认后带 confirm 重来（要新的 wx code）。
 */
export async function linkWithCode(code: string, confirm = false): Promise<UserRef> {
  const u = accept(await wxLink(code, await wxCode(), confirm));
  if (!u) throw new ApiError(500, "unknown", "没认回来，请再试一次");
  return u;
}

export function updateNickname(nickname: string) {
  if (me) setUser({ ...me, nickname });
}

/** 本机退出（退出当前设备、注销之后）。 */
export function signOutHere() {
  setToken("");
  setUser(null);
}
