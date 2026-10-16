// vitest 的 setupFiles（vite.config.ts 里挂上）：每个测试文件跑之前都会执行这一段。
//
// #248：preact 的 useEffect 副作用**不是当场跑的，是排队的** —— jsdom 没有
// requestAnimationFrame，preact 的 hooks.mjs 就退回 `z()` 里 35ms 的 setTimeout 兜底。
// 一条测试文件跑完、**拆掉 jsdom 之后**，排在队里的副作用/清理才跑：那时 `window`、
// `cancelAnimationFrame` 已经没了，直接抛 ReferenceError。vitest 把它记成
// 「Unhandled Errors」，**375 条测试照样全绿**，所以看着像偶发，其实证据一直都在。
//
// 这里就做一件事：**文件跑完时（jsdom 还在）把还排着的副作用当场跑掉**，
// 一个定时器也不让它活过测试。为此把 preact 的 flush 调度包一层
// （有时序原样转发，只是把回调记下来），并在 afterAll 里排空 + 断言排空。
//
// 为什么放在 setupFiles 里、而不是逐个测试文件改：**这不是哪一条用例的问题**。
// 把护栏挂上跑一遍，36 条文件里有十几条在文件结束时都还排着至少一次 flush ——
// 而且每次跑命中的具体是哪几条还不一样（跟「最后一条用例留下什么没跑完」有关）。
// 逐个改等于要求以后每加一个测试文件都记得这件事，而漏掉的时候**一点声音都没有**。
//
// 另外三处 afterEach 里卸组件的地方仍然用 `act()` 包着
// （pages/Learn.test.tsx、pages/Detail.test.tsx、components/guides.test.tsx）：
// `Agent.test.tsx` 本来就是那么写的，是仓库里的现成写法；包上之后那三处连 flush
// 都不会排，比「排了再排空」更直接。
import { options } from "preact";
import { afterAll, expect } from "vitest";

/** 排了、还没跑的 flush。 */
const pending = new Set<() => void>();
const original = options.requestAnimationFrame;

options.requestAnimationFrame = ((cb: () => void) => {
  const entry = () => {
    pending.delete(entry);
    cb();
  };
  pending.add(entry);
  // 有真的 rAF 就交给它；没有就照抄 preact 自己的兜底时序（hooks.mjs 的 z()：
  // 35ms 之后再排一个 setTimeout）。被包一层之后行为不变，只是多记了一个数。
  if (original) return original(entry);
  const timer = setTimeout(() => {
    clearTimeout(timer);
    setTimeout(entry);
  }, 35);
  return timer as unknown as number;
}) as typeof options.requestAnimationFrame;

afterAll(() => {
  // 排空（副作用自己也可能会再排新的，所以循环几次）。跑的时候 jsdom 还在，
  // 这正是它们本来该跑的时候。
  for (let i = 0; i < 10 && pending.size > 0; i++) {
    const left = [...pending];
    pending.clear();
    for (const run of left) run();
  }
  expect(
    pending.size,
    "文件跑完了还排着 preact 的副作用 —— 它会落到拆 jsdom 之后（#248）",
  ).toBe(0);
});
