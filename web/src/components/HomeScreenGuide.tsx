import { useEffect, useState } from "preact/hooks";
import { issueClaim } from "../api";
import "../guides.css";

interface Props {
  /** 有身份才签发认领码。 */
  signedIn: boolean;
  /** 「不装了，加到日历」。 */
  onCalendar: () => void;
  onClose: () => void;
}

/**
 * H2 · iOS 加到主屏幕（设计稿 13）。
 *
 * iOS 只有从主屏幕图标打开才有 PushManager（push.ts 的 `ios-needs-home`），所以只在 Safari 标签页里出。
 * 主屏幕 App 和 Safari 的存储是分开的，cookie 不一定跟过去（#8 待真机确认），
 * 所以这里给一张认领码卡片：主屏幕打开时如果没认出来，输入这 6 个数字（H4）。
 */
export function HomeScreenGuide({ signedIn, onCalendar, onClose }: Props) {
  const [code, setCode] = useState("");

  useEffect(() => {
    if (!signedIn) return;
    let alive = true;
    issueClaim()
      .then((ticket) => alive && setCode(ticket.code))
      .catch(() => {
        // 限流或断网：不给码也能装，打开后还可以重新填称呼。
      });
    return () => {
      alive = false;
    };
  }, [signedIn]);

  return (
    <div class="homeguide" role="dialog" aria-modal="true" aria-label="放到主屏幕" data-testid="guide-homescreen">
      <button class="homeguide-close" aria-label="关闭" onClick={onClose}>
        ×
      </button>
      <h1>iPhone 要多一步，才能收到提醒</h1>

      <section class="homeguide-step">
        <h2>
          <b>1</b>点底部的分享按钮
        </h2>
        <div class="mock-bar" aria-hidden="true">
          <span>‹</span>
          <span class="dim">›</span>
          <span class="ring">
            <ShareIcon />
          </span>
          <span>□</span>
          <span>⧉</span>
        </div>
      </section>

      <section class="homeguide-step">
        <h2>
          <b>2</b>往下滑，点「添加到主屏幕」
        </h2>
        <ul class="mock-sheet" aria-hidden="true">
          <li>
            拷贝<span>⧉</span>
          </li>
          <li>
            添加到阅读列表<span>○○</span>
          </li>
          <li class="hit">
            添加到主屏幕<span>⊞</span>
          </li>
        </ul>
      </section>

      <section class="homeguide-step">
        <h2>
          <b>3</b>回到桌面，点「掼蛋」图标打开
        </h2>
        <div class="mock-home" aria-hidden="true">
          <span>
            <i />
            日历
          </span>
          <span>
            <i />
            信息
          </span>
          <span class="me">
            <i>♥</i>
            掼蛋
          </span>
          <span>
            <i />
            相机
          </span>
        </div>
      </section>

      {code && (
        <section class="claimcard" data-testid="claim-code">
          <p>你的认领码</p>
          <div class="digits" aria-label={`认领码 ${code}`}>
            {code.split("").map((d, i) => (
              <span key={i}>{d}</span>
            ))}
          </div>
          <small>主屏幕打开时如果需要，输入这 6 个数字 · 10 分钟内有效</small>
        </section>
      )}

      <button class="textlink" onClick={onCalendar}>
        不装了，加到日历
      </button>

      <div class="homeguide-hint" aria-hidden="true">
        <span class="bounce">↓</span>
      </div>
    </div>
  );
}

function ShareIcon() {
  return (
    <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
      <path d="M12 3v12M7.5 7.5 12 3l4.5 4.5M6 11H4.5v9.5h15V11H18" fill="none" stroke="currentColor" stroke-width="1.8" />
    </svg>
  );
}
