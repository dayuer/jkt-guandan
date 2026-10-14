import { useEffect, useState } from "preact/hooks";
import { issueClaim } from "../api";
import { jumpOutLabels, withClaim, withoutClaim } from "../guide";
import type { InAppBrowser } from "../push";
import "../guides.css";

interface Props {
  app: InAppBrowser;
  /** iPhone 上微信的菜单项叫「在 Safari 中打开」。 */
  ios: boolean;
  /** 有身份才签发认领令牌；没有身份就只教怎么跳。 */
  signedIn: boolean;
  /** 「算了，加日历就行」。 */
  onCalendar: () => void;
  onClose: () => void;
}

/**
 * H1 · 跳出微信 / WhatsApp / Instagram / Facebook / LINE（设计稿 12）。
 *
 * 一出现就签发 claim 令牌并 `replaceState` 到带令牌的地址（§5.2「跳出微信前」）：
 * 用户在菜单里点「在浏览器中打开」，打开的就是这个地址，浏览器那边启动时自动认回身份。
 * 用户不跳了就把令牌从地址栏拿掉，免得顺手「发送给朋友」时把它带出去。
 */
export function JumpOutGuide({ app, ios, signedIn, onCalendar, onClose }: Props) {
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!signedIn) return;
    let alive = true;
    issueClaim()
      .then((ticket) => {
        if (!alive) return;
        window.history.replaceState(null, "", withClaim(window.location.href, ticket.claim_token));
        setSaved(true);
      })
      .catch(() => {
        // 签发失败（限流、断网）也照样教怎么跳：到了浏览器那边还能用认领码。
      });
    return () => {
      alive = false;
    };
  }, [signedIn]);

  const dismiss = (then: () => void) => {
    window.history.replaceState(null, "", withoutClaim(window.location.href));
    then();
  };

  const { menu, item: openItem } = jumpOutLabels(app, ios);

  return (
    <div class="jumpout" role="dialog" aria-modal="true" aria-label="在浏览器中打开" data-testid="guide-jumpout">
      <svg class="jumpout-arrow" viewBox="0 0 120 140" aria-hidden="true">
        <path d="M14 132 C 20 70, 50 30, 104 16" />
        <path d="M84 8 L 106 15 L 92 34" />
      </svg>

      <ol class="jumpout-steps">
        <li>
          <span class="n">①</span>点右上角「{menu}」
        </li>
        <li>
          <span class="n">②</span>选「{openItem}」
          {app === "wechat" && !ios && <small>iPhone 上叫「在 Safari 中打开」</small>}
        </li>
      </ol>

      <div class="jumpout-menu" aria-hidden="true">
        <span>
          <i>↗</i>发送给朋友
        </span>
        <span>
          <i>☆</i>收藏
        </span>
        <span class="hit">
          <i>◐</i>
          {openItem}
        </span>
        <span>
          <i>⧉</i>复制链接
        </span>
      </div>

      <div class="jumpout-foot">
        <p>{saved ? "你的报名已经保存，打开后自动认出你" : "你的报名已经保存"}</p>
        <button class="jumpout-alt" onClick={() => dismiss(onCalendar)}>
          算了，加日历就行
        </button>
        <button class="jumpout-close" onClick={() => dismiss(onClose)}>
          先不用
        </button>
      </div>
    </div>
  );
}
