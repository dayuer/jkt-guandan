import { useState } from "preact/hooks";
import { ApiError, redeemClaimCode, signOutCurrentDevice, type UserRef } from "../api";
import { CLAIM_CODE_LENGTH, markWelcomed, normaliseCode } from "../guide";
import { clearIdentityMarker, markHasIdentity } from "../identity";
import { notificationPermission, pushSupport, subscribeToPush } from "../push";
import "../guides.css";

interface Props {
  /** /api/me 问出来的身份：iOS 加主屏幕时 cookie 跟过来了就有（#8），否则是 null。 */
  me: UserRef | null;
  /** 认回身份（或点了「不是」退出）之后告诉外壳。 */
  onIdentity: (user: UserRef | null) => void;
  /** 走完或跳过，回到牌局。 */
  onDone: () => void;
}

type Step =
  /** H3：认出你了，确认一下。 */
  | "who"
  /** H4：没认出来，输入认领码。 */
  | "code"
  /** H5：最后一步，打开提醒。 */
  | "push"
  /** H6：系统权限框正弹着。 */
  | "asking"
  /** H7：允许了。 */
  | "granted"
  /** H8：不允许。 */
  | "denied"
  /** 订阅失败（拿不到公钥、断网）：和 H8 一样给出口，只是说法不同。 */
  | "failed";

/** 身份落定之后，这台设备还用不用问提醒。 */
function nextAfterIdentity(): Step | null {
  if (pushSupport().kind !== "ready") return null;
  if (notificationPermission() === "granted") return null;
  return "push";
}

/**
 * H3–H8 · 主屏幕 App 首次打开（设计稿 14）：认出你 → 打开提醒。
 *
 * 每一步都能跳过（#10 的完成标志）；走完或跳过一次就记下来，不再出现。
 */
export function FirstLaunch({ me, onIdentity, onDone }: Props) {
  const [step, setStep] = useState<Step>(me ? "who" : "code");
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState("");
  const [busy, setBusy] = useState(false);

  const finish = () => {
    markWelcomed();
    onDone();
  };

  const afterIdentity = () => {
    const next = nextAfterIdentity();
    if (next) setStep(next);
    else finish();
  };

  const notMe = async () => {
    setBusy(true);
    try {
      await signOutCurrentDevice();
    } catch {
      // 退不掉也照样让他输认领码：兑换会被服务端拒绝，届时会提示。
    }
    clearIdentityMarker();
    onIdentity(null);
    setBusy(false);
    setStep("code");
  };

  const onCodeInput = async (raw: string) => {
    const digits = normaliseCode(raw);
    setCode(digits);
    setCodeError("");
    if (digits.length < CLAIM_CODE_LENGTH) return;

    setBusy(true);
    try {
      const { user } = await redeemClaimCode(digits);
      markHasIdentity();
      onIdentity(user);
      afterIdentity();
    } catch (err) {
      setCode("");
      setCodeError(
        err instanceof ApiError && err.status === 429
          ? "试得太频繁了，过一分钟再来"
          : "认领码不对或已过期。认领码只在原来那台手机所在的网络里有效",
      );
    } finally {
      setBusy(false);
    }
  };

  const enablePush = async () => {
    setStep("asking");
    const result = await subscribeToPush();
    setStep(result.kind === "ok" ? "granted" : result.kind === "denied" ? "denied" : "failed");
  };

  if (step === "who" || step === "code") {
    return (
      <main class="firstrun" data-testid={`firstrun-${step}`}>
        <div class="firstrun-icon" aria-hidden="true">
          ♥
        </div>
        <h1>欢迎回来</h1>

        {step === "who" && me && (
          <section class="firstrun-card">
            <h2>你是 {me.nickname} 吗？</h2>
            <p>这台手机上的报名和提醒都会跟着这个称呼</p>
            <div class="row">
              <button class="btn primary" disabled={busy} onClick={afterIdentity}>
                是我
              </button>
              <button class="btn ghost" disabled={busy} onClick={() => void notMe()}>
                不是
              </button>
            </div>
          </section>
        )}

        {step === "code" && (
          <section class="firstrun-card">
            <h3>输入认领码</h3>
            <p>在原来那台手机的「放到主屏幕」页面上能看到，10 分钟内有效</p>
            <label class={`codecells${codeError ? " error" : ""}`}>
              <input
                inputMode="numeric"
                autoComplete="one-time-code"
                aria-label="认领码"
                maxLength={CLAIM_CODE_LENGTH}
                value={code}
                disabled={busy}
                onInput={(e) => void onCodeInput((e.currentTarget as HTMLInputElement).value)}
              />
              {Array.from({ length: CLAIM_CODE_LENGTH }, (_, i) => (
                <span key={i} class={i === code.length ? "cur" : ""} aria-hidden="true">
                  {code[i] ?? ""}
                </span>
              ))}
            </label>
            {codeError && (
              <p class="codeerror" role="alert">
                {codeError}
              </p>
            )}
          </section>
        )}

        {step === "code" && (
          <button class="textlink" onClick={finish}>
            先不用，随便看看
          </button>
        )}
      </main>
    );
  }

  const solid = step === "granted";
  const title =
    step === "granted"
      ? "搞定，到点叫你"
      : step === "denied"
        ? "没关系"
        : step === "failed"
          ? "这次没打开"
          : "最后一步：打开提醒";
  const sub =
    step === "denied"
      ? "可以去 设置 › 通知 › 掼蛋 里打开"
      : step === "failed"
        ? "网络不稳或系统没给权限，可以先用日历记着"
        : "开局前 1 小时、有人退出、局头改时间，都会告诉你";

  return (
    <main class="firstrun" data-testid={`firstrun-${step}`}>
      <div class={`bell${solid ? " solid" : ""}`} aria-hidden="true">
        <svg viewBox="0 0 64 64">
          <path d="M14 46 C 20 40, 20 30, 20 24 A 12 12 0 0 1 44 24 C 44 30, 44 40, 50 46 Z" />
          <path class="clapper" d="M27 52 Q 32 57 37 52" />
        </svg>
      </div>
      <h1>{title}</h1>
      <p class="firstrun-sub">{sub}</p>
      {step === "asking" && <p class="firstrun-sub">在系统弹窗里点「允许」</p>}

      <div class="firstrun-actions">
        {step === "push" && (
          <>
            <button class="btn primary" onClick={() => void enablePush()}>
              打开提醒
            </button>
            <button class="textlink" onClick={finish}>
              以后再说
            </button>
          </>
        )}
        {step === "granted" && (
          <button class="btn primary" onClick={finish}>
            回到牌局
          </button>
        )}
        {(step === "denied" || step === "failed") && (
          // 日历按钮在每一局的详情页上；回到牌局列表，点进那一局就能加。
          <button class="btn primary" onClick={finish}>
            改用日历
          </button>
        )}
      </div>
    </main>
  );
}
