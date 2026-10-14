import { useEffect, useState } from "preact/hooks";
import { ApiError, issueClaim, type ClaimTicket } from "../api";
import { timeHM } from "../format";
import { copyText } from "../share";
import "../guides.css";

interface Props {
  toast: (text: string) => void;
  onClose: () => void;
}

/**
 * G3 · 换手机找回 / 在别的设备上登录（#116）。
 *
 * 和 H1、H2 引导屏是同一套认领流程（§5.2）：签发一次性令牌和 6 位认领码，10 分钟有效。
 * 新手机上输入认领码（同一网络），或者打开带令牌的链接，就认回同一个身份。
 * 链接等于一次性的登录凭证，所以只给「复制」，提醒只发给自己。
 */
export function ClaimSheet({ toast, onClose }: Props) {
  const [ticket, setTicket] = useState<ClaimTicket | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    issueClaim()
      .then((t) => live && setTicket(t))
      .catch((err) => {
        if (!live) return;
        setError(
          err instanceof ApiError && err.status === 429
            ? "这一小时里拿得太多次了，过一会儿再来"
            : "没拿到认领码，稍后再试",
        );
      });
    return () => {
      live = false;
    };
  }, []);

  const copyLink = async () => {
    if (!ticket) return;
    const url = `${window.location.origin}/?claim=${encodeURIComponent(ticket.claim_token)}`;
    toast((await copyText(url)) ? "链接已复制，只发给你自己" : "复制失败，用认领码吧");
  };

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet" role="dialog" aria-modal="true" aria-label="在别的设备上登录" data-testid="claim-sheet">
        <div class="grab" />
        <h3>在别的设备上登录</h3>
        <p class="sub">在新手机或主屏幕 App 里输入这串数字，就是你本人</p>

        {ticket ? (
          <section class="claimcard" data-testid="claim-code">
            <p>你的认领码</p>
            <div class="digits" aria-label={`认领码 ${ticket.code}`}>
              {ticket.code.split("").map((d, i) => (
                <span key={i}>{d}</span>
              ))}
            </div>
            <small>{timeHM(ticket.expires_at)} 前有效 · 只能用一次 · 要在同一个网络里输入</small>
          </section>
        ) : error ? (
          <p class="error" style={{ margin: "var(--s4) 0 0" }}>
            {error}
          </p>
        ) : (
          <p class="sub">正在生成…</p>
        )}

        <div class="stack">
          {ticket && (
            <button class="btn ghost" onClick={() => void copyLink()}>
              复制登录链接
            </button>
          )}
          <button class="textlink" onClick={onClose}>
            关闭
          </button>
        </div>
      </div>
    </>
  );
}
