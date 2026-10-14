import type { UserRef } from "../api";
import { initial } from "../components/Table";

interface Props {
  me: UserRef | null;
  /** 还没有身份时，点一下填称呼。 */
  requireIdentity: () => Promise<UserRef | null>;
  onDevices: () => void;
  onPrivacy: () => void;
}

/**
 * G1 · 我的。
 *
 * 这一轮只放两项：**登录的设备**（G4，§10.3）和**隐私与数据**（G5，§14）。
 * 「开局提醒」和「换手机也能找回」属于 phase 2（Web Push、WhatsApp 绑定），
 * 先不放，免得点进去是空的。
 */
export function Me({ me, requireIdentity, onDevices, onPrivacy }: Props) {
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

      <div class="list">
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
    </div>
  );
}
