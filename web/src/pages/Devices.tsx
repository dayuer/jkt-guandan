import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { ApiError, getMe, signOutDevice, signOutOtherDevices, type Device, type UserRef } from "../api";

interface Props {
  me: UserRef | null;
  toast: (text: string) => void;
  onBack: () => void;
  /** 退掉的是当前这台时，身份就没了。 */
  onSignedOut: () => void;
}

/**
 * G4 · 登录的设备（§10.3）。
 *
 * 免注册身份没有密码，cookie 被偷就等于身份被偷——唯一能补救的就是**看见**陌生设备
 * 并把它踢掉。所以这个页面是安全功能，不是设置项。
 */
export function Devices({ me, toast, onBack, onSignedOut }: Props) {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [currentID, setCurrentID] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // 页面每次渲染传进来的 onSignedOut 都是新函数，当依赖会让 load 每轮都变、
  // useEffect 跟着重新跑一遍（第一次进页面就发两次请求）。放 ref 里取最新的。
  const onSignedOutRef = useRef(onSignedOut);
  onSignedOutRef.current = onSignedOut;

  const load = useCallback(async () => {
    try {
      const body = await getMe();
      setDevices(body.devices);
      setCurrentID(body.device_id);
      setError("");
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        // cookie 没了：这台设备已经不是任何人的了。
        onSignedOutRef.current();
        return;
      }
      setError("读不到设备列表，稍后再试");
      setDevices([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const signOut = async (device: Device) => {
    if (busy) return;
    setBusy(true);
    try {
      await signOutDevice(device.id);
      toast("那台设备已经退出了");
      await load();
    } catch (err) {
      setError(err instanceof ApiError && err.status === 404 ? "那台设备已经不在了" : "没退成功，请再试一次");
      await load();
    } finally {
      setBusy(false);
    }
  };

  const signOutOthers = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const n = await signOutOtherDevices();
      toast(n > 0 ? `退掉了 ${n} 台设备` : "只有这一台，没有别的设备");
      await load();
    } catch {
      setError("没退成功，请再试一次");
      await load();
    } finally {
      setBusy(false);
    }
  };

  const others = (devices ?? []).filter((d) => d.id !== currentID);

  return (
    <div class="body">
      <div class="nav">
        <button class="ico" onClick={onBack} aria-label="返回">
          ‹
        </button>
        <div>登录的设备</div>
        <span />
      </div>

      {me && <div class="small-title">这些设备都是「{me.nickname}」</div>}

      {devices === null && <p class="empty-state">正在看…</p>}
      {error && <p class="error">{error}</p>}

      <div class="list">
        {(devices ?? []).map((d) => (
          <div class="li" key={d.id}>
            <span class="dev-ico" aria-hidden="true">
              {isHomeScreen(d) ? "📱" : isWeChat(d) ? "💬" : "🧭"}
            </span>
            <div class="tx">
              {deviceName(d)}
              <small>
                {lastSeenLabel(d.last_seen)}
                {d.id === currentID ? "" : ""}
              </small>
            </div>
            {d.id === currentID ? (
              <span class="here">本机</span>
            ) : (
              <button class="textlink" disabled={busy} onClick={() => void signOut(d)}>
                退出
              </button>
            )}
          </div>
        ))}
      </div>

      {devices !== null && others.length > 0 && (
        <div class="pad">
          <button class="btn danger-ghost" disabled={busy} onClick={() => void signOutOthers()}>
            退出其他设备
          </button>
        </div>
      )}

      <p class="disclaimer">
        不认识的设备？退掉之后它需要重新填称呼，看不到你的局。
      </p>
    </div>
  );
}

/** 从 UA 里猜一个图标和名字。猜错了也无所谓，用户看的是「是不是我的」。 */
function isWeChat(d: Device): boolean {
  return /micromessenger/i.test(d.user_agent ?? "");
}

function isHomeScreen(d: Device): boolean {
  const ua = d.user_agent ?? "";
  return /iphone|ipad|android/i.test(ua) && !/micromessenger/i.test(ua);
}

function deviceName(d: Device): string {
  const ua = d.user_agent ?? "";
  if (isWeChat(d)) return "微信内置浏览器";
  if (/iphone/i.test(ua)) return "iPhone · Safari";
  if (/ipad/i.test(ua)) return "iPad · Safari";
  if (/android/i.test(ua)) return /wv\)/i.test(ua) ? "微信内置浏览器（安卓）" : "Android · 浏览器";
  if (/macintosh/i.test(ua)) return "Mac · Safari";
  return "浏览器";
}

/** 「刚刚」/「2 小时前」/「昨天 21:14」。 */
function lastSeenLabel(unixSeconds: number): string {
  const then = new Date(unixSeconds * 1000);
  const minutes = Math.floor((Date.now() - then.getTime()) / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days === 1) return "昨天";
  if (days < 7) return `${days} 天前`;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${then.getMonth() + 1}/${then.getDate()} ${pad(then.getHours())}:${pad(then.getMinutes())}`;
}
