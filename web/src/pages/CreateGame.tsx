import { useEffect, useRef, useState } from "preact/hooks";
import { ApiError, createGame, newIdempotencyKey, type UserRef } from "../api";
import { dayShort, slashDate, timeHM, weekday } from "../format";
import type { FormPrefill } from "./Agent";
import { useRetrying } from "../useRetrying";

/** 允许的片区，和 internal/games 里的白名单一致（§12.2：不接受任意字符串）。 */
const AREAS = ["SCBD", "PIK", "Central Park", "Kemang", "Kuningan", "Senayan", "Kelapa Gading", "BSD", "Other"];
const KINDS: { value: string; label: string }[] = [
  { value: "classic", label: "经典" },
  { value: "no-level", label: "不升级" },
  { value: "teaching", label: "带新手" },
];

/** 常用开局时间：今晚 / 明晚 / 后天晚，都是 WIB 19:30。 */
function commonTime(offsetDays: number): { label: string; unix: number } {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  d.setHours(19, 30, 0, 0);
  const unix = Math.floor(d.getTime() / 1000);
  const prefix = offsetDays === 0 ? "今晚" : offsetDays === 1 ? "明晚" : "后天";
  return { label: `${prefix} ${weekday(unix)} 19:30`, unix };
}

/**
 * 蛋蛋草稿里的时间，写法和旁边的常用时间对齐：「明天 周四 20:00」；
 * 不是今明两天才写日期「10/17 周六 20:00」（#69 复审建议 2）。
 */
export function draftTimeLabel(startsAt: number, now: number = Math.floor(Date.now() / 1000)): string {
  const day = dayShort(startsAt, now);
  const prefix = day === "今天" || day === "明天" ? day : slashDate(startsAt);
  return `${prefix} ${weekday(startsAt)} ${timeHM(startsAt)}`;
}

/** 蛋蛋给的时间不在三个常用时间里时，加成第一个选项并选中。 */
function withDraftTime(presets: { label: string; unix: number }[], startsAt?: number) {
  if (!startsAt || presets.some((p) => p.unix === startsAt)) return presets;
  return [{ label: draftTimeLabel(startsAt), unix: startsAt }, ...presets];
}

interface Props {
  me: UserRef | null;
  requireIdentity: () => Promise<UserRef | null>;
  toast: (text: string) => void;
  onBack: () => void;
  onCreated: (code: string) => void;
  /** 从 07 蛋蛋退回来时带的草稿（#11）。 */
  prefill?: FormPrefill | null;
}

/**
 * 08 · 开一局（设计稿 D4）。
 *
 * 提交带 `Idempotency-Key`（§11.4）：微信里双击、或者超时后用户再点一次，
 * 都不会开出两个局。键在**这次提交开始时**生成一次并记住，重试复用同一个。
 */
export function CreateGame({ me, requireIdentity, toast, onBack, onCreated, prefill }: Props) {
  const draft = prefill?.draft ?? {};
  const presets = useRef(withDraftTime([commonTime(0), commonTime(1), commonTime(2)], draft.starts_at));
  const [startsAt, setStartsAt] = useState(draft.starts_at ?? presets.current[0].unix);
  const [area, setArea] = useState(draft.area && AREAS.includes(draft.area) ? draft.area : AREAS[0]);
  const [venue, setVenue] = useState(draft.venue ?? "");
  const [kind, setKind] = useState(draft.kind && KINDS.some((k) => k.value === draft.kind) ? draft.kind : "classic");
  const [note, setNote] = useState(draft.note ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // 命中敏感词时是哪个字段（§14）：提示要放在对应的输入框下面，而不是笼统地报一句错。
  const [badField, setBadField] = useState("");
  const retrying = useRetrying();

  // 幂等键代表「这一次提交」（§11.4）。放进状态，只有表单内容变了才换新的：
  // 固定不变的话，用户改了内容又提交会用同一个键、拿到上一次的结果；
  // 每次点击都换的话，第一次其实成功了只是响应超时，用户再点一次就会开出两个局。
  const [idemKey, setIdemKey] = useState(() => newIdempotencyKey());
  useEffect(() => {
    setIdemKey(newIdempotencyKey());
  }, [startsAt, area, venue, kind, note]);

  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy) return;

    if (!me) {
      const identity = await requireIdentity();
      if (!identity) return;
    }

    setBusy(true);
    setError("");
    setBadField("");
    try {
      const detail = await createGame(
        { starts_at: startsAt, area, venue: venue.trim(), kind, note: note.trim() || undefined },
        idemKey,
      );
      toast("开好了，把链接发到群里吧");
      onCreated(detail.game.short_code);
    } catch (err) {
      if (err instanceof ApiError) {
        // 敏感词：就地提示在出问题的那个输入框下面，已经写好的内容一个字都不动（原型 X1）。
        if (err.code === "sensitive_word") {
          setBadField(err.field);
          setError(err.message);
        } else {
          setError(
            err.code === "quota_exceeded"
              ? err.message
              : err.code === "invalid_input"
                ? err.message
                : "没开成，请再试一次",
          );
        }
      } else {
        setError("没开成，请再试一次");
      }
    } finally {
      setBusy(false);
    }
  };

  /** 用户开始改那个字段了，就把提示收起来。 */
  const clearBad = (field: string) => {
    if (badField !== field) return;
    setBadField("");
    setError("");
  };

  return (
    <>
      <div class="body has-actionbar">
        <div class="nav">
          <button class="ico" onClick={onBack} aria-label="返回">
            ‹
          </button>
          <div>开一局</div>
          <span />
        </div>

        <form onSubmit={submit}>
          {(prefill?.problem || prefill?.said) && (
            <div class="prefill-note">
              {prefill.said && <p>你刚才说：「{prefill.said}」</p>}
              {prefill.problem && <p class="problem">{prefill.problem}</p>}
            </div>
          )}

          <div class="field">
            <label>什么时候</label>
            <div class="choices">
              {presets.current.map((p) => (
                <button
                  type="button"
                  key={p.unix}
                  class={startsAt === p.unix ? "on" : ""}
                  onClick={() => setStartsAt(p.unix)}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          <div class="field">
            <label for="area">哪个片区</label>
            <select id="area" value={area} onChange={(e) => setArea((e.target as HTMLSelectElement).value)}>
              {AREAS.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
          </div>

          <div class="field">
            <label for="venue">具体地点</label>
            <input
              id="venue"
              class={badField === "venue" ? "err" : ""}
              value={venue}
              maxLength={40}
              placeholder="比如：Pacific Place · 鼎泰丰包间"
              onInput={(e) => {
                clearBad("venue");
                setVenue((e.target as HTMLInputElement).value);
              }}
            />
            {badField === "venue" && <p class="errmsg">⚠ {error}</p>}
          </div>

          <div class="field">
            <label>怎么打</label>
            <div class="choices">
              {KINDS.map((k) => (
                <button type="button" key={k.value} class={kind === k.value ? "on" : ""} onClick={() => setKind(k.value)}>
                  {k.label}
                </button>
              ))}
            </div>
          </div>

          <div class="field">
            <label for="note">备注（可不填）</label>
            <textarea
              id="note"
              class={badField === "note" ? "err" : ""}
              value={note}
              maxLength={200}
              placeholder="比如：新手也欢迎，慢慢打"
              onInput={(e) => {
                clearBad("note");
                setNote((e.target as HTMLTextAreaElement).value);
              }}
            />
            {badField === "note" && <p class="errmsg">⚠ {error}</p>}
          </div>

          <p class="disclaimer">纯娱乐 · 不设彩头 · 不涉及任何金钱输赢</p>
          {error && !badField && <p class="error">{error}</p>}
        </form>
      </div>

      <div class="actionbar single">
        <button class="btn primary" disabled={busy || retrying || venue.trim() === ""} onClick={submit}>
          {retrying ? "人有点多，正在重试…" : busy ? "正在开…" : "开这一局"}
        </button>
      </div>
    </>
  );
}
