import { useEffect, useState } from "preact/hooks";
import { doneLessons, lessonById, markDone, nextLesson } from "../learn";
import "../learn.css";

interface Props {
  id: string;
  onBack: () => void;
  onOpen: (id: string) => void;
}

/**
 * 06 · 规则速览（设计稿 E2，#106）：一课的正文、牌桌示意、秘诀和一道练习题。
 *
 * 练习题答对才算学完（写进本机进度），底部按钮从「先做练习题」变成「下一课」。
 */
export function Lesson({ id, onBack, onOpen }: Props) {
  const lesson = lessonById(id);
  const [picked, setPicked] = useState<number | null>(null);
  const [passed, setPassed] = useState(() => doneLessons().has(id));

  // App 按 id 给这个组件换 key，换课时状态自然重来；这里只负责回到页面顶部。
  useEffect(() => {
    window.scrollTo?.(0, 0);
  }, [id]);

  if (!lesson) {
    return (
      <div class="body">
        <div class="nav">
          <button class="ico" onClick={onBack} aria-label="返回">
            ‹
          </button>
          <div>学牌</div>
          <span />
        </div>
        <p class="empty-state">没有这一课</p>
      </div>
    );
  }

  const next = nextLesson(id);
  const correct = picked === lesson.quiz.answer;

  const pick = (i: number) => {
    setPicked(i);
    if (i === lesson.quiz.answer) {
      markDone(lesson.id);
      setPassed(true);
    }
  };

  return (
    <>
      <div class="body has-actionbar learn">
        <div class="nav">
          <button class="ico" onClick={onBack} aria-label="返回">
            ‹
          </button>
          <div>{lesson.title}</div>
          <span />
        </div>

        <header class="lesson-head">
          <h1>{lesson.title}</h1>
          <p>
            {lesson.summary} · {lesson.minutes} 分钟
          </p>
        </header>

        {lesson.hand && (
          <figure class="felt">
            <div class="hand">
              {lesson.hand.map((c, i) => (
                <span
                  key={i}
                  class={`pcard${c.suit === "♥" || c.suit === "♦" ? " red" : ""}${c.hot ? " hot" : ""}`}
                >
                  <b>{c.rank}</b>
                  {c.suit}
                </span>
              ))}
            </div>
            {lesson.handCaption && <figcaption>{lesson.handCaption}</figcaption>}
          </figure>
        )}

        {lesson.sections.map((s, i) => (
          <section class="rule" key={i}>
            <h2>
              <b>{String(i + 1).padStart(2, "0")}</b>
              {s.title}
            </h2>
            <p>{s.body}</p>
          </section>
        ))}

        {lesson.tip && (
          <aside class="tip">
            <b>秘诀</b>
            <p>{lesson.tip}</p>
          </aside>
        )}

        <section class="quiz" data-testid="quiz">
          <h2>练一练</h2>
          <p>{lesson.quiz.question}</p>
          <div class="opts">
            {lesson.quiz.options.map((o, i) => (
              <button
                key={i}
                class={picked === i ? (correct ? "right" : "wrong") : passed && i === lesson.quiz.answer ? "right" : ""}
                aria-pressed={picked === i}
                onClick={() => pick(i)}
              >
                {o}
              </button>
            ))}
          </div>
          {picked !== null && (
            <p class={`verdict${correct ? " ok" : ""}`} role="status">
              {correct ? "答对了。" : "不对，再想想。"}
              {correct && lesson.quiz.explain}
            </p>
          )}
        </section>
      </div>

      <div class="actionbar">
        {!passed ? (
          <button class="btn soft" onClick={() => document.querySelector(".quiz")?.scrollIntoView?.({ behavior: "smooth" })}>
            先做练习题
          </button>
        ) : next ? (
          <button class="btn primary" onClick={() => onOpen(next.id)}>
            下一课：{next.title}
          </button>
        ) : (
          <button class="btn primary" onClick={onBack}>
            学完了，回到学牌
          </button>
        )}
      </div>
    </>
  );
}
