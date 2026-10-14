import { useEffect, useState } from "preact/hooks";
import { getMe, type UserRef } from "../api";
import { ClaimSheet } from "../components/ClaimSheet";
import { initial } from "../components/Table";
import { WaSheet } from "../components/WaSheet";
import { maskWaPhone } from "../wa";

interface Props {
  me: UserRef | null;
  /** 还没有身份时，点一下填称呼。 */
  requireIdentity: () => Promise<UserRef | null>;
  toast: (text: string) => void;
  onDevices: () => void;
  onPrivacy: () => void;
}

/**
 * G1 · 我的。
 *
 * - 有身份时：**在别的设备上登录**（G3，认领码，#116）、**绑定 WhatsApp**（G2，#116）；
 * - **登录的设备**（G4，§10.3）和**隐私与数据**（G5，§14）。
 */
export function Me({ me, requireIdentity, toast, onDevices, onPrivacy }: Props) {
  const [waPhone, setWaPhone] = useState<string | undefined>(undefined);
  const [sheet, setSheet] = useState<"" | "claim" | "wa">("");

  // 绑定的号码只在 /api/me 里给本人看；换了身份就重新问。
  useEffect(() => {
    setWaPhone(undefined);
    if (!me) return;
    let live = true;
    getMe()
      .then((body) => live && setWaPhone(body.wa_phone || undefined))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [me?.id]);

  return (
    <div class="body has-bar">
      <div class="head">
        <h1>我的</h1>
      </div>

      {me ? (
        <div class="list">
          <div class="li">
            <span class="avatar" aria-hidden="true">
              {initial(me.nickname)}
            </span>
            <div class="tx">
              {me.nickname}
              <small>这个称呼就是你在这个站上的名字</small>
            </div>
          </div>
        </div>
      ) : (
        <>
          <p class="empty-state">
            还没有身份。
            <br />
            填个称呼就能报名、开一局。
          </p>
          <div class="pad">
            <button class="btn primary" onClick={() => void requireIdentity()}>
              填个称呼
            </button>
          </div>
        </>
      )}

      {me && (
        <div class="list" style={{ marginTop: "var(--s3)" }}>
          <button class="li tap" type="button" onClick={() => setSheet("claim")}>
            <div class="tx">
              在别的设备上登录
              <small>换手机、加到主屏幕后找回自己：给你一串认领码</small>
            </div>
            <span class="chev">›</span>
          </button>
          <button class="li tap" type="button" onClick={() => setSheet("wa")}>
            <div class="tx">
              绑定 WhatsApp
              <small>可选。清了缓存也能凭号码找回</small>
            </div>
            <span class="val" data-testid="wa-status">
              {waPhone ? maskWaPhone(waPhone) : "去绑定"}
            </span>
            <span class="chev">›</span>
          </button>
        </div>
      )}

      <div class="list" style={{ marginTop: "var(--s3)" }}>
        <button class="li tap" type="button" onClick={onDevices} disabled={!me}>
          <div class="tx">
            登录的设备
            <small>看看哪些设备认得这个身份，不认识的可以退掉</small>
          </div>
          <span class="chev">›</span>
        </button>
        <button class="li tap" type="button" onClick={onPrivacy}>
          <div class="tx">
            隐私与数据
            <small>我们存了什么、存多久、怎么注销</small>
          </div>
          <span class="chev">›</span>
        </button>
      </div>

      <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>

      {sheet === "claim" && <ClaimSheet toast={toast} onClose={() => setSheet("")} />}
      {sheet === "wa" && (
        <WaSheet current={waPhone} toast={toast} onChange={setWaPhone} onClose={() => setSheet("")} />
      )}
    </div>
  );
}
