import { useEffect, useRef } from "preact/hooks";

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
 */
export function useBarHeight<T extends HTMLElement = HTMLElement>() {
  const ref = useRef<T>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const apply = () => {
      const height = Math.round(el.getBoundingClientRect().height);
      if (height > 0) {
        // 写在 <html> 上：.body 的 padding-bottom 会顺着继承链读到这个值。
        document.documentElement.style.setProperty("--bar-height", `${height}px`);
      }
    };

    apply();

    // 老浏览器没有 ResizeObserver：至少按首屏量一次，别什么都不做。
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", apply);
      return () => window.removeEventListener("resize", apply);
    }

    const observer = new ResizeObserver(apply);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  return ref;
}
