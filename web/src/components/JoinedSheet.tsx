import { useEffect, useMemo, useState } from "preact/hooks";
import type { GameDetail } from "../api";
import { dayShort, slashDate, timeHM, weekday } from "../format";
import { hasPushSubscription, notificationPermission, pushSupport, subscribeToPush, type PushSupport } from "../push";
import { shareURL, whatsappURL } from "../share";
import { initial } from "./Table";

interface Props {
  detail: GameDetail;
  meID: string | undefined;
  toast: (text: string) => void;
  /** 「加到日历」：交给日历弹层（#6）。 */
  onCalendar: () => void;
  /**
   * 微信 / WhatsApp 内置浏览器、iOS Safari 标签页里点「提醒我」：先出引导屏（#70 的 PushGuide）。
   * 不给的话退回 B5 的样子，提示换个方式。
   */
  onGuide?: () => void;
  onClose: () => void;
}

type Mode =
  /** B4 / B6：问要不要提醒。 */
  | { kind: "ask" }
  /** B5：这台设备收不到，换成日历和 WhatsApp。 */
  | { kind: "alt"; hint: string }
  /** 这台设备已经订阅过：提醒本来就会到。 */
  | { kind: "on" };

const HINT_UNSUPPORTED = "这台手机收不到网页提醒，换个方式";

/** 进来时该是哪种样子。权限被拒过的，再弹系统框也不会出来，直接换方式。 */
function initialMode(support: PushSupport): Mode {
  if (support.kind === "unsupported") return { kind: "alt", hint: HINT_UNSUPPORTED };
  if (support.kind === "ready" && notificationPermission() === "denied") {
    return { kind: "alt", hint: "通知被关掉了，换个方式（设置 › 通知 里能再打开）" };
  }
  return { kind: "ask" };
}

/** 进不了引导屏时的说法（#70 合并前，或者调用方不需要引导）。 */
function guideHint(support: PushSupport): string {
  if (support.kind === "in-app-browser") {
    return support.app === "wechat" ? "微信里收不到网页提醒，换个方式" : "WhatsApp 里收不到网页提醒，换个方式";
  }
  return "iPhone 要先加到主屏幕才收得到提醒，先换个方式";
}

/**
 * 11 · 报名成功弹层（设计稿 B4 / B5 / B6，方案 §3）。
 *
 * 报上名的这一刻用户最有动机开提醒，所以在这里问。按这台设备的能力分三种样子：
 *   - B4：能收推送（或者跳出去 / 加主屏幕之后能收）→「提醒我」+「加到日历」；
 *   - B5：收不到 →「加到日历」+「用 WhatsApp 提醒我」，问句下面说一句为什么；
 *   - B6：刚好坐满 → 标题换成「成局了！4 人到齐」，其余同 B4。
 * 每一种都能「先不用」，报名不受影响。
 */
export function JoinedSheet({ detail, meID, toast, onCalendar, onGuide, onClose }: Props) {
  const support = useMemo(() => pushSupport(), []);
  const [mode, setMode] = useState<Mode>(() => initialMode(support));
  const [busy, setBusy] = useState(false);

  // 已经订阅过的设备不用再问：提醒本来就会到。
  useEffect(() => {
    if (support.kind !== "ready" || notificationPermission() !== "granted") return;
    let live = true;
    void hasPushSubscription().then((has) => {
      if (live && has) setMode({ kind: "on" });
    });
    return () => {
      live = false;
    };
  }, [support]);

  const { game } = detail;
  const full = game.status === "full" || detail.missing <= 0;
  const seated = [...detail.seats].sort((a, b) => a.seat_no - b.seat_no);
  const blanks = Math.max(0, game.capacity - seated.length);

  const remind = async () => {
    if (busy) return;
    if (support.kind === "in-app-browser" || support.kind === "ios-needs-home") {
      if (onGuide) onGuide();
      else setMode({ kind: "alt", hint: guideHint(support) });
      return;
    }
    if (support.kind !== "ready") {
      setMode({ kind: "alt", hint: HINT_UNSUPPORTED });
      return;
    }
    setBusy(true);
    const result = await subscribeToPush();
    setBusy(false);
    if (result.kind === "ok") {
      toast("好，开局前 1 小时提醒你");
      onClose();
    } else if (result.kind === "denied") {
      setMode({ kind: "alt", hint: "通知没打开，换个方式（设置 › 通知 里能再打开）" });
    } else {
      setMode({ kind: "alt", hint: "这台手机没开成提醒，换个方式" });
    }
  };

  const reminderText = `提醒自己：${dayShort(game.starts_at)} ${timeHM(game.starts_at)} ${game.venue} 打掼蛋 👉 ${shareURL(game.short_code)}`;

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet joined" role="dialog" aria-modal="true" data-testid="joined-sheet" aria-label={full ? "成局了" : "占上了"}>
        <div class="grab" />
        <div class="check" aria-hidden="true">
          ✓
        </div>
        <h3>{full ? `成局了！${game.capacity} 人到齐` : "占上了！"}</h3>

        <div class="mini">
          <div class="mini-text">
            <div class="w serif">
              {weekday(game.starts_at)} {slashDate(game.starts_at)} {timeHM(game.starts_at)}
            </div>
            <div class="v">{game.venue}</div>
          </div>
          <div class="dots" aria-label={`${seated.length}/${game.capacity}`}>
            {seated.map((s) => (
              <i
                key={s.seat_no}
                class={s.user.id === detail.host.id ? "host" : s.user.id === meID ? "me" : ""}
              >
                {initial(s.user.nickname)}
              </i>
            ))}
            {Array.from({ length: blanks }, (_, i) => (
              <i key={`b${i}`} class="blank" />
            ))}
            <b class="count serif">
              {seated.length}/{game.capacity}
            </b>
          </div>
        </div>

        {mode.kind === "on" ? (
          <>
            <p class="ask">提醒已经开着</p>
            <p class="ask-sub">开局前 1 小时会通知你</p>
            <div class="stack">
              <button class="btn primary" onClick={onClose}>
                好的，到时见
              </button>
              <button class="btn ghost" onClick={onCalendar}>
                加到日历
              </button>
            </div>
          </>
        ) : (
          <>
            <p class="ask">开局前 1 小时提醒你？</p>
            {mode.kind === "alt" && <p class="ask-sub">{mode.hint}</p>}
            <div class="stack">
              {mode.kind === "ask" ? (
                <>
                  <button class="btn primary" disabled={busy} onClick={() => void remind()}>
                    {busy ? "正在开…" : "提醒我"}
                  </button>
                  <button class="btn ghost" onClick={onCalendar}>
                    加到日历
                  </button>
                </>
              ) : (
                <>
                  <button class="btn primary" onClick={onCalendar}>
                    加到日历
                  </button>
                  <a class="btn ghost" href={whatsappURL(reminderText)} target="_blank" rel="noopener">
                    用 WhatsApp 提醒我
                  </a>
                </>
              )}
              <button class="textlink" onClick={onClose}>
                先不用
              </button>
            </div>
          </>
        )}
      </div>
    </>
  );
}
