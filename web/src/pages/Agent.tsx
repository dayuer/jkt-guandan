import { useEffect, useRef, useState } from "preact/hooks";
import { AgentError, askAgent, type AgentDraft, type AgentResult } from "../agent";
import { ApiError, createGame, newIdempotencyKey, type GameDetail, type UserRef } from "../api";
import { ShareSheet } from "../components/ShareSheet";
import { dayLabel, kindLabel, timeHM, weekday } from "../format";

/** 退回 08 表单时带过去的东西：草稿、问题、用户刚才说的话。 */
export interface FormPrefill {
  draft?: AgentDraft;
  problem?: string;
  said?: string;
}

interface Props {
  me: UserRef | null;
  requireIdentity: () => Promise<UserRef | null>;
  toast: (text: string) => void;
  onBack: () => void;
  /** 去 08 表单。蛋蛋没配 key 时也走这里，只是不提示。 */
  onForm: (prefill: FormPrefill) => void;
  onCreated: (code: string) => void;
}

type Phase =
  | { kind: "idle" }
  | { kind: "asking" }
  | { kind: "draft"; result: AgentResult }
  | { kind: "creating"; result: AgentResult }
  | { kind: "failed"; message: string }
  | { kind: "dropped" }
  | { kind: "shared"; detail: GameDetail };

const MAX_TEXT = 300;
/** 按雅加达时间问好（#69 复审建议 1：之前写死「晚上好」，上午打开也这么说）。 */
export function greeting(now: number = Math.floor(Date.now() / 1000)): string {
  const hour = Number(timeHM(now).slice(0, 2));
  const hello = hour >= 5 && hour < 11 ? "早上好" : hour >= 11 && hour < 18 ? "下午好" : "晚上好";
  return `${hello}。想打牌跟我说一声，什么时候、在哪儿，我帮你排好，你点确认就行。`;
}

/** 服务端的 invalid_input 带着「输入不合法：」前缀，直接给人看太生硬（#69 复审建议 4）。 */
export function plainInvalid(message: string): string {
  return message.replace(/^输入不合法(?:[:：]\s*|$)/, "") || "没开成，请再试一次";
}

/** 「周四晚上打一局？」 */
function draftTitle(startsAt?: number): string {
  if (!startsAt) return "打一局？";
  const hour = Number(timeHM(startsAt).slice(0, 2));
  const part = hour < 12 ? "上午" : hour < 18 ? "下午" : "晚上";
  return `${weekday(startsAt)}${part}打一局？`;
}

/**
 * 07 · 约局 · 蛋蛋（设计稿 D1 / D3，#11）。
 *
 * 说一句话 → 服务端流式回一句话 + 草稿 → 确认卡 →「确认，发到群里」照常 `POST /api/games`，
 * 开好后直接弹 04 分享面板。蛋蛋不能用时一律退回 08 表单（server-design §6）：
 * 没配 key 不提示；模型挂了、太忙、超时或推到一半出错，提示一句再给「直接填表」。
 */
export function Agent({ me, requireIdentity, toast, onBack, onForm, onCreated }: Props) {
  const [text, setText] = useState("");
  const [said, setSaid] = useState("");
  const [reply, setReply] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [error, setError] = useState("");
  const ctrl = useRef<AbortController | null>(null);
  const idemKey = useRef("");
  const chatEnd = useRef<HTMLDivElement>(null);

  // 离开页面就掐掉还在推的流，免得服务端白白占着一个模型并发名额。
  useEffect(() => () => ctrl.current?.abort(), []);

  // 新消息、确认卡出来时滚到底，不然 375 高的屏上确认卡落在输入框下面。
  useEffect(() => {
    chatEnd.current?.scrollIntoView?.({ block: "end", behavior: "smooth" });
  }, [said, reply, phase.kind]);

  const busy = phase.kind === "asking" || phase.kind === "creating";
  const trimmed = text.trim();

  const send = async (e?: Event) => {
    e?.preventDefault();
    if (busy || trimmed === "") return;
    const input = trimmed.slice(0, MAX_TEXT);
    ctrl.current?.abort();
    const c = new AbortController();
    ctrl.current = c;
    setSaid(input);
    setText("");
    setReply("");
    setError("");
    setPhase({ kind: "asking" });
    try {
      const result = await askAgent(input, { signal: c.signal, onReply: setReply });
      if (c.signal.aborted) return;
      idemKey.current = newIdempotencyKey();
      setPhase({ kind: "draft", result });
    } catch (err) {
      if (c.signal.aborted) return;
      const code = err instanceof AgentError ? err.code : "agent_unavailable";
      if (code === "agent_disabled") {
        onForm({ said: input });
        return;
      }
      if (code === "invalid_input" || code === "rate_limited") {
        // 这两种不是蛋蛋走神：把服务端的话原样告诉用户。
        const message = err instanceof AgentError ? err.message : "";
        setPhase({ kind: "failed", message: message || "蛋蛋走神了，直接填表吧" });
        return;
      }
      toast(code === "agent_busy" ? "蛋蛋有点忙" : "蛋蛋走神了");
      setPhase({ kind: "failed", message: "蛋蛋走神了，直接填表吧" });
    }
  };

  const confirm = async (result: AgentResult) => {
    const d = result.draft;
    if (!d.starts_at || !d.area || !d.venue || !d.kind) {
      onForm({ draft: d, problem: result.problem, said });
      return;
    }
    if (!me && !(await requireIdentity())) return;
    setError("");
    setPhase({ kind: "creating", result });
    try {
      const detail = await createGame(
        { starts_at: d.starts_at, area: d.area, venue: d.venue, kind: d.kind, note: d.note || undefined },
        idemKey.current,
      );
      setPhase({ kind: "shared", detail });
    } catch (err) {
      setPhase({ kind: "draft", result });
      setError(err instanceof ApiError && err.code === "invalid_input" ? plainInvalid(err.message) : "没开成，请再试一次");
    }
  };

  const toForm = () => {
    const draft = phase.kind === "draft" || phase.kind === "creating" ? phase.result : null;
    onForm({ draft: draft?.draft, problem: draft?.problem, said: said || trimmed || undefined });
  };

  return (
    <>
      <div class="body agent-page">
        <div class="nav">
          <button class="ico" onClick={onBack} aria-label="返回">
            ‹
          </button>
          <div>约局</div>
          <span />
        </div>

        <div class="agent-head">
          <span class="agent-avatar serif" aria-hidden="true">
            蛋
          </span>
          <div>
            <div class="agent-name serif">蛋蛋</div>
            <div class="agent-sub">约局助手 · 在线</div>
          </div>
        </div>

        <div class="chat" aria-live="polite">
          <p class="chat-day">今天 · 雅加达时间</p>
          <Bubble>{greeting()}</Bubble>

          {said && <div class="bubble me">{said}</div>}

          {phase.kind === "asking" && <Bubble>{reply || "蛋蛋想一想…"}</Bubble>}
          {phase.kind !== "asking" && phase.kind !== "idle" && reply && phase.kind !== "failed" && (
            <Bubble>{reply}</Bubble>
          )}

          {(phase.kind === "draft" || phase.kind === "creating") &&
            (phase.result.valid ? (
              <ConfirmCard
                result={phase.result}
                creating={phase.kind === "creating"}
                error={error}
                onConfirm={() => void confirm(phase.result)}
                onEdit={toForm}
                onDrop={() => setPhase({ kind: "dropped" })}
              />
            ) : (
              <>
                <Bubble>{phase.result.problem || "还差一点信息，去表单补一下吧"}</Bubble>
                <button class="btn primary agent-fallback" onClick={toForm}>
                  ✎ 去表单补一下
                </button>
              </>
            ))}

          {phase.kind === "dropped" && <Bubble>好，那这次先不打。想打了再叫我。</Bubble>}

          {phase.kind === "failed" && (
            <>
              <Bubble>{phase.message}</Bubble>
              <button class="btn primary agent-fallback" onClick={toForm}>
                ✎ 直接填表
              </button>
            </>
          )}

          {phase.kind === "shared" && <Bubble>局开好了，海报给你备好了 👇</Bubble>}
          <div ref={chatEnd} />
        </div>
      </div>

      <form class="composer" onSubmit={send}>
        <input
          value={text}
          maxLength={MAX_TEXT}
          aria-label="想什么时候打？说一句就行"
          placeholder="想什么时候打？说一句就行"
          disabled={busy}
          onInput={(e) => setText((e.target as HTMLInputElement).value)}
        />
        <button class="send" type="submit" aria-label="发送" disabled={busy || trimmed === ""}>
          ↑
        </button>
      </form>

      {phase.kind === "shared" && (
        <ShareSheet
          detail={phase.detail}
          toast={toast}
          onClose={() => onCreated(phase.detail.game.short_code)}
        />
      )}
    </>
  );
}

function Bubble({ children }: { children: preact.ComponentChildren }) {
  return (
    <div class="chat-row">
      <span class="agent-avatar small serif" aria-hidden="true">
        蛋
      </span>
      <div class="bubble">{children}</div>
    </div>
  );
}

function ConfirmCard({
  result,
  creating,
  error,
  onConfirm,
  onEdit,
  onDrop,
}: {
  result: AgentResult;
  creating: boolean;
  error: string;
  onConfirm: () => void;
  onEdit: () => void;
  onDrop: () => void;
}) {
  const d = result.draft;
  return (
    <section class="confirm-card" aria-label="需要你确认">
      <p class="confirm-tag">需要你确认</p>
      <h3 class="serif">{draftTitle(d.starts_at)}</h3>
      <dl class="confirm-rows">
        {d.starts_at && (
          <div>
            <dt>时间</dt>
            <dd>
              {dayLabel(d.starts_at)} {timeHM(d.starts_at)}
            </dd>
          </div>
        )}
        <div>
          <dt>地点</dt>
          <dd>{[d.area, d.venue].filter(Boolean).join(" · ")}</dd>
        </div>
        {d.kind && (
          <div>
            <dt>牌局</dt>
            <dd>{kindLabel(d.kind)}</dd>
          </div>
        )}
        {d.note && (
          <div>
            <dt>备注</dt>
            <dd>{d.note}</dd>
          </div>
        )}
      </dl>
      {error && <p class="error">{error}</p>}
      <button class="btn primary" disabled={creating} onClick={onConfirm}>
        {creating ? "正在开…" : "确认，发到群里"}
      </button>
      <div class="confirm-alt">
        <button class="btn ghost" disabled={creating} onClick={onEdit}>
          改一下
        </button>
        <button class="btn ghost" disabled={creating} onClick={onDrop}>
          不打了
        </button>
      </div>
      <p class="confirm-foot">确认后出海报和话术，你发到群里</p>
    </section>
  );
}
