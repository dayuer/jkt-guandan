import { useState } from "preact/hooks";
import { LESSONS, LEVELS, doneLessons, lessonsOf, type Level } from "../learn";
import "../learn.css";

interface Props {
  onOpen: (id: string) => void;
}

/**
 * 05 · 学牌列表（设计稿 E1，#106）。
 *
 * 内容是写死在前端的规则（learn.ts），进度只记在本机。底部 Tab 由 App 统一挂（#107）。
 */
export function Learn({ onOpen }: Props) {
  const done = doneLessons();
  // 默认停在第一个还有没学完的组。
  const [level, setLevel] = useState<Level>(
    () => LEVELS.find((l) => lessonsOf(l.key).some((x) => !done.has(x.id)))?.key ?? "basic",
  );
  const first = LESSONS.find((l) => !done.has(l.id));
  const meta = LEVELS.find((l) => l.key === level)!;

  return (
    <>
      <div class="body has-bar learn">
        <div class="nav">
          <span />
          <div>学牌</div>
          <span />
        </div>

        <section class="progress-card" data-testid="learn-progress">
          <div class="row">
            <h2>我的学习进度</h2>
            <span class="count">
              已学 <b>{done.size}</b> / {LESSONS.length} 课
            </span>
          </div>
          <div class="bar" aria-hidden="true">
            <i style={{ width: `${(done.size / LESSONS.length) * 100}%` }} />
          </div>
          {first ? (
            <button class="go" onClick={() => onOpen(first.id)}>
              {done.size === 0 ? "从第一课开始" : "接着学"}：{first.title} ›
            </button>
          ) : (
            <p class="go">九课都学完了，去约一局实战吧</p>
          )}
        </section>

        <div class="segmented" role="tablist">
          {LEVELS.map((l) => (
            <button
              key={l.key}
              role="tab"
              aria-selected={l.key === level}
              class={l.key === level ? "on" : ""}
              onClick={() => setLevel(l.key)}
            >
              {l.label}
            </button>
          ))}
        </div>

        <div class="learn-head">
          <h3>{meta.title}</h3>
          <span>{meta.hint}</span>
        </div>

        <ul class="lessons">
          {lessonsOf(level).map((l) => {
            const n = LESSONS.indexOf(l) + 1;
            const read = done.has(l.id);
            return (
              <li key={l.id}>
                <button class="lesson" onClick={() => onOpen(l.id)}>
                  <span class="pip" aria-hidden="true">
                    <b>{n}</b>♠
                  </span>
                  <span class="tx">
                    <strong>{l.title}</strong>
                    <span>{l.summary}</span>
                    <small>
                      {l.minutes} 分钟 · 含 1 道练习题
                    </small>
                  </span>
                  <span class={`state${read ? " done" : ""}`}>{read ? "已学完" : "未开始"}</span>
                </button>
              </li>
            );
          })}
        </ul>
      </div>
    </>
  );
}
