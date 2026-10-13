import { useEffect, useState } from "preact/hooks";
import { subscribeRetry } from "./api";

/**
 * 服务端过载时 api.ts 会自动重试一次；这个钩子让按钮能显示「人有点多，正在重试…」（原型 X4）。
 */
export function useRetrying(): boolean {
  const [retrying, setRetrying] = useState(false);
  useEffect(() => subscribeRetry(setRetrying), []);
  return retrying;
}
