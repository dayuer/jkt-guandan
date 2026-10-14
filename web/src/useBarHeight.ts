import { useCallback, useEffect, useRef } from "preact/hooks";

/**
 * 量出底部固定条的真实高度，写到 `<html>` 的 `--bar-height` 上（#75）。
 *
 * 为什么不能写死：详情页的操作栏在窄屏上会**换行**。局头看到四颗按钮
 * （⇪ 分享 / 已报名·退出 / 改时间·地点 / 取消这局），375 宽下每行放两颗，
 * 于是变成两行——`.body.has-actionbar` 里写死的 90px 只够一行，下面 49px 的内容
 * 就被压在操作栏底下了。
 *
 * 所以：CSS 里那 90px 只是**首屏兜底**，挂上以后由这里量出来的真实高度接管。
 * 用 ResizeObserver 而不是只量一次：换行与否取决于宽度、字号和系统字体大小。
 *
 * 返回的是**回调 ref**，不是 `useRef` 的对象 ref。原因是详情页第一次渲染时还在
 * 「正在打开…」那一支，操作栏根本不在树上（对象 ref 的 `current` 会是 null，
 * 而 effect 只在挂载时跑一次，之后元素才出现——量不到）。
 */
export function useBarHeight<T extends HTMLElement = HTMLElement>() {
  const stop = useRef<() => void>(() => {});

  const attach = useCallback((el: T | null) => {
    // 元素换了（或卸载了）就把上一次的观察停掉，别让两个观察同时写这个变量。
    stop.current();
    stop.current = () => {};
    if (!el) return;

    const apply = () => {
      const height = Math.round(el.getBoundingClientRect().height);
      // 还没排版（0）时不动它，免得把 CSS 里的兜底值冲掉。
      if (height > 0) {
        document.documentElement.style.setProperty("--bar-height", `${height}px`);
      }
    };

    apply();

    if (typeof ResizeObserver === "undefined") {
      // 老浏览器：没有 observer，至少跟着窗口变化重量一次。
      window.addEventListener("resize", apply);
      stop.current = () => window.removeEventListener("resize", apply);
      return;
    }

    const observer = new ResizeObserver(apply);
    observer.observe(el);
    stop.current = () => observer.disconnect();
  }, []);

  useEffect(() => () => stop.current(), []);

  return attach;
}
