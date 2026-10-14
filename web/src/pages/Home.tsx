import { useCallback, useEffect, useState } from "preact/hooks";
import { listGames, type GameSummary, type UserRef } from "../api";
import { initial } from "../components/Table";
import { kindLabel, missingLabel, slashDate, timeHM } from "../format";
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
}

/**
 * 02 · 首页牌局列表（设计稿 A2 / A3，以及 W4 的「满 · 4/4」「候补第 N 位」状态）。
 *
 * 「今天 / 明天 / 周末」由服务端按 Asia/Jakarta 算（§13.2），前端只传筛选条件。
 */
export function Home({ me, onOpen, onCreate }: Props) {
  const [day, setDay] = useState<Day>("");
  const [area, setArea] = useState("");
  const [games, setGames] = useState<GameSummary[] | null>(null);
  const [error, setError] = useState("");

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

  // 滚动后给固定头部加一条分隔线；首页期间关掉下拉回弹，免得 iOS 把头部一起拽下来（#111）。
  const [scrolled, setScrolled] = useState(false);
  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 0);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    document.documentElement.classList.add("lock-overscroll");
    return () => {
      window.removeEventListener("scroll", onScroll);
      document.documentElement.classList.remove("lock-overscroll");
    };
  }, []);

  const waiting = games?.reduce((n, g) => n + g.missing, 0) ?? 0;

  return (
    <>
      <div class="body has-bar">
        {/* 标题、定位、日期和片区筛选是一整块，钉在顶部不跟着列表滚（#111）。 */}
        <div class={`home-top${scrolled ? " scrolled" : ""}`} data-testid="home-top">
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
        </div>

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
                <span class={`need${g.status === "full" ? " full" : ""}`}>
                  {g.status === "full" ? `满 · ${g.seats}/4` : missingLabel(g.missing)}
                </span>
              </div>
            </div>
          </button>
        ))}

        <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
      </div>
    </>
  );
}
