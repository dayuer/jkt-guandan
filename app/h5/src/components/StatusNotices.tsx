import { dayShort, timeHM } from "../format";
import type { Notice } from "../../../shared/src/status";
import "../status.css";

/** 横幅上「改到哪了」的说法：只改了时间就说时间，只改了地点就说地点。 */
export function rescheduleText(n: Extract<Notice, { kind: "rescheduled" }>, withVenue = false, now?: number): string {
  const when = `${dayShort(n.startsAt, now)} ${timeHM(n.startsAt)}`;
  if (n.timeChanged && n.venueChanged) return `局头把时间改到 ${when}、地点改到 ${n.venue} 了`;
  if (n.venueChanged) return `局头把地点改到 ${n.venue} 了`;
  return withVenue ? `局头把 ${n.venue} 那局改到 ${when} 了` : `局头把时间改到 ${when} 了`;
}

interface BannerProps {
  notice: Exclude<Notice, { kind: "promoted" }>;
  /** 首页上说清楚是哪一局；详情页上不用。 */
  withVenue?: boolean;
  onOpen?: () => void;
  onDismiss: () => void;
}

/**
 * R1 改时间横幅（浅红）和 K2 局头转交横幅（深色）。都能关，关了才算「知道了」。
 */
export function NoticeBanner({ notice, withVenue, onOpen, onDismiss }: BannerProps) {
  const text =
    notice.kind === "rescheduled" ? (
      rescheduleText(notice, withVenue)
    ) : (
      <>
        {notice.formerHost && <b>{notice.formerHost}退出了，</b>}
        你现在是局头。空出的座位已开放报名。
      </>
    );

  return (
    <div class={`banner notice ${notice.kind === "host" ? "ink" : "red"}`} role="status" data-testid={`notice-${notice.kind}`}>
      <span class="ic" aria-hidden="true">
        {notice.kind === "host" ? "♛" : "◷"}
      </span>
      {onOpen ? (
        <button class="tx link-like" onClick={onOpen}>
          {text} ›
        </button>
      ) : (
        <span class="tx">{text}</span>
      )}
      <button class="x" aria-label="知道了" onClick={onDismiss}>
        ×
      </button>
    </div>
  );
}

interface SheetProps {
  /** 「今天 19:30 · Pacific Place」 */
  startsAt: number;
  venue: string;
  busy?: boolean;
  onOk: () => void;
  onCalendar: () => void;
  onLeave: () => void;
}

/**
 * W3 · 候补转正弹层：有人退出，系统已经把座位给了排第一的候补。
 *
 * 三个出口：「好的，到时见」、「加到日历」、「去不了了，让给下一位」（就是退出，下一位候补会被补上）。
 */
export function PromotedSheet({ startsAt, venue, busy, onOk, onCalendar, onLeave }: SheetProps) {
  return (
    <>
      <div class="scrim" onClick={onOk} />
      <div class="sheet promoted" role="dialog" aria-modal="true" aria-label="轮到你了" data-testid="promoted-sheet">
        <div class="grab" />
        <div class="medal" aria-hidden="true">
          ♥
        </div>
        <h3>有人退出，你补上了！</h3>
        <p class="sub">
          {dayShort(startsAt)} {timeHM(startsAt)} · {venue}
        </p>
        <p class="sub">轮到你了，已经给你占上座</p>
        <div class="stack">
          <button class="btn primary" disabled={busy} onClick={onOk}>
            好的，到时见
          </button>
          <button class="btn ghost" disabled={busy} onClick={onCalendar}>
            加到日历
          </button>
          <button class="textlink" disabled={busy} onClick={onLeave}>
            去不了了，让给下一位
          </button>
        </div>
      </div>
    </>
  );
}
