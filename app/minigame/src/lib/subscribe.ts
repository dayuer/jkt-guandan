// 订阅消息（#137）：小游戏收不到 Web Push，「成局」「开局前 1 小时」的提醒走微信订阅消息。
//
// 一次性订阅：用户每同意一次某个模板，服务端就能发一条。所以每次报名都问一次，同意了的上报给服务端
// （POST /api/wx/subscribe）记次数。模板 ID 从 GET /api/config 拿，必须在点按之前拿好：
// wx.requestSubscribeMessage 只能在点按的 touchend 里**同步**调用（热区的 onTap 就在 wx.onTouchEnd 里执行），
// 中间隔一个 await 就会被微信拒掉。

import { getPublicConfig, reportWxSubscribe, type WxSubscribeTemplates } from "./api";

let templates: WxSubscribeTemplates = {};

/** 启动时调一次。失败不要紧：拿不到就不问订阅，报名照常。 */
export async function loadSubscribeTemplates(): Promise<void> {
  try {
    templates = (await getPublicConfig()).wx_subscribe;
  } catch {
    // ignore
  }
}

/** 测试用。 */
export function setSubscribeTemplates(t: WxSubscribeTemplates) {
  templates = t;
}

function templateIDs(): string[] {
  return [templates.remind, templates.full].filter((id): id is string => !!id);
}

/** wx.requestSubscribeMessage 的结果里同意了的模板。 */
export function acceptedOf(res: any, ids: string[]): string[] {
  return ids.filter((id) => res && res[id] === "accept");
}

/**
 * 问用户要不要订阅提醒。**必须在点按的 onTap 里同步调用**（不能放在任何 await 之后）。
 * 返回同意了的模板 ID；没配模板、基础库不支持、用户拒绝或者出错都返回空数组，不会 reject。
 */
export function askSubscribe(): Promise<string[]> {
  const ids = templateIDs();
  if (!ids.length || typeof wx === "undefined" || typeof wx.requestSubscribeMessage !== "function") return Promise.resolve([]);
  return new Promise((resolve) => {
    try {
      wx.requestSubscribeMessage({
        tmplIds: ids,
        success: (res: any) => resolve(acceptedOf(res, ids)),
        fail: () => resolve([]),
      });
    } catch {
      resolve([]);
    }
  });
}

/** 把同意了的模板报给服务端（要有身份）。失败不提示：最多少收一条提醒。 */
export async function reportAccepted(ids: string[]): Promise<void> {
  if (!ids.length) return;
  try {
    await reportWxSubscribe(ids);
  } catch {
    // ignore
  }
}

/** 同意里有没有「开局提醒」：报名成功弹层据此显示「开局前 1 小时会提醒你」。 */
export function remindOn(accepted: string[]): boolean {
  return !!templates.remind && accepted.includes(templates.remind);
}
