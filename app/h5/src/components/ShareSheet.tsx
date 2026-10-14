import { useEffect, useRef, useState } from "preact/hooks";
import type { GameDetail } from "../api";
import {
  canSystemShare,
  copyText,
  posterURL,
  shareText,
  shareURL,
  systemShare,
  systemShareText,
  whatsappURL,
} from "../share";
import { track, type ShareChannel } from "../telemetry";

interface Props {
  detail: GameDetail;
  toast: (text: string) => void;
  onClose: () => void;
}

/**
 * C1 · 分享面板（设计稿 04、docs/h5-pwa-plan.md §5）。
 *
 * 微信里的链接渲染不出牌桌卡片，所以给四个出口：海报（长按保存）、复制话术、
 * WhatsApp（那边有 OG 原生卡片）、系统分享（支持时才有）。海报是服务端渲染的
 * 竖版图，带上牌局版本号，座位一变图就跟着变。
 */
export function ShareSheet({ detail, toast, onClose }: Props) {
  const [copied, setCopied] = useState(false);
  // 服务端的海报接口挂了或还没上线时，别让用户对着一张碎图长按。
  const [posterFailed, setPosterFailed] = useState(false);

  const text = shareText(detail);
  const link = shareURL(detail.game.short_code);
  const poster = posterURL(detail.game.short_code, detail.game.version);
  const inWeChat = isWeChat();

  // 牌局变了（换了局，或者有人报名导致 version +1、海报 URL 跟着变）就把上一局的状态清掉：
  // 不然「已复制 ✓」和「海报没加载出来」会挂到新的一张图上。
  // 只在**海报真的换了**的时候清状态：effect 在挂载后是异步跑的，无条件清一次会把
  // 用户在这之前刚点出来的「已复制 ✓」和海报加载结果一起抹掉。
  const lastPoster = useRef(poster);
  useEffect(() => {
    if (lastPoster.current === poster) return;
    lastPoster.current = poster;
    setCopied(false);
    setPosterFailed(false);
  }, [poster]);

  const channel = (c: ShareChannel) => track("share_channel", { game: detail.game.short_code, channel: c });

  const copy = async () => {
    channel("copy");
    if (await copyText(text)) {
      setCopied(true);
      toast("话术已复制");
    } else {
      toast("复制不了，长按上面的话术自己选吧");
    }
  };

  const savePoster = () => {
    channel("poster");
    if (inWeChat) {
      // 微信内置浏览器里 a[download] 基本没用，长按保存是唯一可靠的路子。
      toast("长按上面的海报就能保存");
      return;
    }
    const a = document.createElement("a");
    a.href = poster;
    a.download = `guandan-${detail.game.short_code}.png`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    toast("海报开始下载");
  };

  const shareToSystem = async () => {
    channel("system");
    const result = await systemShare({
      title: `随时掼掼蛋 · ${detail.game.venue}`,
      // 正文不带链接：链接单独走 url，不然目标 App 里会看到两遍。
      text: systemShareText(detail),
      url: link,
    });
    if (result === "failed") toast("分享没成功，试试复制话术");
    if (result === "unsupported") toast("这台设备不支持系统分享，用复制话术吧");
  };

  return (
    <>
      <div class="scrim" onClick={onClose} />
      {/* C1 没有标题：海报本身就说明了要干什么。读屏用 aria-label。 */}
      <div class="sheet share" role="dialog" aria-modal="true" aria-label="分享这一局" data-testid="share-sheet">
        <div class="grab" />

        <div class="poster-frame">
          {!posterFailed ? (
            <img
              class="poster"
              src={poster}
              alt="牌局海报"
              width={188}
              height={250}
              onError={() => setPosterFailed(true)}
            />
          ) : (
            <div class="poster placeholder" aria-hidden="true">
              <span>{detail.game.venue}</span>
              <small>海报没加载出来 · 先把话术复制到群里</small>
            </div>
          )}
        </div>
        <p class="hint">长按图片保存，发到群里</p>

        <div class="outlets">
          <button class="outlet" disabled={posterFailed} onClick={savePoster}>
            <span class="ico" aria-hidden="true">
              <IconDownload />
            </span>
            保存海报
          </button>
          <button class="outlet" onClick={() => void copy()}>
            <span class="ico" aria-hidden="true">
              <IconCopy />
            </span>
            复制话术
          </button>
          <a
            class="outlet"
            href={whatsappURL(text)}
            target="_blank"
            rel="noopener noreferrer"
            onClick={() => channel("whatsapp")}
          >
            <span class="ico wa" aria-hidden="true">
              <IconWhatsApp />
            </span>
            WhatsApp
          </a>
          {canSystemShare() && (
            <button class="outlet" onClick={() => void shareToSystem()}>
              <span class="ico" aria-hidden="true">
                <IconMore />
              </span>
              更多
            </button>
          )}
        </div>

        <div class="script">
          <p>{text}</p>
          <button class="btn primary small" onClick={() => void copy()}>
            {copied ? "已复制 ✓" : "复制"}
          </button>
        </div>

        <button class="btn soft cancel" onClick={onClose}>
          取消
        </button>

        {/* 图没加载出来时「保存海报」是禁用的，但话术照样能复制——那是最稳的出口。 */}
      </div>
    </>
  );
}

/** 微信内置浏览器：长按保存是唯一可靠的路子，a[download] 会被忽略。 */
function isWeChat(): boolean {
  return typeof navigator !== "undefined" && /micromessenger/i.test(navigator.userAgent ?? "");
}

// ── C1 的出口图标：线条跟着 currentColor 走（米色圆底上是红色，WhatsApp 绿底上是白色）。──

function IconDownload() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M12 4v11M7 10.5l5 5 5-5M5 20h14" />
    </svg>
  );
}

function IconCopy() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round">
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M15 5.5V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h.5" />
    </svg>
  );
}

function IconWhatsApp() {
  return (
    <svg viewBox="0 0 24 24" width="26" height="26" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round">
      <path d="M4 20l1.2-4.1A8 8 0 1 1 8.3 19z" />
      <path
        d="M9 8.6c.2-.5.6-.5.9-.5h.4c.2 0 .4.1.5.4l.6 1.5c.1.2 0 .4-.1.6l-.5.6c.6 1.1 1.5 2 2.6 2.6l.6-.5c.2-.1.4-.2.6-.1l1.5.6c.3.1.4.3.4.5v.4c0 .3 0 .7-.5.9-.7.4-1.6.5-2.7 0a9 9 0 0 1-3.8-3.8c-.5-1.1-.4-2 0-2.7z"
        fill="currentColor"
        stroke="none"
      />
    </svg>
  );
}

function IconMore() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="currentColor">
      <circle cx="6" cy="12" r="1.8" />
      <circle cx="12" cy="12" r="1.8" />
      <circle cx="18" cy="12" r="1.8" />
    </svg>
  );
}
