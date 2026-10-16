// 分享话术（#253）：文案和链接在 app/shared/src/share.ts，这里把**小游戏的时间写法**
// （`lib/fmt`，安卓微信不一定有 `Intl` 时区）和站点地址（`API_BASE`）绑进去。
//
// 从 scenes/sheets.ts 挪出来是为了能单独测：那个场景模块 import 了 wx / core/ui 一大串。

import { API_BASE } from "./api";
import { dayShort, timeHM } from "./fmt";
import { shareText, type ShareLike } from "../../../shared/src/share";

/** 话术要读的字段：共享层声明的那几个 + 开始时间（时间怎么显示是小游戏自己的事）。 */
export interface MiniShareInput extends ShareLike {
  game: { short_code: string; venue: string; status: string; starts_at: number };
}

/** 一句话话术：“今天 19:30 …，来的点 👉 <站点>/g/<短码>”。 */
export function gameShareText(detail: MiniShareInput, now: number = Math.floor(Date.now() / 1000)): string {
  return shareText(detail, `${dayShort(detail.game.starts_at, now)} ${timeHM(detail.game.starts_at)}`, API_BASE);
}
