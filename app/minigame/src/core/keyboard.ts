// 键盘会话：把「点字段 → 拉键盘 → 换字段 → 收起」这一串**事件处理**从 App 里拆出来。
//
// 为什么值得单独一个模块：切字段时如果两项设置不一样（开一局页从「地点」40 字单行切到
// 「备注」200 字多行），必须先收起旧键盘、再**按新设置**重新拉起。而「收起」会来两个事件
// ——`onKeyboardComplete`、`onKeyboardHeightChange` 高度归零 —— **顺序不定**，还可能一个都不来。
//
// 第四轮（#209）改成**排队**：一个会话的收尾没落地之前，不开下一个。
//   · 点新字段时如果上一个还在收尾（我们让它收起 / 系统已经收起（高度归零）/ 主动 hide 过），
//     新字段排在队尾；
//   · 收尾落地有四种方式：收到 complete、高度归零、HIDE_FALLBACK_MS 兜底超时、hide 失败回调；
//   · 排队期间 `editing` **一直是旧字段**，所以迟到的 complete 知道文字该写给谁（不串字段）；
//   · 靠 complete 落地的这一家后面不会再有迟到事件；靠另外三种落地的，之后第一个
//     **没打过字**的 complete 按迟到处理（丢掉）。
//
// 为什么宁可等：真机上收起和拉起之间会有认不出归属的 complete，不等它就只能猜 ——
// 第三轮就是猜错的（迟到的 complete 把旧字段的文字写进了新字段）。慢几十毫秒换正确。

export interface EditOptions {
  key: string;
  value: string;
  maxLength?: number;
  multiple?: boolean;
  confirmType?: "done" | "next" | "search" | "go" | "send";
  onInput: (v: string) => void;
  onConfirm?: (v: string) => void;
  onDone?: (v: string) => void;
  placeholder?: string;
}

/** 会话用得到的 wx 接口。真机传 `wx`，单测传假的。 */
export interface KeyboardApi {
  showKeyboard(o: any): void;
  hideKeyboard(o?: any): void;
  updateKeyboard?(o: { value: string }): void;
  onKeyboardInput(fn: (r: any) => void): void;
  onKeyboardConfirm(fn: (r: any) => void): void;
  onKeyboardComplete(fn: (r: any) => void): void;
  onKeyboardHeightChange?(fn: (r: any) => void): void;
  onHide?(fn: () => void): void;
}

export interface SessionHost {
  redraw(): void;
  /** 真的拉不起来时才提示（「已经开着」这种不提示）。 */
  toast(msg: string): void;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(id: unknown): void;
}

/**
 * 点输入框时该怎么动键盘。
 *
 * `wx.showKeyboard` 在**键盘已经开着**的时候再调会失败（真机报 `showKeyboard:fail`）。
 * 抽成纯函数是为了**能测**：真机上 showKeyboard 的成败没法在单测里造。
 *   none   同一个字段再点：键盘本来就在，什么都不用做
 *   update 换字段、键盘开着、设置一样：只换内容（`wx.updateKeyboard`）
 *   queue  上一个会话还在收尾（`closing`），或者键盘开着但设置不一样：先让前一个收尾，
 *          新字段排队，收起了再按它自己的设置拉起来
 *   show   键盘没开、也没有欠账：正常拉起来
 */
export function keyboardAction(
  st: { open: boolean; closing: boolean; sameKey: boolean; settingsMatch: boolean },
  hasUpdateKeyboard: boolean,
): "none" | "update" | "queue" | "show" {
  // 上一条的收尾还没落地：哪怕点的是同一个字段也不抢（它的 complete 可能还在路上）。
  if (st.closing) return "queue";
  if (st.open && st.sameKey) return "none";
  // ⚠️ `wx.updateKeyboard` **只换文字**，`maxLength` / `multiple` / `confirmType` 还是上一个
  // 字段的。开一局页从「地点」（40 字、单行、下一项）切到「备注」（200 字、多行）时，
  // 备注会被限成 40 字单行 —— 所以三项必须**完全一样**才敢走 update。
  if (st.open && hasUpdateKeyboard && st.settingsMatch) return "update";
  if (st.open) return "queue";
  return "show";
}

/**
 * 键盘现在是不是真开着。
 *
 * 光信自己记的 `open` 会卡住：切后台、安卓返回键、或者基础库收起键盘却没触发
 * `onKeyboardComplete` 时它一直是 true，之后就**再也拉不起键盘**、还没有任何提示。
 * 所以必须「自己记的那个」和「系统给的高度」都说开着才算。
 *
 * 注：没有 `onKeyboardHeightChange` 的旧基础库上高度恒为 0，于是每次都走直接 `show` ——
 * 那种机器上会退回到「重复 show 报 already show」，但 `benignKeyboardFail` 兜住了，不会弹错。
 */
export function keyboardOpen(open: boolean, height: number): boolean {
  return open && height > 0;
}

/** 两次编辑的键盘设置是不是一样（`updateKeyboard` 换不了这些，只能重新拉）。 */
export function keyboardSettingsMatch(a: EditOptions, b: EditOptions): boolean {
  return (a.maxLength ?? 40) === (b.maxLength ?? 40)
    && !!a.multiple === !!b.multiple
    && (a.confirmType || "done") === (b.confirmType || "done");
}

/** `showKeyboard` 的失败信息里，哪些是「键盘本来开着」这种**不是错误**的情况。 */
export function benignKeyboardFail(errMsg: string): boolean {
  return /already|已经/i.test(errMsg || "");
}

/**
 * 收起之后等收尾事件的兜底时长。到点还没动静就当它已经收起了：有排队的字段就开它，
 * 没有就把会话收干净。
 *
 * 500ms 是「人感觉不到、又比真机收起的动画长」的量级：等得到 complete 就不用等它，
 * 等不到也不至于让用户点了字段半天没反应。
 */
export const HIDE_FALLBACK_MS = 500;

/**
 * 键盘会话。**`editing` 只在键盘真的换过去之后才改成新字段** —— 排队期间它一直是旧字段，
 * 这样迟到的收尾事件知道文字该写给谁。
 */
export class KeyboardSession {
  editing: EditOptions | null = null;

  /** 队尾：等上一个会话收尾的字段。只留最后一个 —— 用户连点几个，只有最后那个是要看的。 */
  private waiting: EditOptions | null = null;
  /** 自己记的「键盘拉着」。要配合 `height` 一起看（见 keyboardOpen）。 */
  private open = false;
  private height = 0;
  /** 已经让它收起（我们 hide 了、或者系统报高度归零），但它的 complete 还没到。 */
  private closing = false;
  /**
   * 上一家是**兜底 / 高度归零 / hide 失败**放走的（不是收到 complete 放走的）：之后第一个
   * 「没打过字」的 complete 就是它迟到的，丢掉。
   *
   * 真机上认不出那个 complete 属于谁，而写错字段比丢一次 onDone 严重得多。用户在新键盘上
   * 打过字（`sawInput`）就解除 —— 那时候来的 complete 肯定是新键盘的。
   */
  private staleInFlight = false;
  private sawInput = false;

  private seq = 0;
  private timer: unknown = null;

  constructor(private wx: KeyboardApi, private host: SessionHost) {
    wx.onKeyboardInput((r: any) => {
      this.sawInput = true;
      this.staleInFlight = false;
      if (this.editing) { this.editing.onInput(String(r?.value ?? "")); host.redraw() }
    });
    wx.onKeyboardConfirm((r: any) => {
      const ed = this.editing;
      if (ed) { const v = String(r?.value ?? ""); ed.onInput(v); ed.onConfirm?.(v) }
      // 收起本身交给 complete / 高度归零，这里不抢
    });
    wx.onKeyboardComplete((r: any) => {
      const value = String(r?.value ?? "");
      if (this.closing) {
        // 收尾到了：文字交给**当时的字段**（排队期间 `editing` 还是旧字段），
        // 然后才把排队的新字段拉起来。这里不能 close —— 新字段正要开。
        const ed = this.editing;
        if (ed) { ed.onInput(value); ed.onDone?.(value) }
        this.finishClose(true);
        return;
      }
      if (this.staleInFlight && !this.sawInput) {
        // 上一家是兜底放走的，这是它迟到的 complete：不认，也不 close（现在开的是新字段）
        this.staleInFlight = false;
        return;
      }
      const ed = this.editing;
      if (ed) { ed.onInput(value); ed.onDone?.(value) }
      this.close();
    });
    wx.onKeyboardHeightChange?.((r: any) => {
      this.height = r?.height || 0;
      if (!this.height) {
        this.open = false;
        // 高度归零 = 键盘确实收起了。文字每次输入就已经给过，这里不用再给。
        // 但它的 complete 可能还在路上：先记成「正在收尾」，这样这之后点别的字段会**排队**
        // 等它（复审的 P1：高度先归零、马上点另一个字段、旧 complete 才到）。
        // 也不清 staleInFlight —— 兜底放走过的话，那个迟到的 complete 还欠着。
        this.closing = true;
        if (this.waiting) this.finishClose(false);
        else this.armTimer(); // 没有排队的字段：兜底把会话收干净，免得一直停在「编辑中」
      }
      host.redraw();
    });
    wx.onHide?.(() => this.reset());
  }

  /** 键盘高度（`onKeyboardHeightChange` 给的），0 表示没键盘。给外面算可视区用。 */
  get kbHeight(): number { return this.height }

  isEditing(key: string): boolean { return !!this.editing && this.editing.key === key }

  /** 点了一个字段（或「下一项」）。 */
  focus(next: EditOptions) {
    // 队里已经有人在等：换成最后点的那个（用户改了主意，中间那个不用真的开一遍）。
    if (this.waiting) { this.waiting = next; this.host.redraw(); return }

    const prev = this.editing;
    const open = keyboardOpen(this.open, this.height);
    const action = keyboardAction(
      {
        open,
        closing: this.closing,
        sameKey: !!prev && prev.key === next.key,
        settingsMatch: !!prev && keyboardSettingsMatch(prev, next),
      },
      !!this.wx.updateKeyboard,
    );
    if (action === "none") { this.host.redraw(); return }
    if (action === "update") {
      this.editing = next;
      try { this.wx.updateKeyboard!({ value: next.value || "" }) } catch { /* 换不了也还能用 */ }
      this.host.redraw();
      return;
    }
    if (action === "queue") {
      // 排队期间 `editing` **保持旧字段**（见类注释）。
      this.waiting = next;
      if (open) {
        try {
          this.wx.hideKeyboard({
            // hide 失败（有些基础库/机型上会）就别干等：当成已经收起了，直接开新字段。
            // 它迟到的 complete 由 staleInFlight 兜住。
            fail: () => { if (this.waiting === next) this.finishClose(false) },
          });
        } catch { this.finishClose(false); return }
        // ⚠️ fail 回调是**同步**调的：上面这次 hideKeyboard 可能已经收尾并开了新字段，
        // 那就不能再把 closing 置回去（置回去的话，迟到的 complete 会被当成新字段的）。
        if (this.waiting !== next) { this.host.redraw(); return }
      }
      this.closing = true;
      this.armTimer();
      this.host.redraw();
      return;
    }
    this.show(next);
  }

  /** 收起键盘并作废会话状态（离开页面、关弹层）。**别直接调 wx.hideKeyboard** —— 那样会话不知道。 */
  blur() {
    if (!this.editing && !this.waiting && !this.closing) return;
    this.waiting = null;
    this.editing = null;
    this.open = false;
    this.height = 0;
    try { this.wx.hideKeyboard({}) } catch { /* ignore */ }
    // 收起请求已经发出去了，它的 complete 可能后到：这之后点别的字段要排队（复审的 P2）。
    this.closing = true;
    this.staleInFlight = false;
    this.armTimer();
    this.host.redraw();
  }

  private show(o: EditOptions) {
    const my = ++this.seq;
    this.editing = o;
    this.sawInput = false;
    this.open = false;
    try {
      this.wx.showKeyboard({
        defaultValue: o.value || "",
        maxLength: o.maxLength || 40,
        multiple: !!o.multiple,
        confirmHold: false,
        confirmType: o.confirmType || "done",
        success: () => { if (my === this.seq) this.open = true },
        fail: (e: any) => {
          if (my !== this.seq) return; // 迟到的，不管
          const msg = String(e?.errMsg || "");
          if (benignKeyboardFail(msg)) { this.open = true; return }
          this.open = false;
          this.host.toast("键盘没拉起来：" + msg);
        },
      });
    } catch {
      this.open = false;
    }
    this.host.redraw();
  }

  /** 上一次收尾落地了。byComplete 为假表示是兜底/高度/失败放走的，后面还可能来个迟到的 complete。 */
  private finishClose(byComplete: boolean) {
    const next = this.waiting;
    this.waiting = null;
    this.clearTimer();
    this.closing = false;
    if (next && !byComplete) this.staleInFlight = true;
    this.editing = null;
    this.open = false;
    this.height = 0;
    if (next) this.show(next);
    else this.host.redraw();
  }

  private close() {
    this.clearTimer();
    this.waiting = null;
    this.closing = false;
    this.staleInFlight = false;
    this.editing = null;
    this.open = false;
    this.height = 0;
    this.host.redraw();
  }

  /** 切后台：一律作废（键盘可能已经不在了，但事件不会来）。 */
  private reset() {
    this.clearTimer();
    this.waiting = null;
    this.closing = false;
    this.staleInFlight = false;
    this.editing = null;
    this.open = false;
    this.height = 0;
    this.host.redraw();
  }

  /** 等收尾落地：到点还没动静就当成收起了。 */
  private armTimer() {
    this.clearTimer();
    this.timer = this.host.setTimeout(() => {
      this.timer = null;
      if (!this.closing && !this.waiting) return;
      this.finishClose(false);
    }, HIDE_FALLBACK_MS);
  }

  private clearTimer() {
    if (this.timer !== null && this.timer !== undefined) {
      this.host.clearTimeout(this.timer);
      this.timer = null;
    }
  }
}
