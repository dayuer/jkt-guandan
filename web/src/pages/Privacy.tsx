import { useState } from "preact/hooks";
import { ApiError, deleteMe } from "../api";

interface Props {
  /** 当前有没有身份。没有身份时只读说明，不显示注销按钮（没东西可注销）。 */
  hasIdentity: boolean;
  toast: (text: string) => void;
  onBack: () => void;
  /** 注销成功之后：清掉本地标记、回到没有身份的状态。 */
  onDeleted: () => void;
}

/**
 * G5 · 隐私与数据（§14，UU PDP）。
 *
 * 这个页面不要求登录：法规要求的是「说清楚收集什么、存多久、怎么删」，
 * 这些内容对还没有填称呼的人也应当能看到。
 */
export function Privacy({ hasIdentity, toast, onBack, onDeleted }: Props) {
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const doDelete = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await deleteMe();
      setConfirmOpen(false);
      toast("已经注销，数据都删掉了");
      onDeleted();
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? "现在这台设备没有身份" : "没删掉，请稍后再试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div class="body">
        <div class="nav">
          <button class="ico" onClick={onBack} aria-label="返回">
            ‹
          </button>
          <div>隐私与数据</div>
          <span />
        </div>

        <div class="list">
          <div class="li">
            <div class="tx">
              我们存了什么
              <small>称呼、你绑定的 WhatsApp（可选）、登录的设备、你报过的局</small>
            </div>
          </div>
          <div class="li">
            <div class="tx">
              存多久
              <small>设备 180 天没用自动删除；使用统计 1 年后删除；备份 30 天后删除</small>
            </div>
          </div>
          <div class="li">
            <div class="tx">
              拿来做什么
              <small>只用来让你和牌友互相认出来、把局的提醒发给你。不卖给谁，也没有广告</small>
            </div>
          </div>
          <div class="li">
            <div class="tx">
              注销之后会怎样
              <small>
                称呼、WhatsApp、设备和提醒都会被删除。已经结束的局里，你的座位会显示成「已注销牌友」，
                这样那一局当时有几个人还是记得准的
              </small>
            </div>
          </div>
        </div>

        {error && <p class="error">{error}</p>}

        {hasIdentity ? (
          <div class="pad">
            <button class="btn danger-ghost" onClick={() => setConfirmOpen(true)}>
              注销并删除我的数据
            </button>
          </div>
        ) : (
          <p class="empty">现在这台设备没有身份，没有可以注销的数据。</p>
        )}

        <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
      </div>

      {confirmOpen && (
        <>
          <div class="scrim" onClick={() => !busy && setConfirmOpen(false)} />
          <div class="dialog" role="dialog" aria-modal="true" data-testid="delete-dialog">
            <h3>确定注销？</h3>
            <p class="sub">
              你的称呼、WhatsApp、设备和提醒会被删除，无法恢复。你报过的局里，你的座位会显示为「已注销牌友」。
            </p>
            <div class="two">
              <button class="btn soft" disabled={busy} onClick={() => setConfirmOpen(false)}>
                不注销
              </button>
              <button class="btn primary" disabled={busy} onClick={() => void doDelete()}>
                {busy ? "正在删除…" : "注销"}
              </button>
            </div>
          </div>
        </>
      )}
    </>
  );
}
