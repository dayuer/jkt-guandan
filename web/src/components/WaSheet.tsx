import { useState } from "preact/hooks";
import { ApiError, clearWaPhone, setWaPhone } from "../api";
import { maskWaPhone, normalizeWaPhone } from "../wa";

interface Props {
  /** 已绑定的号码（E.164）；没绑定是 undefined。 */
  current?: string;
  toast: (text: string) => void;
  /** 绑定、换号或解绑之后的新值。 */
  onChange: (phone: string | undefined) => void;
  onClose: () => void;
}

/**
 * G2 · 绑定 WhatsApp（#116）。可选：只给本人看，不出现在牌友列表里（#108）；
 * 注销时一起删掉（§14）。
 */
export function WaSheet({ current, toast, onChange, onClose }: Props) {
  const [editing, setEditing] = useState(!current);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const save = async (e: Event) => {
    e.preventDefault();
    if (busy) return;
    const phone = normalizeWaPhone(value);
    if (!phone) {
      setError("认不出这个号码。请带上国家码，比如 +62 812 3456 7890 或 +86 138 1234 5678");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const body = await setWaPhone(phone);
      onChange(body.wa_phone);
      toast("绑好了");
      onClose();
    } catch (err) {
      setError(err instanceof ApiError && err.code === "invalid_input" ? err.message : "没绑上，稍后再试");
    } finally {
      setBusy(false);
    }
  };

  const unbind = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await clearWaPhone();
      onChange(undefined);
      toast("已解绑");
      onClose();
    } catch {
      setError("没解开，稍后再试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet" role="dialog" aria-modal="true" aria-label="绑定 WhatsApp" data-testid="wa-sheet">
        <div class="grab" />
        <h3>绑定 WhatsApp</h3>
        <p class="sub">换手机或清了缓存，凭这个号码找回。只有你自己看得到，不会出现在牌友列表里。</p>

        {editing ? (
          <form onSubmit={save}>
            <div class="stack">
              <input
                type="tel"
                inputMode="tel"
                autoComplete="tel"
                aria-label="WhatsApp 号码"
                placeholder="0812 3456 7890"
                value={value}
                maxLength={24}
                onInput={(e) => setValue((e.target as HTMLInputElement).value)}
              />
              {error && <p class="error" style={{ margin: 0 }}>{error}</p>}
              <button class="btn primary" type="submit" disabled={busy || value.trim() === ""}>
                {busy ? "正在保存…" : current ? "换成这个号码" : "绑定"}
              </button>
              <button class="textlink" type="button" onClick={current ? () => setEditing(false) : onClose}>
                {current ? "算了" : "先不用"}
              </button>
            </div>
          </form>
        ) : (
          <div class="stack">
            <p class="wa-current" data-testid="wa-current">
              已绑定 {current && maskWaPhone(current)}
            </p>
            {error && <p class="error" style={{ margin: 0 }}>{error}</p>}
            <button class="btn ghost" onClick={() => setEditing(true)} disabled={busy}>
              换个号码
            </button>
            <button class="btn danger-ghost" onClick={() => void unbind()} disabled={busy}>
              {busy ? "正在解绑…" : "解绑"}
            </button>
            <button class="textlink" onClick={onClose}>
              关闭
            </button>
          </div>
        )}
      </div>
    </>
  );
}
