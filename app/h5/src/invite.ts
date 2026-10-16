/**
 * F2「约 TA 打一局」的话术（#108）—— H5 这一侧的入口。
 *
 * #253 P6.75 起**话术在 `app/shared/src/invite.ts`**，而且那边的 `origin` 是必填的
 * （共享层不许自己读 `window`）。这个文件只把 H5 的默认值（当前站点地址）绑好再导出，
 * 所以 H5 这边的调用点照旧 `inviteText(nickname)` 就行。
 */

import { inviteText as sharedInviteText } from "../../shared/src/invite";

export * from "../../shared/src/invite";

/** H5 这边默认用当前站点的地址（小游戏那边传自己的 `API_BASE`）。 */
export function inviteText(nickname: string, origin: string = window.location.origin): string {
  return sharedInviteText(nickname, origin);
}
