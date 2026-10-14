import { useEffect, useState } from "preact/hooks";
import { ApiError, listFriends, type Friend } from "../api";
import { initial } from "./Table";

/** 一次最多邀请 20 位（服务端的上限）。 */
export const MAX_INVITES = 20;

interface Props {
  /** 已经选上的（从 08 表单再次打开时带回来）。 */
  selected?: string[];
  /** 确认按钮上的字，比如「邀请 2 位」或「就这些」。 */
  confirmLabel: (n: number) => string;
  busy?: boolean;
  /** 08 表单里可以一个都不选（等于清空）；详情页里至少选一个。 */
  allowNone?: boolean;
  onConfirm: (picked: Friend[]) => void;
  onClose: () => void;
}

/**
 * 「邀请谁」（#115）：从牌友里多选。只列一起打过局的人（GET /api/friends），没有搜索。
 * 08 开局表单和详情页（局头视角）共用。
 */
export function FriendPicker({ selected = [], confirmLabel, busy, allowNone, onConfirm, onClose }: Props) {
  const [friends, setFriends] = useState<Friend[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(() => new Set(selected));

  useEffect(() => {
    listFriends()
      .then(setFriends)
      .catch((err) => {
        setFriends([]);
        if (!(err instanceof ApiError && err.status === 401)) setFailed(true);
      });
  }, []);

  const toggle = (id: string) =>
    setPicked((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else if (next.size < MAX_INVITES) next.add(id);
      return next;
    });

  const chosen = (friends ?? []).filter((f) => picked.has(f.user.id));

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet picker" role="dialog" aria-modal="true" aria-label="邀请谁" data-testid="friend-picker">
        <div class="grab" />
        <h3>邀请谁</h3>
        <p class="sub">只能邀请一起打过局的牌友；他们会收到一条通知</p>

        {friends === null && <p class="empty-state">正在找牌友…</p>}
        {failed && <p class="error">牌友没拉到，稍后再试</p>}
        {friends !== null && friends.length === 0 && !failed && (
          <p class="empty-state" data-testid="picker-empty">
            还没有牌友。一起打过局的人会出现在这里，也可以先把链接发到群里。
          </p>
        )}

        {friends && friends.length > 0 && (
          <div class="pick-list">
            {friends.map((f) => {
              const on = picked.has(f.user.id);
              return (
                <button
                  key={f.user.id}
                  class={`pick${on ? " on" : ""}`}
                  role="checkbox"
                  aria-checked={on}
                  onClick={() => toggle(f.user.id)}
                >
                  <span class="avatar" aria-hidden="true">
                    {initial(f.user.nickname)}
                  </span>
                  <span class="tx">
                    {f.user.nickname}
                    <small>一起打过 {f.together_count} 局</small>
                  </span>
                  <span class="check" aria-hidden="true">
                    {on ? "✓" : ""}
                  </span>
                </button>
              );
            })}
          </div>
        )}

        <div class="stack">
          <button class="btn primary" disabled={busy || (!allowNone && chosen.length === 0)} onClick={() => onConfirm(chosen)}>
            {confirmLabel(chosen.length)}
          </button>
          <button class="btn soft" onClick={onClose}>
            取消
          </button>
        </div>
      </div>
    </>
  );
}
