import { useCallback, useEffect, useState } from "preact/hooks";
import { leaveGame, listGames, type GameSummary, type UserRef } from "../api";
import { NoticeBanner, PromotedSheet } from "../components/StatusNotices";
import { initial } from "../components/Table";
import { kindLabel, missingLabel, slashDate, timeHM } from "../format";
import { acknowledge, fromSummary, observe, type Notice } from "../status";
import { OfflineDot, useRealtime } from "../useRealtime";

type Day = "today" | "tomorrow" | "weekend" | "";

/** 「全部」在最前面并且默认选中，和设计稿 A2 一致。 */
const DAYS: { value: Day; label: string }[] = [
  { value: "", label: "全部" },
  { value: "today", label: "今天" },
  { value: "tomorrow", label: "明天" },
  { value: "weekend", label: "周末" },
];

const AREAS = ["SCBD", "PIK", "Central Park", "Kemang", "Kuningan", "Senayan", "Kelapa Gading", "BSD", "Other"];

/** 卡片上那行小字：「今天」/「明天」/「10/17」。 */
function dayShort(unixSeconds: number): string {
  const target = new Date(unixSeconds * 1000).toLocaleDateString("en-CA", { timeZone: "Asia/Jakarta" });
  const now = new Date();
  const today = now.toLocaleDateString("en-CA", { timeZone: "Asia/Jakarta" });
  const tomorrow = new Date(now.getTime() + 24 * 3600 * 1000).toLocaleDateString("en-CA", {
    timeZone: "Asia/Jakarta",
  });
  if (target === today) return "今天";
  if (target === tomorrow) return "明天";
  return slashDate(unixSeconds);
}

interface Props {
  me: UserRef | null;
  onOpen: (code: string) => void;
  onCreate: () => void;
  /** 补位弹层里「让给下一位」的结果要说一声（#114）。 */
  toast?: (text: string) => void;
}

/**
 * 02 · 首页牌局列表（设计稿 A2 / A3，以及 W4 的「满 · 4/4」「候补第 N 位」状态）。
 *
 * 「今天 / 明天 / 周末」由服务端按 Asia/Jakarta 算（§13.2），前端只传筛选条件。
 */
export function Home({ me, onOpen, onCreate, toast }: Props) {
  const [day, setDay] = useState<Day>("");
  const [area, setArea] = useState("");
  const [games, setGames] = useState<GameSummary[] | null>(null);
  const [error, setError] = useState("");
  // 我在里面的局有没有变化（#114）：改时间、补位、局头转交。
  const [notices, setNotices] = useState<Notice[]>([]);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setGames(await listGames({ day, area }));
      setError("");
    } catch {
      setError("列表没拉到，稍后再试");
      setGames([]);
    }
  }, [day, area]);

  useEffect(() => {
    setGames(null);
    void load();
  }, [load]);

  // 列表的实时（#49）：服务端把「这个片区有新局 / 有局变了」发到 feed:{area} 上，
  // 收到就重新拉一次列表。事件本身不带完整数据，所以不试着往列表里塞。
  //
  // 消息是按片区发的，没有「所有片区」这个主题；所以选了「全部」时逐个片区订阅
  // （服务端每个连接最多 20 个主题，这里最多 9 个）。
  const topics = (area ? [area] : AREAS).map((a) => `feed:${a}`);
  useRealtime({
    topics,
    poll: true,
    onEvent: () => void load(),
    refresh: () => void load(),
  });

  // 列表每加载一次就和本机快照比一次。只看服务端给了 my_role 的局：没有它就不知道我还在不在里面。
  useEffect(() => {
    if (!games || !me) return;
    setNotices(games.filter((g) => g.my_role !== undefined).flatMap((g) => observe(fromSummary(g), me.id)));
  }, [games, me?.id]);

  const dismiss = (n: Notice) => {
    const g = games?.find((x) => x.short_code === n.code);
    if (g) acknowledge(fromSummary(g), me?.id, [n.kind]);
    setNotices((list) => list.filter((x) => !(x.code === n.code && x.kind === n.kind)));
  };

  const promoted = notices.find((n) => n.kind === "promoted");
  const promotedGame = promoted && games?.find((g) => g.short_code === promoted.code);
  const banners = notices.filter((n): n is Exclude<Notice, { kind: "promoted" }> => n.kind !== "promoted");

  const waiting = games?.reduce((n, g) => n + g.missing, 0) ?? 0;

  return (
    <>
      <div class="body has-bar">
        <div class="head">
          <h1>随时掼掼蛋</h1>
          <p class="sub">
            📍 Jakarta · 找人凑一桌，就差你一个 <OfflineDot />
          </p>
        </div>

        <div class="segmented" role="tablist" aria-label="按日期筛选">
          {DAYS.map((d) => (
            <button
              key={d.value || "all"}
              role="tab"
              aria-selected={day === d.value}
              class={day === d.value ? "on" : ""}
              onClick={() => setDay(d.value)}
            >
              {d.label}
            </button>
          ))}
        </div>

        <div class="areachips" role="tablist" aria-label="按片区筛选">
          {["", ...AREAS].map((a) => (
            <button
              key={a || "all"}
              role="tab"
              aria-selected={area === a}
              class={area === a ? "on" : ""}
              onClick={() => setArea(a)}
            >
              {a || "全部"}
            </button>
          ))}
        </div>

        {banners.map((n) => (
          <NoticeBanner
            key={`${n.code}-${n.kind}`}
            notice={n}
            withVenue
            onOpen={() => {
              dismiss(n);
              onOpen(n.code);
            }}
            onDismiss={() => dismiss(n)}
          />
        ))}

        <div class="listing">
          <h2>可报名的局</h2>
          <span class="count">{games && games.length > 0 ? `${waiting} 桌在等人` : ""}</span>
        </div>

        {error && <p class="error">{error}</p>}
        {games === null && <p class="empty-state">正在找局…</p>}
        {games !== null && games.length === 0 && !error && (
          <p class="empty-state">
            这几天还没有局。
            <br />
            <button class="link" style={{ marginTop: 12 }} onClick={onCreate}>
              开一局 ›
            </button>
          </p>
        )}

        {games?.map((g) => (
          <button class="gamecard" key={g.short_code} onClick={() => onOpen(g.short_code)}>
            <div class="when-block">
              <div class="d">{dayShort(g.starts_at)}</div>
              <div class="t serif">{timeHM(g.starts_at)}</div>
              <div class="md">{slashDate(g.starts_at)}</div>
            </div>

            <div class="main">
              <div class="tags">
                <span class="tag">{g.area}</span>
                {kindLabel(g.kind)}
              </div>
              <div class="venue">{g.venue}</div>
              <div class="row">
                <div class="dots">
                  {[1, 2, 3, 4].map((no) => {
                    const seat = g.seat_list?.find((s) => s.seat_no === no);
                    if (!seat) return <i class="blank" key={no} />;
                    const isHost = seat.user.id === g.host.id;
                    const isMe = !!me && seat.user.id === me.id;
                    return (
                      <i class={isHost ? "host" : isMe ? "me" : ""} key={no}>
                        {initial(seat.user.nickname)}
                      </i>
                    );
                  })}
                </div>
                {/* W4：我在这局里时，标签换成我的位置（#114）。 */}
                {g.my_role === "seated" ? (
                  <span class="need mine">已报名</span>
                ) : g.my_role === "waitlist" ? (
                  <span class="need waiting">{g.waitlist_pos ? `候补第 ${g.waitlist_pos} 位` : "候补中"}</span>
                ) : (
                  <span class={`need${g.status === "full" ? " full" : ""}`}>
                    {g.status === "full" ? `满 · ${g.seats}/4` : missingLabel(g.missing)}
                  </span>
                )}
              </div>
            </div>
          </button>
        ))}

        <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
      </div>

      {promoted && promotedGame && (
        <PromotedSheet
          startsAt={promotedGame.starts_at}
          venue={promotedGame.venue}
          busy={busy}
          onOk={() => dismiss(promoted)}
          // 日历按钮在详情页上（#6）：直接带过去。
          onCalendar={() => {
            dismiss(promoted);
            onOpen(promoted.code);
          }}
          onLeave={async () => {
            dismiss(promoted);
            setBusy(true);
            try {
              await leaveGame(promoted.code);
              toast?.("已经让给下一位了");
            } catch {
              toast?.("没退成，进这一局再试一次");
            } finally {
              setBusy(false);
              void load();
            }
          }}
        />
      )}
    </>
  );
}
