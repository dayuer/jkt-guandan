// 实时（§5.5）：直接用共享层的 RealtimeClient（退避、降级轮询、按 v 去重都在里面），
// 只把 WebSocket 换成 wx.connectSocket，鉴权用 Bearer 头（#84）。前后台用 wx.onShow / onHide。
// #253 起不再从 app/h5 借（那会让小游戏反向依赖 H5），改从 app/shared 拿。

import { RealtimeClient, type RealtimeEvent, type RealtimeStatus, type SocketLike } from "../../../shared/src/realtime";
import { API_BASE, getToken } from "./api";

export type { RealtimeEvent, RealtimeStatus };

const wsURL = API_BASE.replace(/^http/, "ws") + "/ws";

function createSocket(url: string): SocketLike {
  const header: Record<string, string> = {};
  const t = getToken();
  if (t) header["Authorization"] = `Bearer ${t}`;
  const task = wx.connectSocket({ url, header, timeout: 10000 });
  const sock: SocketLike = {
    readyState: 0,
    send: (data: string) => task.send({ data }),
    close: (code?: number, reason?: string) => task.close({ code: code ?? 1000, reason: reason ?? "" }),
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
  };
  task.onOpen(() => {
    sock.readyState = 1;
    sock.onopen?.({});
  });
  task.onMessage((m: any) => sock.onmessage?.({ data: m.data }));
  task.onError((e: any) => sock.onerror?.(e));
  task.onClose((e: any) => {
    sock.readyState = 3;
    sock.onclose?.({ code: e?.code ?? 1006 });
  });
  return sock;
}

let visible = true;
const visListeners = new Set<() => void>();
wx.onShow(() => {
  visible = true;
  visListeners.forEach((f) => f());
});
wx.onHide(() => {
  visible = false;
  visListeners.forEach((f) => f());
});

export const realtime = new RealtimeClient({
  url: wsURL,
  deps: {
    createSocket,
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (id) => clearTimeout(id as any),
    setInterval: (fn, ms) => setInterval(fn, ms),
    clearInterval: (id) => clearInterval(id as any),
    random: Math.random,
    isVisible: () => visible,
    onVisibilityChange: (fn) => {
      visListeners.add(fn);
      return () => visListeners.delete(fn);
    },
  },
});

/** 身份变了（登录、退出）要换一条带新凭证的连接。 */
export function reconnectRealtime() {
  realtime.stop();
  realtime.start();
}
