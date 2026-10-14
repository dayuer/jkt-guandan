import { useState } from "preact/hooks";
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

  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const me = await createMe(nickname);
      // 只留一个非敏感标记，真正的凭证在 HttpOnly cookie 里（§10.3）。
      markHasIdentity();
      onDone(me.user.id, me.user.nickname);
    } catch (err) {
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
            onInput={(e) => setNickname((e.target as HTMLInputElement).value)}
            onKeyDown={(e) => {
              // 输入法还在拼字时的回车是「上屏」，不是提交；拼完再按回车才提交（#130 第 3 条）。
              if (e.key === "Enter" && !e.isComposing) {
                e.preventDefault();
                (e.currentTarget as HTMLInputElement).form?.requestSubmit();
              }
            }}
            placeholder="比如：老王"
            maxLength={12}
            autoFocus
            aria-label="称呼"
            name="gd-nickname"
            // 不要浏览器的自动填充条：它会盖住「就用这个名字」，而且填进来的多半是真名（#130）。
            autoComplete="off"
            autoCapitalize="off"
            spellcheck={false}
            enterKeyHint="done"
          />
          {error && <p class="error">{error}</p>}
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
