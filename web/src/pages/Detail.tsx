import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import {
  ApiError,
  cancelGame,
  getGame,
  joinGame,
  kickPlayer,
  leaveGame,
  updateGame,
  type GameDetail,
  type Seat,
  type UserRef,
} from "../api";
import { GameTable } from "../components/Table";
import { dayLabel, kindLabel, timeHM } from "../format";

interface Props {
  code: string;
  me: UserRef | null;
  /** 需要身份时弹称呼层，返回填好的身份；用户取消则返回 null。 */
  requireIdentity: () => Promise<UserRef | null>;
  /** 「我的」那一屏还没做，先只提示。 */
  toast: (text: string) => void;
  onBack: () => void;
}

/**
 * 03 · 牌局详情（设计稿 B1 / B3，以及 W1 / W2 / R3 / K1 / K3 / K4 / K5）。
 *
 * 状态全部来自服务端：`me.role` 说明我在不在这一局里，`missing` 说明还差几个人，
 * `status` 说明这一局还能不能报。前端不自己推断这些。
 */
export function Detail({ code, me, requireIdentity, toast, onBack }: Props) {
  const [detail, setDetail] = useState<GameDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // 局头改时间时的冲突处理（K4）：服务端返回的最新牌局。
  const [conflict, setConflict] = useState<GameDetail | null>(null);
  const [editOpen, setEditOpen] = useState(false);

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

  const run = async (fn: () => Promise<GameDetail | void>) => {
    if (busy) return;
    setBusy(true);
    try {
      const next = await fn();
      if (next) setDetail(next);
      else await load();
      setError("");
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.isVersionConflict) {
          // 有人抢先改了（§11.3）。K5：如果冲突不在你改的那个字段上，
          // 拿最新版本自动重试一次就行；否则把最新数据摆出来让用户确认（K4）。
          setConflict(err.body as GameDetail);
        } else {
          setError(err.message);
        }
      } else {
        setError("操作没成功，请再试一次");
      }
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
    await run(() => joinGame(code));
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
      if (!confirm(`把 ${seat.user.nickname} 移出这一局？他之后不能再报名。`)) return;
      void run(() => kickPlayer(code, seat.user.id));
    };
  }, [isHost, closed, me?.id, code]);

  if (!detail) {
    return (
      <div class="body">
        <Nav title="牌局详情" onBack={onBack} />
        {error ? <p class="error">{error}</p> : <p class="empty">正在打开…</p>}
      </div>
    );
  }

  const { game, host, seats, waitlist, missing, me: myRole } = detail;
  const myWait = myRole.role === "waitlist" ? waitlist.find((w) => w.user.id === me?.id) : undefined;

  return (
    <>
      <div class="body">
        <Nav title="牌局详情" onBack={onBack} />

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
          <div class="s">雅加达时间 WIB · {kindLabel(game.kind)}</div>
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

        <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
        {error && <p class="error">{error}</p>}
      </div>

      {conflict && (
        <ConflictBar
          latest={conflict}
          onAccept={() => {
            setDetail(conflict);
            setConflict(null);
          }}
        />
      )}

      {editOpen && isHost && (
        <EditSheet
          detail={detail}
          onClose={() => setEditOpen(false)}
          onSubmit={(patch) => {
            setEditOpen(false);
            void run(async () => {
              const next = await updateGame(code, patch, detail.game.version);
              toast("改好了，已经通知座位上的人");
              return next;
            });
          }}
        />
      )}

      <div class="actionbar">
        {myRole.role !== "none" && (
          <button
            class="btn ghost"
            disabled={busy || closed}
            onClick={() => void run(() => leaveGame(code))}
          >
            {myRole.role === "waitlist" ? "候补中 · 退出候补" : "已报名 · 退出"}
          </button>
        )}
        {isHost && !closed && (
          <button class="btn soft" onClick={() => setEditOpen(true)}>
            改时间 / 地点
          </button>
        )}
        {isHost && !closed && (
          <button
            class="btn danger-ghost"
            disabled={busy}
            onClick={() => {
              if (confirm("取消这一局？座位上的人和候补都会收到通知。")) {
                void run(async () => {
                  await cancelGame(code);
                  toast("这一局已取消");
                  await load();
                });
              }
            }}
          >
            取消这局
          </button>
        )}
        {myRole.role === "none" && (
          <button class="btn primary" disabled={busy || closed} onClick={() => void onJoin()}>
            {busy ? "稍等…" : missing > 0 ? "报名占座" : `排候补 · 第 ${waitlist.length + 1} 位`}
          </button>
        )}
        {myWait && (
          <span class="sr-only">你排在候补第 {myWait.position} 位</span>
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

/** K4：编辑期间有人改过这一局，而且字段冲突了，让用户确认以哪边为准。 */
function ConflictBar({ latest, onAccept }: { latest: GameDetail; onAccept: () => void }) {
  return (
    <div class="banner ink" style={{ position: "fixed", left: 0, right: 0, bottom: 0, margin: 0, zIndex: 30 }}>
      <span>
        这局刚被改过：现在是人 {latest.seats.length}/{latest.game.capacity}、
        {timeHM(latest.game.starts_at)} 在 {latest.game.venue}。
      </span>
      <button class="x" onClick={onAccept} aria-label="用最新的">
        知道了
      </button>
    </div>
  );
}

/** 局头改时间 / 地点。 */
function EditSheet({
  detail,
  onClose,
  onSubmit,
}: {
  detail: GameDetail;
  onClose: () => void;
  onSubmit: (patch: { starts_at?: number; venue?: string }) => void;
}) {
  const [when, setWhen] = useState(isoLocal(detail.game.starts_at));
  const [venue, setVenue] = useState(detail.game.venue);
  const first = useRef(true);
  useEffect(() => {
    first.current = false;
  }, []);

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <form
        class="sheet"
        onSubmit={(e) => {
          e.preventDefault();
          const patch: { starts_at?: number; venue?: string } = {};
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
          <input id="venue" value={venue} onInput={(e) => setVenue((e.target as HTMLInputElement).value)} placeholder="地点" />
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
