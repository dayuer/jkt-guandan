import { useCallback, useEffect, useState } from "preact/hooks";
import { ApiError, getGroupInfo, listFriends, setStar, type Friend, type GroupInfo } from "../api";
import { InviteSheet } from "../components/InviteSheet";
import { initial } from "../components/Table";
import { copyText } from "../share";

interface Props {
  toast: (text: string) => void;
  /** 空状态里的「去找一局」。 */
  onGames: () => void;
  /** F2「生成海报」：直接打开开局表单，开好后的分享面板里就有海报。 */
  onNewGame: () => void;
}

/** 星标的排前面，再按一起打过的局数、最近一次——和服务端的排序一致，星标切换后本地重排用。 */
function sorted(list: Friend[]): Friend[] {
  return [...list].sort(
    (a, b) =>
      Number(b.starred) - Number(a.starred) ||
      b.together_count - a.together_count ||
      b.last_together_at - a.last_together_at,
  );
}

/**
 * 09 · 牌友（设计稿 F1，#108）。
 *
 * 只列出和你一起坐过同一局的人——服务端没有搜用户的接口，这是有意的（隐私）。
 * 顶部的「雅加达掼蛋群」横幅由服务端配置决定，没配就不显示。底部 Tab 由 App 统一挂（#107）。
 */
export function Friends({ toast, onGames, onNewGame }: Props) {
  const [friends, setFriends] = useState<Friend[] | null>(null);
  const [config, setConfig] = useState<GroupInfo | null>(null);
  const [error, setError] = useState("");
  const [inviting, setInviting] = useState<Friend | null>(null);

  const load = useCallback(async () => {
    try {
      setFriends(sorted(await listFriends()));
      setError("");
    } catch (err) {
      // 还没有身份（401）就是还没和谁打过局，不是错误。
      if (err instanceof ApiError && err.status === 401) setFriends([]);
      else {
        setFriends([]);
        setError("牌友没拉到，稍后再试");
      }
    }
  }, []);

  useEffect(() => {
    void load();
    getGroupInfo()
      .then(setConfig)
      .catch(() => setConfig(null));
  }, [load]);

  const toggleStar = async (f: Friend) => {
    const next = !f.starred;
    const apply = (starred: boolean) =>
      setFriends((list) => (list ? sorted(list.map((x) => (x.user.id === f.user.id ? { ...x, starred } : x))) : list));
    // 先改界面，失败再改回来：星标是很轻的操作，不该让人等。
    apply(next);
    try {
      await setStar(f.user.id, next);
    } catch {
      apply(!next);
      toast("没标上，稍后再试");
    }
  };

  const copyWeChat = async (id: string) => {
    if (await copyText(id)) toast(`微信号 ${id} 已复制，加群主拉你进群`);
    else toast(`微信号：${id}`);
  };

  const hasGroup = !!config && (!!config.wechat_id || !!config.whatsapp_group_url);

  return (
    <>
      <div class="body has-bar">
        <div class="nav">
          <span />
          <div>牌友</div>
          <span />
        </div>

        {hasGroup && config && (
          <div class="group-banner" data-testid="group-banner">
            <span class="gico" aria-hidden="true">
              <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round">
                <path d="M5 19l1.1-3.3A7 7 0 1 1 9 18.5z" />
              </svg>
            </span>
            <div class="gtx">
              <b>雅加达掼蛋群</b>
              <small>新局都在群里发，加群主拉你进群</small>
            </div>
            <div class="gact">
              {config.wechat_id && (
                <button class="pill-btn" onClick={() => void copyWeChat(config.wechat_id)}>
                  复制微信号
                </button>
              )}
              {config.whatsapp_group_url && (
                <a class="pill-btn wa" href={config.whatsapp_group_url} target="_blank" rel="noopener noreferrer">
                  WhatsApp 群
                </a>
              )}
            </div>
          </div>
        )}

        {error && <p class="error">{error}</p>}
        {friends === null && <p class="empty-state">正在找牌友…</p>}
        {friends !== null && friends.length === 0 && !error && (
          <p class="empty-state" data-testid="friends-empty">
            还没和谁一起打过局。
            <br />
            报一局、坐一桌，打过的人会出现在这里。
            <br />
            <button class="link" style={{ marginTop: 12 }} onClick={onGames}>
              去找一局 ›
            </button>
          </p>
        )}

        {friends && friends.length > 0 && (
          <div class="friends">
            {friends.map((f) => (
              <div class="friend" key={f.user.id} data-testid="friend">
                <span class={`avatar${f.starred ? " starred" : ""}`} aria-hidden="true">
                  {initial(f.user.nickname)}
                </span>
                <div class="tx">
                  <b>{f.user.nickname}</b>
                  <small>一起打过 {f.together_count} 局</small>
                </div>
                <button
                  class={`star${f.starred ? " on" : ""}`}
                  aria-pressed={f.starred}
                  aria-label={f.starred ? `取消星标 ${f.user.nickname}` : `星标 ${f.user.nickname}`}
                  onClick={() => void toggleStar(f)}
                >
                  {f.starred ? "★" : "☆"}
                </button>
                <button class="btn primary small" onClick={() => setInviting(f)}>
                  约 TA
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {inviting && (
        <InviteSheet
          friend={inviting}
          toast={toast}
          onPoster={() => {
            setInviting(null);
            onNewGame();
          }}
          onClose={() => setInviting(null)}
        />
      )}
    </>
  );
}
