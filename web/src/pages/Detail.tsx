import { useCallback, useEffect, useMemo, useState } from "preact/hooks";
import {
  ApiError,
  cancelGame,
  getGame,
  inviteError,
  inviteFriends,
  inviteSummary,
  joinGame,
  kickPlayer,
  leaveGame,
  updateGame,
  type GameDetail,
  type GamePatch,
  type Seat,
  type UserRef,
} from "../api";
import { CalendarSheet } from "../components/CalendarSheet";
import { FriendPicker } from "../components/FriendPicker";
import { JoinedSheet } from "../components/JoinedSheet";
import { PushGuide } from "../components/PushGuide";
import { ShareSheet } from "../components/ShareSheet";
import { NoticeBanner, PromotedSheet } from "../components/StatusNotices";
import { GameTable } from "../components/Table";
import { conflictingFields, describeConflict, type FieldDiff } from "../conflict";
import { dayLabel, kindLabel, timeHM } from "../format";
import { acknowledge, fromDetail, observe, type Notice, type NoticeKind } from "../status";
import { gameTitle } from "../title";
import { useBarHeight } from "../useBarHeight";
import { OfflineDot, useRealtime, useRealtimeStatus } from "../useRealtime";
import { useRetrying } from "../useRetrying";

interface Props {
  code: string;
  me: UserRef | null;
  /** 需要身份时弹称呼层，返回填好的身份；用户取消则返回 null。 */
  requireIdentity: () => Promise<UserRef | null>;
  toast: (text: string) => void;
  onBack: () => void;
}

/** K4：编辑期间这些字段被别人也改了，需要局头自己选。 */
interface ConflictState {
  patch: GamePatch;
  /** 打开编辑框那一刻的牌局——用来判断冲突是不是和本次修改有关。 */
  baseline: GameDetail;
  latest: GameDetail;
  diffs: FieldDiff[];
}

/**
 * 03 · 牌局详情（设计稿 B1 / B3，以及 W1 / W2 / R3 / K1 / K3 / K4 / K5 / K6）。
 *
 * 状态全部来自服务端：`me.role` 说明我在不在这一局里，`missing` 说明还差几个人，
 * `status` 说明这一局还能不能报。前端不自己推断这些。
 */
export function Detail({ code, me, requireIdentity, toast, onBack }: Props) {
  const [detail, setDetail] = useState<GameDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [conflict, setConflict] = useState<ConflictState | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [kickTarget, setKickTarget] = useState<Seat | null>(null);
  const [cancelOpen, setCancelOpen] = useState(false);
  // 编辑弹层里的就地错误（敏感词，§14）：弹层盖住了页面的错误行，得在弹层里面说。
  const [editError, setEditError] = useState<{ field: string; message: string } | null>(null);
  const [shareOpen, setShareOpen] = useState(false);
  /** 局头的「邀请牌友」（#115）。 */
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviting, setInviting] = useState(false);
  /** 这一局现在有几台设备在看（含自己，#46 / 原型 R1）。 */
  const [viewers, setViewers] = useState(0);
  // 连接断了（重连中或降级成轮询）就不再有 presence，人数会停在旧值：清零，胶囊跟着藏起来。
  // 重连后服务端会给新订阅发一次 presence，人数自己回来（#71 复审建议）。
  const realtimeStatus = useRealtimeStatus();
  useEffect(() => {
    if (realtimeStatus !== "online") setViewers(0);
  }, [realtimeStatus]);
  /** 「加入日历」弹层（#6）。 */
  const [calendar, setCalendar] = useState(false);
  /** 刚报上名：报名成功弹层，问要不要提醒（B4–B6，#10）。 */
  const [joinedOpen, setJoinedOpen] = useState(false);
  /** 「提醒我」在收不到推送的环境里点下去：跳出 / 加主屏幕引导屏（H1 / H2，#70）。 */
  const [guide, setGuide] = useState(false);
  // 改时间 / 补位 / 局头转交（#114）：和本机快照比出来的提示，关掉才算知道了。
  const [notices, setNotices] = useState<Notice[]>([]);
  const retrying = useRetrying();
  // 操作栏在窄屏上会换行（局头有四颗按钮），高度要量出来（#75）。
  const actionBar = useBarHeight<HTMLDivElement>();

  const load = useCallback(async () => {
    try {
      setDetail(await getGame(code));
      setError("");
    } catch (err) {
      setError(err instanceof ApiError && err.status === 404 ? "这一局不在了" : "打不开这一局，下拉重试");
    }
  }, [code]);

  useEffect(() => {
    void load();
  }, [load]);

  // 标题和服务端注入的 OG 标题一致（#60）；局头改了时间 / 地点会跟着变。
  useEffect(() => {
    if (detail) document.title = gameTitle(detail);
  }, [detail]);

  // 每拿到一次最新数据（包括实时事件触发的重新拉取）就和快照比一次（#114）。
  useEffect(() => {
    if (detail && detail.game.short_code === code) setNotices(observe(fromDetail(detail), me?.id));
  }, [detail, me?.id, code]);

  const dismiss = (kind: NoticeKind) => {
    if (detail) acknowledge(fromDetail(detail), me?.id, [kind]);
    setNotices((list) => list.filter((n) => n.kind !== kind));
  };

  // 从一局跳到另一局时组件是复用的（路由参数变了而已），弹层要跟着关掉，
  // 不然会停在上一个局的分享面板上。
  useEffect(() => {
    setShareOpen(false);
    setInviteOpen(false);
    setViewers(0);
  }, [code]);

  // 这一局的实时（#49）：座位有人坐上、有人退出、局头改了时间……服务端都发到 game:{code} 上，
  // 我们只当「有事发生」的信号，一律重新拉 REST。座位的落座动画由 GameTable 比对前后座位自己播，
  // 所以这里不需要关心事件里带的是什么。
  useRealtime({
    topics: [`game:${code}`],
    poll: true,
    onEvent: () => void load(),
    // 「👀 N 人正在看」：只认这一局的主题，从别的局跳过来时不会串。
    onPresence: (topic, n) => {
      if (topic === `game:${code}`) setViewers(n);
    },
    refresh: () => void load(),
  });

  /** 把错误翻成人话。 */
  const explain = (err: unknown): string => {
    if (err instanceof ApiError) {
      if (err.isClosed) return "这一局已经结束或取消了";
      if (err.code === "banned") return "你被移出过这一局，不能再报名";
      if (err.code === "quota_exceeded") return err.message;
      if (err.code === "game_started") return "这一局已经开始了，问问局头还能不能加";
      if (err.status === 503) return "人有点多，稍等一下再试";
      return err.message;
    }
    return "操作没成功，请再试一次";
  };

  /** 返回操作是否成功。 */
  const run = async (fn: () => Promise<GameDetail | void>): Promise<boolean> => {
    if (busy) return false;
    setBusy(true);
    try {
      const next = await fn();
      if (next) setDetail(next);
      else await load();
      setError("");
      return true;
    } catch (err) {
      setError(explain(err));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const onJoin = async () => {
    if (!me) {
      const identity = await requireIdentity();
      if (!identity) return;
      // 拿到身份后接着报名：cookie 已经在浏览器里了。
    }
    // 报上名之后问一句要不要提醒（方案 §3）。候补没有确定的座位，不问。
    let seated = false;
    const ok = await run(async () => {
      const next = await joinGame(code);
      seated = next.me.role === "seat";
      return next;
    });
    if (ok && seated) setJoinedOpen(true);
  };

  // ── 局头改牌局（K4 / K5 / K6）────────────────────────────────────────────

  /**
   * 提交一次修改。409 之后先判断冲突和本次修改有没有关系：
   * 没关系就带新版本号自动重提（K5），有关系才弹对比框（K4）。
   */
  const submitEdit = async (patch: GamePatch, baseline: GameDetail) => {
    setBusy(true);
    setError("");
    try {
      const next = await updateGame(code, patch, baseline.game.version);
      setDetail(next);
      toast("改好了，已经通知座位上的人");
      setEditOpen(false);
    } catch (err) {
      if (err instanceof ApiError && err.isVersionConflict) {
        const latest = err.body as GameDetail;
        if (conflictingFields(patch, baseline, latest).length === 0) {
          // K5：冲突与本次修改无关（最常见的是期间有人报名）——不打断用户，直接重提一次。
          await retryEdit(patch, latest);
          return;
        }
        // K4：同一个字段被两个人改了，摆出对比让局头决定。
        setConflict({ patch, baseline, latest, diffs: describeConflict(patch, baseline, latest) });
        setEditOpen(false);
      } else if (err instanceof ApiError && err.code === "sensitive_word") {
        // 内容不能收：弹层不关，就在那个字段下面说，用户写好的东西不动（原型 X1）。
        setEditError({ field: err.field, message: err.message });
      } else if (err instanceof ApiError && err.code === "not_host") {
        // K6：编辑期间局头被转给了别人。丢掉本地的修改，重新加载。
        toast("你已经不是局头了");
        setEditOpen(false);
        await load();
      } else {
        setError(explain(err));
      }
    } finally {
      setBusy(false);
    }
  };

  /** K5 的那一次重提；如果又撞上，就交给 K4 让用户选。 */
  const retryEdit = async (patch: GamePatch, latest: GameDetail) => {
    try {
      const next = await updateGame(code, patch, latest.game.version);
      setDetail(next);
      toast("已保存 · 期间有人报名");
      setEditOpen(false);
    } catch (err) {
      if (err instanceof ApiError && err.isVersionConflict) {
        const again = err.body as GameDetail;
        setConflict({ patch, baseline: latest, latest: again, diffs: describeConflict(patch, latest, again) });
        setEditOpen(false);
        return;
      }
      setError(explain(err));
    }
  };

  /** K4 里选「仍然改成我的」：带最新版本号重提一次。 */
  const keepMine = async () => {
    if (!conflict) return;
    const { patch, latest } = conflict;
    setConflict(null);
    setBusy(true);
    try {
      setDetail(await updateGame(code, patch, latest.game.version));
      toast("已按你的版本改好");
    } catch (err) {
      setError(explain(err));
    } finally {
      setBusy(false);
    }
  };

  const isHost = !!me && detail?.host.id === me.id;
  const closed = detail ? detail.game.status === "cancelled" || detail.game.status === "done" : false;

  const onSeatClick = useMemo(() => {
    if (!isHost || closed) return undefined;
    return (seat: Seat) => {
      if (seat.user.id === me?.id) {
        toast("局头不能移出自己，想走请点「退出」");
        return;
      }
      setKickTarget(seat);
    };
  }, [isHost, closed, me?.id, toast]);

  if (!detail) {
    // 还没拿到数据时底部没有操作栏，不用留那一块空白。
    return (
      <div class="body">
        <Nav title="牌局详情" onBack={onBack} />
        {error ? <p class="error">{error}</p> : <p class="empty-state">正在打开…</p>}
      </div>
    );
  }

  const { game, host, seats, waitlist, missing, me: myRole } = detail;
  const myWait = myRole.role === "waitlist" ? waitlist.find((w) => w.user.id === me?.id) : undefined;

  return (
    <>
      <div class="body has-actionbar">
        <Nav title="牌局详情" onBack={onBack} />

        {!closed &&
          notices.map((n) =>
            n.kind === "promoted" ? null : <NoticeBanner key={n.kind} notice={n} onDismiss={() => dismiss(n.kind)} />,
          )}

        {/* 被局头邀请了、还没报名（#115）。报上名之后就不用再提醒了。 */}
        {myRole.invited_by && myRole.role === "none" && !closed && (
          <div class="strip invited" data-testid="invited-by">
            <span class="dot on" />
            <span class="tx">
              {myRole.invited_by.nickname} 邀请了你
              <small>{missing > 0 ? `还差 ${missing} 人，报个名占座吧` : "已经满了，可以排候补"}</small>
            </span>
          </div>
        )}

        {myWait && !closed && (
          <div class="strip">
            <span class="dot" />
            <span class="tx">
              你是候补第 {myWait.position} 位
              <small>有人退出会自动补上你，并推送通知</small>
            </span>
          </div>
        )}

        {closed && (
          <div class="banner red">
            <span>{game.status === "cancelled" ? "这一局已经取消了" : "这一局已经结束"}</span>
          </div>
        )}

        <div class={`when${closed ? " dim" : ""}`}>
          <div class="d">{dayLabel(game.starts_at)}</div>
          <div class="t">{timeHM(game.starts_at)}</div>
          <div class="s">
            雅加达时间 WIB · {kindLabel(game.kind)} <OfflineDot />
          </div>
        </div>

        <GameTable detail={detail} meID={me?.id} onSeat={onSeatClick} />

        <div class="names">
          {seats.map((s) => (
            <span class="chip" key={s.seat_no}>
              {s.user.id === host.id ? `局头 · ${s.user.nickname}` : s.user.nickname}
              {s.user.id === me?.id ? "（我）" : ""}
            </span>
          ))}
          {waitlist.map((w) => (
            <span class="chip wait" key={w.user.id}>
              候补 {w.position} · {w.user.nickname}
            </span>
          ))}
          {/* 原型 R1：和名字同一排的普通胶囊。人数含自己，只有自己在看时不显示。 */}
          {viewers > 1 && !closed && realtimeStatus === "online" && (
            <span class="chip" data-testid="viewers">
              👀 {viewers} 人正在看
            </span>
          )}
        </div>

        <div class="info">
          <div class="r">
            <span>地点</span>
            <div>{game.venue}</div>
          </div>
          <div class="r">
            <span>商圈</span>
            <div>{game.area}</div>
          </div>
          <div class="r">
            <span>局头</span>
            <div>{host.nickname}</div>
          </div>
          {game.note && (
            <div class="r">
              <span>备注</span>
              <div>{game.note}</div>
            </div>
          )}
        </div>

        {myRole.role === "seat" && !closed && (
          <button class="btn ghost cal-entry" onClick={() => setCalendar(true)}>
            📅 加入日历
          </button>
        )}

        {isHost && !closed && (
          <button class="btn ghost cal-entry" onClick={() => setInviteOpen(true)}>
            ＋ 邀请牌友
          </button>
        )}

        <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
        {error && <p class="error">{error}</p>}
      </div>

      {inviteOpen && (
        <FriendPicker
          busy={inviting}
          inGame={
            new Map([
              ...seats.map((x) => [x.user.id, "已在座位上"] as const),
              ...waitlist.map((w) => [w.user.id, `候补第 ${w.position} 位`] as const),
            ])
          }
          confirmLabel={(n) => (n > 0 ? `邀请 ${n} 位` : "选几位牌友")}
          onConfirm={async (picked) => {
            setInviting(true);
            try {
              toast(inviteSummary(await inviteFriends(code, picked.map((f) => f.user.id))));
              setInviteOpen(false);
            } catch (err) {
              toast(inviteError(err));
            } finally {
              setInviting(false);
            }
          }}
          onClose={() => setInviteOpen(false)}
        />
      )}

      {editOpen && isHost && (
        <EditSheet
          detail={detail}
          error={editError}
          onClearError={() => setEditError(null)}
          onClose={() => setEditOpen(false)}
          onSubmit={(patch) => void submitEdit(patch, detail)}
        />
      )}

      {kickTarget && (
        <Sheet
          title={kickTarget.user.nickname}
          sub="移出后 TA 不能再报这局，会收到通知；有候补会自动补上"
          actionLabel="移出这局"
          onClose={() => setKickTarget(null)}
          onConfirm={() => {
            const target = kickTarget;
            setKickTarget(null);
            void run(() => kickPlayer(code, target.user.id));
          }}
        />
      )}

      {cancelOpen && (
        <Sheet
          title="取消这一局？"
          sub={`会通知 ${seats.length} 位座位上的人${
            waitlist.length > 0 ? `和 ${waitlist.length} 位候补` : ""
          }，取消后不能恢复。`}
          actionLabel="取消这局"
          onClose={() => setCancelOpen(false)}
          onConfirm={() => {
            setCancelOpen(false);
            void run(async () => {
              await cancelGame(code);
              toast("这一局已取消");
              await load();
            });
          }}
        />
      )}

      {joinedOpen && (
        <JoinedSheet
          detail={detail}
          meID={me?.id}
          toast={toast}
          onCalendar={() => {
            setJoinedOpen(false);
            setCalendar(true);
          }}
          onGuide={() => setGuide(true)}
          onClose={() => setJoinedOpen(false)}
        />
      )}

      {/* 微信 / WhatsApp 等内置浏览器 → H1，iOS Safari 标签页 → H2，盖在报名成功弹层上（#70）。 */}
      {guide && (
        <PushGuide
          signedIn={!!me}
          onCalendar={() => {
            setGuide(false);
            setJoinedOpen(false);
            setCalendar(true);
          }}
          onClose={() => setGuide(false)}
        />
      )}

      {/* W3：候补转正。实时事件触发的重新拉取，或者下次打开时都会走到这里（#114）。 */}
      {!closed && !joinedOpen && notices.some((n) => n.kind === "promoted") && (
        <PromotedSheet
          startsAt={game.starts_at}
          venue={game.venue}
          busy={busy}
          onOk={() => dismiss("promoted")}
          onCalendar={() => {
            dismiss("promoted");
            setCalendar(true);
          }}
          onLeave={() => {
            dismiss("promoted");
            void run(() => leaveGame(code));
          }}
        />
      )}

      {calendar && <CalendarSheet code={code} onClose={() => setCalendar(false)} />}

      {shareOpen && <ShareSheet detail={detail} toast={toast} onClose={() => setShareOpen(false)} />}

      {conflict && (
        <ConflictDialog
          state={conflict}
          onUseTheirs={() => {
            setDetail(conflict.latest);
            setConflict(null);
            toast("已用对方的版本");
          }}
          onKeepMine={() => void keepMine()}
          onCancel={() => setConflict(null)}
        />
      )}

      <div class="actionbar" ref={actionBar}>
        {/* 分享对所有能看到这一局的人都可用——报了名的、路过的，都能帮局头拉人（§5.4）。 */}
        {!closed && (
          <button class="btn dark-ghost" onClick={() => setShareOpen(true)}>
            ⇪ 分享
          </button>
        )}
        {myRole.role !== "none" && (
          <button class="btn ghost" disabled={busy || closed} onClick={() => void run(() => leaveGame(code))}>
            {myRole.role === "waitlist" ? "候补中 · 退出候补" : "已报名 · 退出"}
          </button>
        )}
        {isHost && !closed && (
          <button
            class="btn soft"
            onClick={() => {
              setEditError(null);
              setEditOpen(true);
            }}
          >
            改时间 / 地点
          </button>
        )}
        {isHost && !closed && (
          <button class="btn danger-ghost" disabled={busy || retrying} onClick={() => setCancelOpen(true)}>
            取消这局
          </button>
        )}
        {myRole.role === "none" && (
          <button class="btn primary" disabled={busy || retrying || closed} onClick={() => void onJoin()}>
            {retrying
              ? "人有点多，正在重试…"
              : busy
                ? "稍等…"
                : closed
                  ? "已结束"
                  : missing > 0
                    ? "报名占座"
                    : `排候补 · 第 ${waitlist.length + 1} 位`}
          </button>
        )}
      </div>
    </>
  );
}

function Nav({ title, onBack }: { title: string; onBack: () => void }) {
  return (
    <div class="nav">
      <button class="ico" onClick={onBack} aria-label="返回">
        ‹
      </button>
      <div>{title}</div>
      <span />
    </div>
  );
}

/** K4：同一个字段被两个人改了，让局头选用哪一边。 */
function ConflictDialog({
  state,
  onUseTheirs,
  onKeepMine,
  onCancel,
}: {
  state: ConflictState;
  onUseTheirs: () => void;
  onKeepMine: () => void;
  onCancel: () => void;
}) {
  return (
    <>
      <div class="scrim" onClick={onCancel} />
      <div class="dialog" role="dialog" aria-modal="true" data-testid="conflict-dialog">
        <h3>这局刚被改过</h3>
        <p class="sub">你改的这几项别人也动了，想用哪一边？</p>
        <div class="diffs">
          {state.diffs.map((d) => (
            <div class="diff" key={d.field}>
              <div class="label">{d.label}</div>
              <div class="row">
                <span class="mine">我：{d.mine}</span>
                <span class="theirs">现在：{d.theirs}</span>
              </div>
            </div>
          ))}
        </div>
        <div class="stack">
          <button class="btn soft" onClick={onUseTheirs}>
            用对方的版本
          </button>
          <button class="btn primary" onClick={onKeepMine}>
            仍然改成我的
          </button>
          <button class="textlink" onClick={onCancel}>
            先不改了
          </button>
        </div>
      </div>
    </>
  );
}

/** 底部确认弹层（K1 移出、K3 取消）。微信内置浏览器里原生 confirm 会显示域名，所以自己做。 */
function Sheet({
  title,
  sub,
  actionLabel,
  onClose,
  onConfirm,
}: {
  title: string;
  sub: string;
  actionLabel: string;
  onClose: () => void;
  onConfirm: () => void;
}) {
  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet" role="dialog" aria-modal="true">
        <div class="grab" />
        <h3>{title}</h3>
        <p class="sub">{sub}</p>
        <div class="stack">
          <button class="btn danger-ghost" onClick={onConfirm}>
            {actionLabel}
          </button>
          <button class="btn soft" onClick={onClose}>
            再想想
          </button>
        </div>
      </div>
    </>
  );
}

/** 局头改时间 / 地点。 */
function EditSheet({
  detail,
  error,
  onClearError,
  onClose,
  onSubmit,
}: {
  detail: GameDetail;
  /** 服务端拒绝了内容（敏感词）：在哪一项下面说、说什么（原型 X1）。 */
  error: { field: string; message: string } | null;
  onClearError: () => void;
  onClose: () => void;
  onSubmit: (patch: GamePatch) => void;
}) {
  const [when, setWhen] = useState(isoLocal(detail.game.starts_at));
  const [venue, setVenue] = useState(detail.game.venue);

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <form
        class="sheet"
        onSubmit={(e) => {
          e.preventDefault();
          const patch: GamePatch = {};
          const next = Date.parse(when);
          if (!Number.isNaN(next) && next !== detail.game.starts_at * 1000) {
            patch.starts_at = Math.floor(next / 1000);
          }
          if (venue.trim() && venue.trim() !== detail.game.venue) patch.venue = venue.trim();
          if (Object.keys(patch).length === 0) {
            onClose();
            return;
          }
          onSubmit(patch);
        }}
      >
        <div class="grab" />
        <h3>改一下这一局</h3>
        <p class="sub">改完会通知座位上的人，他们那边的提醒也会跟着更新</p>
        <div class="stack">
          <label class="sr-only" for="when">
            开局时间
          </label>
          <input id="when" type="datetime-local" value={when} onInput={(e) => setWhen((e.target as HTMLInputElement).value)} />
          <label class="sr-only" for="venue">
            地点
          </label>
          <input
            id="venue"
            class={error?.field === "venue" ? "err" : ""}
            value={venue}
            onInput={(e) => {
              onClearError();
              setVenue((e.target as HTMLInputElement).value);
            }}
            placeholder="地点"
          />
          {error?.field === "venue" && <p class="errmsg">⚠ {error.message}</p>}
          {/* 这个弹层只有时间和地点；万一是别处的问题，也别把话吞掉。 */}
          {error && error.field !== "venue" && <p class="errmsg">⚠ {error.message}</p>}
          <button class="btn primary" type="submit">
            保存
          </button>
          <button class="textlink" type="button" onClick={onClose}>
            算了
          </button>
        </div>
      </form>
    </>
  );
}

/** 把 Unix 秒转成 datetime-local 需要的本地字符串。 */
function isoLocal(unixSeconds: number): string {
  const d = new Date(unixSeconds * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
