import { useTabBarHeight } from "../useBarHeight";

/**
 * 底部 Tab 栏（设计稿 A2）。
 *
 * 放在 App 层渲染，而不是各个页面里：#107 里 `/me` 忘了带它，iOS 主屏幕（独立窗口、
 * 没有浏览器返回键）上点进「我的」就出不来了。凡是 tab 级页面（`/`、`/me`，
 * 以及将来的 `/learn`、`/friends`）都由 App 统一挂这一条；详情页、开局表单这类
 * 非 tab 页不挂。
 */
export type TabKey = "games" | "learn" | "create" | "friends" | "me";

const TABS: { key: TabKey; label: string; glyph: string; ball?: boolean }[] = [
  { key: "games", label: "牌局", glyph: "♠" },
  { key: "learn", label: "学牌", glyph: "♣" },
  // 中间这一颗红球：球里是「雅」（雅加达），#218 之前写的是「蛋」。
  { key: "create", label: "约局", glyph: "雅", ball: true },
  { key: "friends", label: "牌友", glyph: "♦" },
  { key: "me", label: "我的", glyph: "♥" },
];

interface Props {
  /** 当前在哪个 tab 上，高亮它。 */
  active: TabKey;
  onTab: (tab: TabKey) => void;
}

export function TabBar({ active, onTab }: Props) {
  // 报自己的高度（`--tabbar-height`）：页面据此留白，toast 也据此让开。
  const bar = useTabBarHeight<HTMLElement>();

  return (
    <nav class="tabbar" ref={bar}>
      {TABS.map((t) => (
        <button
          key={t.key}
          class={`tab${t.key === active ? " on" : ""}${t.ball ? " ball" : ""}`}
          aria-current={t.key === active ? "page" : undefined}
          onClick={() => onTab(t.key)}
        >
          <span class="g" aria-hidden="true">
            {t.glyph}
          </span>
          {t.label}
        </button>
      ))}
    </nav>
  );
}
