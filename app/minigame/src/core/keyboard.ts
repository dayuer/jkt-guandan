// 键盘会话：把「点字段 → 拉键盘 → 换字段 → 收起」这一串**事件处理**从 App 里拆出来。
//
// 为什么值得单独一个模块：切字段时如果两项设置不一样（开一局页从「地点」40 字单行切到
// 「备注」200 字多行），必须先收起旧键盘、回**按新设置**重新拉起。而「收起」会来两个事件
// ——`onKeyboardComplete`、`onKeyboardHeightChange` 高度归零 —— **顺序不定**，还可能一个都不来。
//
// 原来 `edit()` 是「先把 editing 换成新字段，再去收起」，于是旧键盘的收尾事件会：
//   · complete 先到：旧字段（地点）的文字被写进**新字段**（备注）；
//   · 高度归零先到：先按新设置把备注拉起来，之后迟到的 complete 又清掉 editing / kbOpen，
//     还把地点的文字写进备注 —— 用户接着在备注里打的字**全丢**。
// 这几条只有把事件顺序摆出来才看得见，所以这里只依赖一个很小的 wx 接口，
// 单测里造假 wx 就能把顺序都试一遍（#209 复审要求）。

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
 *   reshow 换字段、键盘开着、**设置不一样**（或没有 updateKeyboard）：先收起再按新设置拉起
 *   show   键盘没开：正常拉起来
 */
export function keyboardAction(
  st: { open: boolean; sameKey: boolean; settingsMatch: boolean },
  hasUpdateKeyboard: boolean,
): "none" | "update" | "reshow" | "show" {
  if (st.open && st.sameKey) return "none";
  // ⚠️ `wx.updateKeyboard` **只换文字**，`maxLength` / `multiple` / `confirmType` 还是上一个
  // 字段的。开一局页从「地点」（40 字、单行、下一项）切到「备注」（200 字、多行）时，
  // 备注会被限成 40 字单行 —— 所以三项必须**完全一样**才敢走 update。
  if (st.open && hasUpdateKeyboard && st.settingsMatch) return "update";
  if (st.open) return "reshow";
  return "show";
}

/**
 * 键盘现在是不是真开着。
 *
 * 光信自己记的 `kbOpen` 会卡住：切后台、安卓返回键、或者基础库收起键盘却没触发
 * `onKeyboardComplete` 时它一直是 true，之后就**再也拉不起键盘**、还没有任何提示。
 * 所以必须「自己记的那个」和「系统给的高度」都说开着才算。
 *
 * 注：没有 `onKeyboardHeightChange` 的旧基础库上高度恒为 0，于是每次都走直接 `show` ——
 * 那种机器上会退回到「重复 show 报 already show」，但 `benignKeyboardFail` 兜住了，不会弹错。
 */
export function keyboardOpen(kbOpen: boolean, kbHeight: number): boolean {
  return kbOpen && kbHeight > 0;
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

/** `hideKeyboard` 之后等收尾事件的兜底时长。到点还没动静就直接拉新键盘。 */
export const HIDE_FALLBACK_MS = 500;

/**
 * 键盘会话。**`editing` 只在键盘真的换过去之后才改成新字段** —— 收起期间它一直是旧字段，
 * 这样迟到的收尾事件知道文字该写给谁。
 */
export class KeyboardSession {
  editing: EditOptions | null = null;

  private pending: EditOptions | null = null;
  private open = false;
  private height = 0;
  private seq = 0;
  private hideTimer: unknown = null;
  /**
   * 刚**换过字段**（收起旧键盘再拉新的）：在确认新键盘真起来之前，来一个 complete 也可能是
   * **旧键盘迟到的** —— 那时 `editing` 已经是新字段，照收就把旧字段的文字写进新字段（复审那条）。
   * 所以置位吞掉，等新键盘报出高度 > 0（或用户在新键盘上打了字）再解除。
   */
  private swallowNextComplete = false;
  private sawInput = false;

  constructor(private wx: KeyboardApi, private host: SessionHost) {
    wx.onKeyboardInput((r: any) => {
      this.sawInput = true;
      if (this.editing) { this.editing.onInput(String(r?.value ?? "")); host.redraw() }
    });
    wx.onKeyboardConfirm((r: any) => {
      const ed = this.editing;
      if (ed) { const v = String(r?.value ?? ""); ed.onInput(v); ed.onConfirm?.(v) }
      // 收起本身交给 complete / 高度归零，这里不抢
    });
    wx.onKeyboardComplete((r: any) => {
      const value = String(r?.value ?? "");
      if (this.pending) {
        // 收旧键盘的过程中：文字交给**旧字段**（`editing` 还是它），再把新字段拉起来。
        const old = this.editing;
        if (old) { old.onInput(value); old.onDone?.(value) }
        this.finishHide();
        return;
      }
      if (this.swallowNextComplete && !this.sawInput) {
        // 换字段期间迟到的 complete（旧键盘的）：不认，也**不能 close** —— 新键盘正开着
        return;
      }
      const ed = this.editing;
      if (ed) { ed.onInput(value); ed.onDone?.(value) }
      this.close();
    });
    wx.onKeyboardHeightChange?.((r: any) => {
      this.height = r?.height || 0;
      // 新键盘起来了：之后收到的 complete 肯定是它的，不用再吞
      if (this.height > 0) this.swallowNextComplete = false;
      if (!this.height) {
        this.open = false;
        // 高度归零也是「确实收起了」。文字每一次输入就已经给过了，这里不用再给。
        if (this.pending) this.finishHide();
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
    const prev = this.editing;
    const action = keyboardAction(
      {
        open: keyboardOpen(this.open, this.height),
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
    if (action === "reshow") {
      // 收起期间 editing **保持旧字段**（见类注释）。
      this.pending = next;
      try {
        this.wx.hideKeyboard({
          // hide 失败（有些基础库/机型上会）就别干等：直接拉新的
          fail: () => { if (this.pending === next) this.finishHide() },
        });
      } catch { this.finishHide(); return }
      this.startHideTimer();
      this.host.redraw();
      return;
    }
    this.show(next);
  }

  /** 收起键盘（离开页面、关弹层）。 */
  blur() {
    if (!this.editing && !this.pending) return;
    this.pending = null;
    this.clearHideTimer();
    try { this.wx.hideKeyboard({}) } catch { /* ignore */ }
    this.close();
  }

  private show(o: EditOptions) {
    const my = ++this.seq;
    this.editing = o;
    this.sawInput = false;
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

  /** 确实收起了：把挂起的新字段拉起来。 */
  private finishHide() {
    const next = this.pending;
    this.pending = null;
    this.clearHideTimer();
    // 只要这次收起是为了换字段，旧键盘的 complete 就可能后到（超时兜底之外，高度归零也会
    // 抢在 complete 前面），一律先吞掉一个，别写进新字段。
    if (next || this.startedByTimeout) this.swallowNextComplete = true;
    this.startedByTimeout = false;
    this.editing = null;
    this.open = false;
    this.height = 0;
    if (next) this.show(next);
    else this.host.redraw();
  }

  private close() {
    this.clearHideTimer();
    this.swallowNextComplete = false;
    this.pending = null;
    this.editing = null;
    this.open = false;
    this.height = 0;
    this.host.redraw();
  }

  /** 切后台：一律作废（键盘可能已经不在了，但事件不会来）。 */
  private reset() {
    this.clearHideTimer();
    this.swallowNextComplete = false;
    this.pending = null;
    this.editing = null;
    this.open = false;
    this.height = 0;
    this.host.redraw();
  }

  private startedByTimeout = false;

  private startHideTimer() {
    this.clearHideTimer();
    if (!this.pending) return; // hideKeyboard 同步失败时 pending 已经消费掉了，别留空定时器
    this.hideTimer = this.host.setTimeout(() => {
      this.hideTimer = null;
      if (!this.pending) return;
      // hideKeyboard 没生效（既不归零也收不到 complete）时的兜底：直接拉新的。
      this.startedByTimeout = true;
      this.finishHide();
    }, HIDE_FALLBACK_MS);
  }

  private clearHideTimer() {
    if (this.hideTimer !== null && this.hideTimer !== undefined) {
      this.host.clearTimeout(this.hideTimer);
      this.hideTimer = null;
    }
  }
}
