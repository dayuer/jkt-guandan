import { useCallback, useEffect, useRef } from "preact/hooks";

/** toast 和底栏之间留的空隙。 */
export const TOAST_GAP = 20;

/** 页面自己的底栏（操作栏 / 蛋蛋输入框）：占 `--bar-height`，并且决定 toast 抬多高。 */
export function useBarHeight<T extends HTMLElement = HTMLElement>() {
  return useMeasured<T>("--bar-height", true);
}

/**
 * Tab 栏：只报自己的高度（`--tabbar-height`）。
 *
 * 它不碰 toast 的抬升——蛋蛋那一页同时有输入框和 Tab 栏，toast 要同时让开两条；
 * 让 toast 自己把 `--toast-lift` 和 `--tabbar-height` 加起来更清楚。
 */
export function useTabBarHeight<T extends HTMLElement = HTMLElement>() {
  return useMeasured<T>("--tabbar-height", false);
}

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
 * 顺手把「底栏高度 + 20px」写到 `--toast-lift` 上：toast 是 position: fixed 的，
 * 它不在 .body 的子树里，读不到页面局部的 --bar-height，只能从 <html> 上读。
 * 写死一个值的话，局头那种两行操作栏下面 toast 会盖住上面一行（#94 follow-up）。
 *
 * 返回的是**回调 ref**，不是 `useRef` 的对象 ref。原因是详情页第一次渲染时还在
 * 「正在打开…」那一支，操作栏根本不在树上（对象 ref 的 `current` 会是 null，
 * 而 effect 只在挂载时跑一次，之后元素才出现——量不到）。
 */
function useMeasured<T extends HTMLElement>(varName: string, liftToast: boolean) {
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
        document.documentElement.style.setProperty(varName, `${height}px`);
        // 页面自己的底栏顺带决定 toast 抬多高；Tab 栏不参与（见 useTabBarHeight）。
        if (liftToast) {
          document.documentElement.style.setProperty("--toast-lift", `${height + TOAST_GAP}px`);
        }
      }
    };

    apply();

    // 卸载时把 toast 的抬升还给兜底值：这个页面已经没有底栏了（新页面的底栏
    // 紧接着会自己写一次，所以不会互相打架）。
    const release = () => {
      document.documentElement.style.removeProperty(varName);
      if (liftToast) document.documentElement.style.removeProperty("--toast-lift");
    };

    if (typeof ResizeObserver === "undefined") {
      // 老浏览器：没有 observer，至少跟着窗口变化重量一次。
      window.addEventListener("resize", apply);
      stop.current = () => {
        window.removeEventListener("resize", apply);
        release();
      };
      return;
    }

    const observer = new ResizeObserver(apply);
    observer.observe(el);
    stop.current = () => {
      observer.disconnect();
      release();
    };
  }, []);

  useEffect(() => () => stop.current(), []);

  return attach;
}
