import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { installFakeSocket } from "../fakeSocket";
import { LESSONS, LEVELS, doneLessons, lessonsOf, markDone, nextLesson } from "../learn";
import { APP_NAME } from "../title";

/** 学牌 Tab（#106，设计稿 E1 / E2）。 */

function respond(status: number, body: unknown) {
  const text = JSON.stringify(body);
  return { ok: status >= 200 && status < 300, status, text: async () => text, json: async () => body } as unknown as Response;
}

// 每个用例挂上的 App 都要在用例结束时卸掉：只清 DOM 的话 App 还活着，还在听路由、改 document.title，
// 它排在下一帧之后的 effect 会落进下一个用例（#167）。
const mounted: HTMLElement[] = [];

function renderAt(path: string): HTMLElement {
  window.history.replaceState(null, "", path);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.startsWith("/api/me")) return respond(401, { error: { code: "unauthorized", message: "" } });
      if (url.startsWith("/api/games")) return respond(200, { games: [] });
      return respond(404, { error: { code: "not_found", message: "" } });
    }),
  );
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(<App />, container);
  mounted.push(container);
  return container;
}

function button(c: HTMLElement, text: string): HTMLButtonElement {
  const b = [...c.querySelectorAll("button")].find((x) => x.textContent?.includes(text));
  if (!b) throw new Error(`没有「${text}」：${c.textContent}`);
  return b as HTMLButtonElement;
}

beforeEach(() => installFakeSocket());

afterEach(() => {
  // `act` 包着卸，**不能省**：preact 的 useEffect 清理是**排队**跑的（jsdom 没有
  // requestAnimationFrame，走的是 hooks.mjs 里 35ms 的 setTimeout 兜底）。afterEach 里
  // 卸完这一条文件就结束了，清理会排在拆 jsdom **之后**才跑 —— 那时 `window` /
  // `cancelAnimationFrame` 已经没了，报成 unhandled error（测试却全是绿的，#248）。
  // `act` 把这次 flush 接住、当场同步跑完，什么都不留到文件外面。
  act(() => {
    for (const c of mounted.splice(0)) render(null, c);
  });
  document.body.innerHTML = "";
  document.title = "";
  localStorage.clear();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

describe("学牌内容", () => {
  it("九课，入门 / 进阶 / 高级各三课，id 不重复", () => {
    expect(LESSONS).toHaveLength(9);
    for (const l of LEVELS) expect(lessonsOf(l.key)).toHaveLength(3);
    expect(new Set(LESSONS.map((l) => l.id)).size).toBe(9);
  });

  it("每课都有正文和一道答案在选项里的练习题", () => {
    for (const l of LESSONS) {
      expect(l.sections.length, l.id).toBeGreaterThan(0);
      expect(l.quiz.options[l.quiz.answer], l.id).toBeTruthy();
      expect(l.quiz.explain, l.id).not.toBe("");
    }
  });

  it("覆盖 issue 要求的主题：牌型、炸弹、升级、进贡还贡、接风、逢人配", () => {
    const all = LESSONS.map((l) => l.title + l.sections.map((s) => s.title + s.body).join("")).join("\n");
    for (const word of ["三带二", "顺子", "钢板", "同花顺", "四王", "升 3 级", "还贡", "抗贡", "接风", "逢人配", "过 A"]) {
      expect(all, word).toContain(word);
    }
  });

  it("炸弹顺序：四张 < 五张 < 同花顺 < 六张 < … < 四王", () => {
    const bombs = LESSONS.find((l) => l.id === "bombs")!;
    expect(bombs.sections[0].body).toContain("四张炸 < 五张炸 < 同花顺 < 六张炸");
    expect(bombs.sections[0].body).toContain("< 四王");
  });

  it("下一课跨组接着走，最后一课没有下一课", () => {
    expect(nextLesson("levelup")?.id).toBe("bombs");
    expect(nextLesson(LESSONS[8].id)).toBeUndefined();
  });

  it("进度只认字符串 id，坏数据当作没学过", () => {
    markDone("overview");
    markDone("overview");
    expect([...doneLessons()]).toEqual(["overview"]);
    localStorage.setItem("gd_learn_done", "{broken");
    expect(doneLessons().size).toBe(0);
  });
});

describe("05 学牌列表 / 06 规则速览", () => {
  it("首页底部「学牌」能点，进 /learn，Tab 高亮、标题跟着换", async () => {
    const c = renderAt("/");
    await vi.waitFor(() => expect(c.querySelector(".tabbar")).not.toBeNull());
    button(c, "学牌").click();

    await vi.waitFor(() => expect(c.querySelector("[data-testid=learn-progress]")).not.toBeNull());
    expect(window.location.pathname).toBe("/learn");
    await vi.waitFor(() => expect(document.title).toBe(`学牌 · ${APP_NAME}`));
    expect(c.querySelector(".tabbar .tab.on")?.textContent).toContain("学牌");
    expect(c.textContent).toContain("已学 0 / 9 课");
    expect(c.textContent).toContain("从第一课开始：规则速览");
    expect([...c.querySelectorAll(".lesson strong")].map((x) => x.textContent)).toEqual(["规则速览", "牌型大全", "进贡与升级"]);
  });

  it("切到「进阶」看到炸弹、进贡、接风", async () => {
    const c = renderAt("/learn");
    await vi.waitFor(() => button(c, "进阶"));
    button(c, "进阶").click();
    await vi.waitFor(() =>
      expect([...c.querySelectorAll(".lesson strong")].map((x) => x.textContent)).toEqual(["炸弹大小", "进贡与还贡", "接风"]),
    );
  });

  it("点开一课进 /learn/:id；答错不算学完，答对后进度 +1、底部变成「下一课」", async () => {
    const c = renderAt("/learn");
    await vi.waitFor(() => button(c, "规则速览"));
    c.querySelector<HTMLButtonElement>(".lesson")!.click();

    await vi.waitFor(() => expect(c.querySelector("[data-testid=quiz]")).not.toBeNull());
    expect(window.location.pathname).toBe("/learn/overview");
    // 标题在 App 的 useEffect 里改，排在下一帧之后，要等（#167）。
    await vi.waitFor(() => expect(document.title).toBe(`规则速览 · 学牌 · ${APP_NAME}`));
    expect(c.querySelector(".felt figcaption")?.textContent).toContain("逢人配");
    expect(button(c, "先做练习题")).toBeTruthy();

    button(c, "黑桃 7").click();
    await vi.waitFor(() => expect(c.querySelector(".verdict")?.textContent).toContain("不对"));
    expect(doneLessons().has("overview")).toBe(false);

    button(c, "红桃 7").click();
    await vi.waitFor(() => expect(button(c, "下一课：牌型大全")).toBeTruthy());
    expect(doneLessons().has("overview")).toBe(true);

    // 下一课用替换：返回直接回学牌列表。
    const depth = window.history.length;
    button(c, "下一课：牌型大全").click();
    await vi.waitFor(() => expect(window.location.pathname).toBe("/learn/combos"));
    expect(window.history.length).toBe(depth);
    expect(c.querySelector(".verdict")).toBeNull();


  });

  it("学过的课在列表上显示「已学完」，进度条跟着走", async () => {
    markDone("overview");
    markDone("combos");
    const c = renderAt("/learn");
    await vi.waitFor(() => expect(c.textContent).toContain("已学 2 / 9 课"));
    expect(c.textContent).toContain("接着学：进贡与升级");
    expect(c.querySelectorAll(".lesson .state.done")).toHaveLength(2);
  });

  it("不存在的课给出提示，不白屏", async () => {
    const c = renderAt("/learn/nope");
    await vi.waitFor(() => expect(c.textContent).toContain("没有这一课"));
    await vi.waitFor(() => expect(document.title).toBe(`学牌 · ${APP_NAME}`));
  });
});
