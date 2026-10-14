import { calendarOption } from "../calendar";

interface Props {
  code: string;
  /** 刚报上名时弹出来，标题换成「占上了」。 */
  justJoined?: boolean;
  onClose: () => void;
}

/**
 * 「加入日历」弹层（#6，方案 §6）。
 *
 * 浏览器里给 .ics（iOS Safari / 安卓 Chrome 打开就导入系统日历）和 Google Calendar 两个出口；
 * 微信内置浏览器下载不了 .ics、也登不上 Google，只提示先跳到浏览器再加。
 */
export function CalendarSheet({ code, justJoined, onClose }: Props) {
  const option = calendarOption(code);

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet" role="dialog" aria-modal="true" data-testid="calendar-sheet">
        <div class="grab" />
        <h3>{justJoined ? "占上了！加到日历里？" : "加入日历"}</h3>
        {option.kind === "links" ? (
          <>
            <p class="sub">开局前日历会提醒你；局头改了时间，再点一次就能更新</p>
            <div class="stack">
              <a class="btn primary" href={option.ics} data-testid="calendar-ics">
                加到手机日历
              </a>
              <a class="btn ghost" href={option.google} target="_blank" rel="noopener" data-testid="calendar-google">
                Google 日历
              </a>
              <button class="textlink" onClick={onClose}>
                先不用
              </button>
            </div>
          </>
        ) : (
          <>
            <p class="sub" data-testid="calendar-wechat-hint">
              微信里加不了日历：点右上角「···」，选「在浏览器打开」，再点「加入日历」
            </p>
            <div class="stack">
              <button class="btn soft" onClick={onClose}>
                知道了
              </button>
            </div>
          </>
        )}
      </div>
    </>
  );
}
