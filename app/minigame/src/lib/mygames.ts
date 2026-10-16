// 「我的牌局」每一行的文字（#211）。纯函数，单测在 mygames.test.ts。
//
// 用户原话：「把我开的，我参与的，这些还没有开始的牌局列出来，时间地点，让我能看清凑局的状态」
// 「历史局也要放进去，可以删」。所以一行要回答两件事：我在这局里是什么身份、这局凑到哪一步了。

import type { GameSummary, HistoryGame } from "./api";
import { levelName } from "./scoring";

export type Tone = "red" | "green" | "sand" | "grey";
export interface Badge { label: string; tone: Tone }

/** 我在这一局里的身份。局头也坐在座位上，所以先看是不是我开的。 */
export function roleBadge(g: GameSummary, meID: string | undefined): Badge | null {
  if (meID && g.host.id === meID) return { label: "我开的", tone: "red" };
  if (g.my_role === "seated") return { label: "已报名", tone: "green" };
  if (g.my_role === "waitlist") return { label: g.waitlist_pos ? `候补第 ${g.waitlist_pos} 位` : "候补中", tone: "sand" };
  return null;
}

/** 要打的局凑到哪一步：差几人 / 已成局。 */
export function upcomingStatus(g: GameSummary): Badge {
  if (g.status === "full" || g.missing <= 0) return { label: "已成局 · 4/4", tone: "green" };
  return { label: `差 ${g.missing} 人 · ${g.seats}/4`, tone: g.missing === 1 ? "red" : "sand" };
}

/** 历史里的一局：取消了、打完了，还是开了局还没收尾。 */
export function historyStatus(g: GameSummary): Badge {
  if (g.status === "cancelled") return { label: "已取消", tone: "grey" };
  if (g.status === "done") return { label: "已打完", tone: "grey" };
  // 过了开始时间，服务端还没把它收成 done：一般是正在打
  return { label: "已开局", tone: "grey" };
}

/** 历史里记过分的局：「红 J : 黑 9 · 6 局」，有一队过了 A 就说「红方过 A」；不升级局说各赢几局。 */
export function scoreLine(g: HistoryGame): string {
  const s = g.score;
  if (!s || s.rounds === 0) return "";
  const rounds = `${s.rounds} 局`;
  if (s.kind === "no-level") {
    return `红 ${s.red_wins} : 黑 ${s.black_wins} · ${rounds}`;
  }
  if (s.winner) return `${s.winner === "red" ? "红方" : "黑方"}过 A · ${rounds}`;
  return `红 ${levelName(s.red_level)} : 黑 ${levelName(s.black_level)} · ${rounds}`;
}
