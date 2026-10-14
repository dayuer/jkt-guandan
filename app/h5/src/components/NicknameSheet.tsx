import { useState } from "preact/hooks";
import { tapFocusHandlers } from "../tapFocus";
import { ApiError, createMe } from "../api";
import { markHasIdentity } from "../identity";

interface Props {
  /** 提交成功后回调，带着新建的身份。 */
  onDone: (userID: string, nickname: string) => void;
  onCancel?: () => void;
}

/**
 * 首次填称呼的底部弹层（设计稿 B2 / §5.2）。
 *
 * 免注册：填个称呼就有身份了；cookie 由服务端下发，前端不保存任何凭证。
 */
export function NicknameSheet({ onDone, onCancel }: Props) {
  const [nickname, setNickname] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // 称呼命中敏感词（#147）：和开局的地点、备注一样，输入框标红，提示放在它下面，已经写的字不动（原型 X1）。
  const [sensitive, setSensitive] = useState(false);
  // 安卓 WebView：焦点不是点出来的时候，点一下也不弹键盘；先 blur 再让原生聚焦来一遍（P0）。
  const [tap] = useState(() => tapFocusHandlers());

  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    setSensitive(false);
    try {
      const me = await createMe(nickname);
      // 只留一个非敏感标记，真正的凭证在 HttpOnly cookie 里（§10.3）。
      markHasIdentity();
      onDone(me.user.id, me.user.nickname);
    } catch (err) {
      if (err instanceof ApiError && err.code === "sensitive_word") setSensitive(true);
      setError(err instanceof ApiError ? err.message : "提交失败，请再试一次");
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div class="scrim" onClick={onCancel} />
      <form class="sheet" onSubmit={submit}>
        <div class="grab" />
        <h3>怎么称呼你？</h3>
        <p class="sub">填一次就好，之后自动认得出你</p>
        <div class="stack">
          <input
            value={nickname}
            class={sensitive ? "err" : ""}
            aria-invalid={sensitive}
            onInput={(e) => {
              // 开始改了就把敏感词提示收起来。
              if (sensitive) {
                setSensitive(false);
                setError("");
              }
              setNickname((e.target as HTMLInputElement).value);
            }}
            {...tap}
            onKeyDown={(e) => {
              // 输入法还在拼字时的回车是「上屏」，不是提交；拼完再按回车才提交（#130 第 3 条）。
              if (e.key === "Enter" && !e.isComposing) {
                e.preventDefault();
                (e.currentTarget as HTMLInputElement).form?.requestSubmit();
              }
            }}
            placeholder="比如：老王"
            maxLength={12}
            // 不要 autoFocus（P0）：弹层是点完按钮后异步挂上的，这时的程序化 focus 已经不在用户手势里。
            // iOS Safari、微信的 WKWebView、安卓 XWeb 会把焦点给它（红框）却不弹键盘；
            // 有的 WebView 里再点一下已经有焦点的输入框也不弹，用户就卡住了。让用户自己点。
            aria-label="称呼"
            name="nickname"
            autoComplete="nickname"
            inputMode="text"
            autoCapitalize="off"
            spellcheck={false}
            enterKeyHint="done"
          />
          {sensitive && <p class="errmsg">⚠ {error}</p>}
          {error && !sensitive && <p class="error">{error}</p>}
          <button class="btn primary" type="submit" disabled={busy || nickname.trim() === ""}>
            {busy ? "正在保存…" : "就用这个名字"}
          </button>
          {onCancel && (
            <button class="textlink" type="button" onClick={onCancel}>
              先看看
            </button>
          )}
        </div>
      </form>
    </>
  );
}
