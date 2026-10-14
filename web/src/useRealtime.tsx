import { useEffect, useRef, useState } from "preact/hooks";
import { realtime, type RealtimeEvent, type RealtimeStatus } from "./realtime";

/**
 * 把页面接上全站共用的那条 WebSocket（#49、§5.5）。
 *
 * 约定（见 realtime.ts 的注释）：**REST 是事实来源**，实时消息只说「哪里变了」，
 * 页面收到后重新拉一次自己的 REST，不靠消息本身拼状态。所以这里的 onEvent 通常
 * 就是一次 refresh。
 *
 * 回调放在 ref 里：页面每次渲染传进来的都是新函数，如果把它当依赖，就会不停地
 * 注销再登记，主题一进一出之间会漏掉消息。真正需要重新登记的只有主题本身。
 */
export function useRealtime(options: {
  topics: string[];
  /** 这个主题上有消息时做什么。已经按 v 去重过（同一局不比见过的更旧）。 */
  onEvent?: (ev: RealtimeEvent) => void;
  /** 重新拉一次本页的 REST：重连成功、回到前台、以及降级轮询时调用。 */
  refresh?: () => void;
  /** 连不上时是否参与轮询降级（02 和 03 需要）。 */
  poll?: boolean;
}): void {
  const { topics, onEvent, refresh, poll } = options;
  const latest = useRef({ onEvent, refresh });
  latest.current = { onEvent, refresh };

  // 数组每轮渲染都是新对象；按内容当依赖，内容没变就不重新登记。
  const key = topics.join("\n");
  useEffect(() => {
    const list = key === "" ? [] : key.split("\n");
    if (list.length === 0) return;
    return realtime().register({
      topics: list,
      poll,
      onEvent: (ev) => latest.current.onEvent?.(ev),
      refresh: () => latest.current.refresh?.(),
    });
  }, [key, poll]);
}

/** 当前实时连接的状态。降级成轮询时是 "offline"。 */
export function useRealtimeStatus(): RealtimeStatus {
  const [status, setStatus] = useState<RealtimeStatus>(() => realtime().status);
  useEffect(() => realtime().onStatus(setStatus), []);
  return status;
}

/**
 * 「离线 · 15 秒刷新」小点（原型 R2）。连上时不渲染任何东西。
 *
 * 说明：断线后页面仍在按 15 秒的节奏拉 REST，用户看到的列表不会停在旧数据上，
 * 这个点只是把「现在不是实时的」这件事讲出来。
 */
export function OfflineDot() {
  const status = useRealtimeStatus();
  if (status !== "offline") return null;
  return (
    <span class="net">
      <i />
      离线 · 15 秒刷新
    </span>
  );
}
