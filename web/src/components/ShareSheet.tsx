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

  const copy = async () => {
    if (await copyText(text)) {
      setCopied(true);
      toast("话术已复制");
    } else {
      toast("复制不了，长按上面的话术自己选吧");
    }
  };

  const savePoster = () => {
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
      <div class="sheet share" role="dialog" aria-modal="true" data-testid="share-sheet">
        <div class="grab" />
        <h3>发到群里</h3>

        <div class="poster-frame">
          {!posterFailed ? (
            <img
              class="poster"
              src={poster}
              alt="牌局海报"
              width={270}
              height={360}
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
              ⬇
            </span>
            保存海报
          </button>
          <button class="outlet" onClick={() => void copy()}>
            <span class="ico" aria-hidden="true">
              ⧉
            </span>
            复制话术
          </button>
          <a class="outlet" href={whatsappURL(text)} target="_blank" rel="noopener noreferrer">
            <span class="ico wa" aria-hidden="true">
              ✆
            </span>
            WhatsApp
          </a>
          {canSystemShare() && (
            <button class="outlet" onClick={() => void shareToSystem()}>
              <span class="ico" aria-hidden="true">
                ⇪
              </span>
              更多
            </button>
          )}
        </div>

        <div class="script">
          <p>{text}</p>
          <button class="btn soft small" onClick={() => void copy()}>
            {copied ? "已复制 ✓" : "复制"}
          </button>
        </div>

        <button class="textlink" onClick={onClose}>
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
