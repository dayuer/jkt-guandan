/**
 * 让安卓 WebView 里「点一下输入框就弹键盘」（P0）。
 *
 * 微信的 XWeb / X5、部分厂商自带浏览器里，如果输入框的焦点不是用户点出来的（程序化 focus、
 * 页面切换时遗留的焦点、弹层挂载时浏览器自己给的），它会处在「有焦点、没键盘」的状态，
 * 而且再点一下也不弹——浏览器认为它已经聚焦了，不再走「聚焦 → 弹键盘」这条路。
 *
 * 办法：按下时如果它已经有焦点、但这个焦点不是刚才的手势给的，就先 blur 掉，
 * 让这次点击的原生聚焦重新来一遍，键盘就会出来。
 * 用户自己点出来的焦点不动：不然每次点一下挪光标，键盘都会收起再弹出来。
 */

/** 用户按下之后多久之内拿到的焦点算「手势给的」。 */
const GESTURE_MS = 1000;

export interface TapFocusHandlers {
  onPointerDown: (e: Event) => void;
  onTouchStart: (e: Event) => void;
  onFocus: () => void;
  onBlur: () => void;
}

export function tapFocusHandlers(now: () => number = Date.now): TapFocusHandlers {
  let lastDown = -Infinity;
  let fromGesture = false;

  const down = (e: Event) => {
    const el = e.currentTarget as HTMLElement | null;
    if (el && document.activeElement === el && !fromGesture) el.blur();
    lastDown = now();
  };

  return {
    onPointerDown: down,
    // 老内核没有 pointer 事件时靠 touchstart；两个都来时第二次已经是「手势给的」，不会再 blur。
    onTouchStart: down,
    onFocus: () => {
      fromGesture = now() - lastDown < GESTURE_MS;
    },
    onBlur: () => {
      fromGesture = false;
    },
  };
}
