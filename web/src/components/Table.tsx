import { useEffect, useRef, useState } from "preact/hooks";
import type { GameDetail, Seat } from "../api";

/** 座位在牌桌上的方位。按设计稿 B1：1 号在上，顺时针排过去（上 / 右 / 下 / 左）。 */
const POSITIONS = ["top", "right", "bottom", "left"] as const;

function positionFor(seatNo: number) {
  return POSITIONS[(seatNo - 1) % POSITIONS.length];
}

/** 称呼取首字做头像；空称呼兜底成「?」。 */
export function initial(nickname: string): string {
  return (nickname || "?").trim().slice(0, 1) || "?";
}

interface Props {
  detail: GameDetail;
  /** 当前用户的 id，用来把「我」的座位描成金色。 */
  meID?: string;
  /** 点某个座位（局头用来移出人）。 */
  onSeat?: (seat: Seat) => void;
}

/**
 * 招牌的牌桌视图（设计稿 B1 / W1 / W2）。
 *
 * 有人刚坐下时会放一下回弹动画：和上一次的座位比，新出现的那个加 `drop`。
 */
export function GameTable({ detail, meID, onSeat }: Props) {
  const bySeatNo = new Map(detail.seats.map((s) => [s.seat_no, s]));
  const closed = detail.game.status === "cancelled" || detail.game.status === "done";

  const previous = useRef<Set<string>>(new Set());
  const [justSat, setJustSat] = useState<Set<string>>(new Set());

  useEffect(() => {
    const current = new Set(detail.seats.map((s) => s.user.id));
    const added = new Set<string>();
    current.forEach((id) => {
      // 第一次渲染不算「刚坐下」——那时 previous 还是空的。
      if (previous.current.size > 0 && !previous.current.has(id)) added.add(id);
    });
    previous.current = current;
    if (added.size > 0) setJustSat(added);
  }, [detail.seats]);

  return (
    <div class="table-wrap">
      <div class={`table${closed ? " closed" : ""}`}>
        <div class="count">
          <div class="n">
            {detail.seats.length}/{detail.game.capacity}
          </div>
          <div class="l">{centerLabel(detail)}</div>
        </div>
      </div>

      {[1, 2, 3, 4].map((seatNo) => {
        const seat = bySeatNo.get(seatNo);
        const at = `seat ${positionFor(seatNo)}`;

        if (!seat) {
          return (
            <div class={at} key={seatNo}>
              <div class="disc empty" aria-hidden="true" />
            </div>
          );
        }

        const isHost = seat.user.id === detail.host.id;
        const isMe = !!meID && seat.user.id === meID;
        const classes = ["disc", isHost ? "host" : "other"];
        if (isMe) classes.push("me");
        if (justSat.has(seat.user.id)) classes.push("drop");

        const label = `${seat.user.nickname}${isHost ? "（局头）" : ""}${isMe ? "（我）" : ""}`;
        // 皇冠只在「我本人就是局头」时挂上，和原型一致（访客看到的局头座位没有皇冠）。
        const showCrown = isHost && isMe;
        return (
          <button
            class={at}
            key={seatNo}
            onClick={() => onSeat?.(seat)}
            aria-label={label}
            title={onSeat ? `${label}，点一下可以移出` : label}
          >
            <span class={classes.join(" ")}>
              {initial(seat.user.nickname)}
              {showCrown && <span class="crown">局头</span>}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** 桌心那行小字：满员时是「已成局（· N 人候补）」，否则是搭档提示（原型 W1 / K3）。 */
function centerLabel(detail: GameDetail): string {
  if (detail.game.status === "cancelled") return "已取消";
  if (detail.game.status === "done") return "已结束";
  if (detail.game.status !== "full") return "对家为搭档";
  const waiting = detail.waitlist.length;
  return waiting > 0 ? `已成局 · ${waiting} 人候补` : "已成局";
}
