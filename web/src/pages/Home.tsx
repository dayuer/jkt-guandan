import { useCallback, useEffect, useState } from "preact/hooks";
import { listGames, type GameSummary, type UserRef } from "../api";
import { dayLabel, missingLabel, timeHM } from "../format";

type Day = "today" | "tomorrow" | "weekend" | "";

const DAYS: { value: Day; label: string }[] = [
  { value: "today", label: "今天" },
  { value: "tomorrow", label: "明天" },
  { value: "weekend", label: "周末" },
  { value: "", label: "全部" },
];

interface Props {
  me: UserRef | null;
  onOpen: (code: string) => void;
  onCreate: () => void;
}

/**
 * 02 · 首页牌局列表（设计稿 A2 / A3，以及 W4 的「满 · 4/4」「候补第 N 位」「已报名」标签）。
 *
 * 「今天 / 明天 / 周末」由服务端按 Asia/Jakarta 算（§13.2），前端只传筛选条件。
 */
export function Home({ me, onOpen, onCreate }: Props) {
  const [day, setDay] = useState<Day>("today");
  const [games, setGames] = useState<GameSummary[] | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setGames(await listGames({ day }));
      setError("");
    } catch {
      setError("列表没拉到，稍后再试");
      setGames([]);
    }
  }, [day]);

  useEffect(() => {
    setGames(null);
    void load();
  }, [load]);

  return (
    <>
      <div class="body">
        <div class="section-head">
          <h2>随时掼掼蛋</h2>
          <span class="meta" style={{ color: "var(--ink-3)", fontSize: "var(--fs-13)" }}>
            雅加达
          </span>
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

        {error && <p class="error">{error}</p>}

        {games === null && <p class="empty">正在找局…</p>}

        {games !== null && games.length === 0 && !error && (
          <p class="empty">
            这几天还没有局。
            <br />
            要不你开一个？
          </p>
        )}

        {games?.map((g) => (
          <button class="card" key={g.short_code} onClick={() => onOpen(g.short_code)}>
            <div class="top">
              <span class="time serif">{timeHM(g.starts_at)}</span>
              <span class="venue">{g.venue}</span>
            </div>
            <div class="meta">
              {dayLabel(g.starts_at)} · {g.area} · 局头 {g.host.nickname}
            </div>
            <div class="foot">
              <span class={`tag${g.status === "full" ? " full" : " red"}`}>
                {g.status === "full" ? `满 · ${g.seats}/${4}` : missingLabel(g.missing)}
              </span>
            </div>
          </button>
        ))}
      </div>

      <div class="actionbar single">
        <button
          class="btn primary"
          onClick={() => {
            if (!me) {
              // 开局要先有身份；交给 create 页自己处理（它会弹称呼层）。
            }
            onCreate();
          }}
        >
          开一局
        </button>
      </div>
    </>
  );
}
