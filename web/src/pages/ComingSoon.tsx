interface Props {
  title: string;
  /** 一句话说明这里将来是什么，别让用户以为坏了。 */
  note: string;
}

/**
 * tab 级页面的占位（#107）。
 *
 * 学牌（#106）和牌友（#108）还没做，但底部 Tab 栏已经是全站的了——点进来必须有东西，
 * 不能空白，也不能出现「点不动」的 tab。这两个页面落地时把这一页替换掉即可。
 */
export function ComingSoon({ title, note }: Props) {
  return (
    <div class="body has-bar">
      <div class="head">
        <h1>{title}</h1>
      </div>
      <p class="empty-state">
        {note}
        <br />
        还在做，先用「牌局」约起来。
      </p>
      <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
    </div>
  );
}
