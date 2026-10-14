import { expect, request, test, type APIRequestContext, type Page } from "@playwright/test";

/**
 * 浏览器端到端冒烟（#72），全部在 375×667 下跑（分享面板另外看 375×560）。
 *
 * 用到的数据：`guandan seed` 的 SEED01（今天 19:30，三缺一），加上 beforeAll 通过 API 开的 12 局。
 * 自己开的局都在明天以后，所以不管 CI 在一天里什么时候跑，报名、分享都不会撞上「已经开始」。
 */

const ORIGIN = `http://127.0.0.1:${process.env.E2E_PORT ?? 18673}`;
/** 自己开的局放在这两个片区；首页的空状态用 BSD（seed 和这里都不碰它）。 */
const AREAS = ["Kuningan", "Senayan"];

/** 一个独立的身份（各自的 cookie）。写请求要带 Origin，服务端按 CSRF 规则校验（§10.5）。 */
async function identity(nickname: string): Promise<APIRequestContext> {
  const ctx = await request.newContext({ baseURL: ORIGIN, extraHTTPHeaders: { Origin: ORIGIN } });
  const res = await ctx.post("/api/me", { data: { nickname } });
  expect(res.ok(), `POST /api/me ${res.status()}`).toBeTruthy();
  return ctx;
}

let seq = 0;
async function openGame(host: APIRequestContext, hoursAhead: number, area: string): Promise<string> {
  const startsAt = Math.floor(Date.now() / 1000) + 24 * 3600 + hoursAhead * 3600;
  const res = await host.post("/api/games", {
    data: { starts_at: startsAt, area, venue: `E2E 测试场 ${++seq}`, kind: "classic" },
    headers: { "Idempotency-Key": `e2e-${Date.now()}-${seq}` },
  });
  expect(res.ok(), `POST /api/games ${res.status()} ${await res.text()}`).toBeTruthy();
  return (await res.json()).game.short_code as string;
}

const codes: string[] = [];

test.beforeAll(async () => {
  // 每人同时最多占 5 个位置（局头占一个），所以分给三个局头，各开 4 局。
  for (const name of ["E2E 局头甲", "E2E 局头乙", "E2E 局头丙"]) {
    const host = await identity(name);
    for (let i = 0; i < 4; i++) codes.push(await openGame(host, codes.length, AREAS[codes.length % AREAS.length]));
    await host.dispose();
  }
});

// 每个用例：拦掉所有非本机请求（并记下来），收集 console error 和页面异常，用例结束时都必须为空。
const problems = new WeakMap<Page, string[]>();

test.beforeEach(async ({ page }) => {
  const list: string[] = [];
  problems.set(page, list);
  await page.context().route("**/*", (route) => {
    const url = new URL(route.request().url());
    if (url.origin === ORIGIN || url.protocol === "data:" || url.protocol === "blob:") return route.continue();
    list.push(`外网请求：${url.href}`);
    return route.abort();
  });
  page.on("console", (msg) => {
    if (msg.type() !== "error") return;
    // 唯一预期内的：还没有身份的访客，GET /api/me 是 401（应用当作「未登录」处理，不是错误）。
    // Chromium 会把它记成一条「Failed to load resource」。
    const url = msg.location().url ?? "";
    if (/Failed to load resource.*401/.test(msg.text()) && new URL(url, ORIGIN).pathname === "/api/me") return;
    list.push(`console.error：${msg.text()}（${url}）`);
  });
  page.on("pageerror", (err) => list.push(`页面异常：${err.message}`));
});

test.afterEach(async ({ page }) => {
  expect(problems.get(page) ?? []).toEqual([]);
});

/** 底栏的定位和 bottom。 */
async function barBox(page: Page, selector: string) {
  return page.locator(selector).evaluate((el) => ({
    position: getComputedStyle(el).position,
    bottom: el.getBoundingClientRect().bottom,
    viewport: window.innerHeight,
  }));
}

/** 让浏览器以一个新身份进来（cookie 和页面共用）。 */
async function signIn(page: Page, nickname: string) {
  const res = await page.request.post("/api/me", { data: { nickname }, headers: { Origin: ORIGIN } });
  expect(res.ok()).toBeTruthy();
}

test("首页：长列表滚动前后，Tab 栏都 fixed 贴在视口底", async ({ page }) => {
  await page.goto("/");
  // seed 的局有的在今天，跑的时间晚了可能已经不在列表里，所以只要求自己开的那 12 局都在。
  await expect.poll(() => page.locator(".gamecard").count()).toBeGreaterThanOrEqual(codes.length);

  const before = await barBox(page, ".tabbar");
  expect(before.position).toBe("fixed");
  expect(before.bottom).toBe(before.viewport);

  // 列表确实比一屏长，才谈得上「滚走」。
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeGreaterThan(667 * 2);
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);

  const after = await barBox(page, ".tabbar");
  expect(after.position).toBe("fixed");
  expect(after.bottom).toBe(after.viewport);

  // 最后一张卡片没有被 Tab 栏压住（#53）。
  const lastBottom = await page.locator(".gamecard").last().evaluate((el) => el.getBoundingClientRect().bottom);
  const barTop = await page.locator(".tabbar").evaluate((el) => el.getBoundingClientRect().top);
  expect(lastBottom).toBeLessThanOrEqual(barTop);
});

test("首页空状态没有虚线框（#56）", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("tab", { name: "BSD" }).click();
  const empty = page.locator(".empty-state", { hasText: "这几天还没有局" });
  await expect(empty).toBeVisible();
  const border = await empty.evaluate((el) => getComputedStyle(el).borderTopStyle);
  expect(border).toBe("none");
});

test("详情页 SEED01：空座位 64×64，有操作栏，没有 Tab 栏（#55）", async ({ page }) => {
  await page.goto("/g/SEED01");
  const seat = page.locator(".disc.empty").first();
  await expect(seat).toBeVisible();
  const box = await seat.boundingBox();
  expect(box?.width).toBe(64);
  expect(box?.height).toBe(64);
  // 虚线边框和零内边距：border-box 下 padding 小于内容盒时尺寸不变，只量宽高拦不住这两样。
  const style = await seat.evaluate((el) => {
    const cs = getComputedStyle(el);
    return { border: cs.borderTopStyle, padding: cs.paddingTop };
  });
  expect(style).toEqual({ border: "dashed", padding: "0px" });

  await expect(page.locator(".actionbar")).toBeVisible();
  const bar = await barBox(page, ".actionbar");
  expect(bar.position).toBe("fixed");
  expect(bar.bottom).toBe(bar.viewport);
  await expect(page.locator(".tabbar")).toHaveCount(0);
});

test("报名占座：座位更新成「我」，之后有「加入日历」", async ({ page }) => {
  await signIn(page, "E2E 路人");
  const code = codes[0];
  await page.goto(`/g/${code}`);
  await expect(page.locator(".disc.empty")).toHaveCount(3);

  await page.getByRole("button", { name: "报名占座" }).click();
  await expect(page.locator(".disc.me")).toHaveCount(1);
  await expect(page.locator(".disc.empty")).toHaveCount(2);

  // 报上名先弹报名成功弹层（B4–B6，#10）问要不要提醒；从那里点「加到日历」进日历弹层（#6）。
  const joined = page.getByTestId("joined-sheet");
  await expect(joined).toContainText("占上了");
  await expect(joined.getByRole("button", { name: "加到日历" })).toBeVisible();
  await joined.getByRole("button", { name: "加到日历" }).click();
  await expect(joined).toHaveCount(0);

  const sheet = page.getByTestId("calendar-sheet");
  await expect(sheet.getByTestId("calendar-ics")).toHaveAttribute("href", `/g/${code}/game.ics`);
  await sheet.getByRole("button", { name: "先不用" }).click();
  await expect(sheet).toHaveCount(0);
  await expect(page.getByRole("button", { name: "📅 加入日历" })).toBeVisible();
});

test("分享面板：375×560 下能滚到「取消」，WhatsApp 链接带的就是话术", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 560 });
  await page.goto(`/g/${codes[1]}`);
  await page.getByRole("button", { name: "⇪ 分享" }).click();

  const sheet = page.getByTestId("share-sheet");
  await expect(sheet).toBeVisible();
  // 海报是服务端渲染的：真的加载出来了，而不是占位图。
  await expect(sheet.locator("img.poster")).toHaveJSProperty("complete", true);
  expect(await sheet.locator("img.poster").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);

  const script = (await sheet.locator(".script p").textContent()) ?? "";
  expect(script).toContain(`/g/${codes[1]}`);
  const href = (await sheet.locator("a.outlet", { hasText: "WhatsApp" }).getAttribute("href")) ?? "";
  const wa = new URL(href);
  expect(wa.origin).toBe("https://wa.me");
  expect(wa.searchParams.get("text")).toBe(script);

  const cancel = sheet.getByRole("button", { name: "取消" });
  await sheet.evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await expect(cancel).toBeInViewport({ ratio: 1 });
  await cancel.click();
  await expect(sheet).toHaveCount(0);
});

// #75：局头的操作栏有 3–4 个按钮，会折成两行；页面底部留白要跟着变高，不然最后一段内容被压住。
// 这条断言原本挂着 `test.fail`（当时确实失败）；#75 修好之后去掉，让它真的跑起来。
test("局头视角：操作栏折成两行时，滚到底内容也不被压住（#75）", async ({ page }) => {
  await signIn(page, "E2E 局头丁");
  const startsAt = Math.floor(Date.now() / 1000) + 3 * 24 * 3600;
  const res = await page.request.post("/api/games", {
    data: { starts_at: startsAt, area: "Senayan", venue: "E2E 局头视角", kind: "classic", note: "备注写长一点，确保页面能滚。" },
    headers: { Origin: ORIGIN, "Idempotency-Key": `e2e-host-${Date.now()}` },
  });
  expect(res.ok()).toBeTruthy();
  const code = (await res.json()).game.short_code as string;

  await page.goto(`/g/${code}`);
  await expect(page.getByRole("button", { name: "取消这局" })).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));

  const last = await page.locator(".disclaimer").evaluate((el) => el.getBoundingClientRect().bottom);
  const barTop = await page.locator(".actionbar").evaluate((el) => el.getBoundingClientRect().top);
  expect(last).toBeLessThanOrEqual(barTop);
});

// 访客视角：只有「分享」「报名占座」两个按钮，一行；滚到底最后一段内容也必须在操作栏上面。
// 这条现在就该过——它拦的是「--bar-height 被改小 / 改成 0」这类回归（#77 复审）。
test("访客视角：滚到底内容不被操作栏压住", async ({ page }) => {
  await page.goto(`/g/${codes[2]}`);
  await expect(page.getByRole("button", { name: "报名占座" })).toBeVisible();
  // 页面确实比一屏长，才谈得上被压住。
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeGreaterThan(667);
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));

  const last = await page.locator(".disclaimer").evaluate((el) => el.getBoundingClientRect().bottom);
  const barTop = await page.locator(".actionbar").evaluate((el) => el.getBoundingClientRect().top);
  expect(last).toBeLessThanOrEqual(barTop);
});

// #94 follow-up：toast 不再按屏幕底部写死 96px，而是按**实际底栏高度**抬起来。
// 局头那条操作栏折成两行（155px）时，写死的 96px 会让 toast 盖住上面那一行。
test("局头视角：toast 抬在操作栏上方，不盖住按钮", async ({ page }) => {
  await signIn(page, "E2E 局头戊");
  const startsAt = Math.floor(Date.now() / 1000) + 3 * 24 * 3600;
  const res = await page.request.post("/api/games", {
    data: { starts_at: startsAt, area: "Kuningan", venue: "E2E toast 视角", kind: "classic" },
    headers: { Origin: ORIGIN, "Idempotency-Key": `e2e-toast-${Date.now()}` },
  });
  expect(res.ok()).toBeTruthy();
  const code = (await res.json()).game.short_code as string;

  await page.goto(`/g/${code}`);
  const bar = page.locator(".actionbar");
  await expect(bar).toBeVisible();
  // 局头这条栏确实是两行——不然这条断言没有意义。
  const rows = await bar.evaluate(
    (el) => new Set([...el.querySelectorAll(".btn")].map((b) => Math.round(b.getBoundingClientRect().top))).size,
  );
  expect(rows).toBeGreaterThan(1);

  // 分享面板里的「复制」会弹 toast（微信外是 a[download] 那条路，这里走复制）。
  await page.getByRole("button", { name: "⇪ 分享" }).click();
  await page.locator(".script button").click();

  const toast = page.locator(".toast");
  await expect(toast).toBeVisible();
  // 等它滑上来（sheetUp 动画 220ms）再量：动画中间它从下面 100% 的位置往上走，
  // 那时候量到的位置没有意义——用户看到的是停下来之后的位置。
  await toast.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)));
  const toastBottom = await toast.evaluate((el) => el.getBoundingClientRect().bottom);
  const barTop = await bar.evaluate((el) => el.getBoundingClientRect().top);
  expect(toastBottom).toBeLessThanOrEqual(barTop);
});

// #107：/me 以前没有 Tab 栏也没有返回键——iOS 主屏幕（独立窗口、没有浏览器返回键）上
// 点进「我的」就出不来了。Tab 栏现在由 App 层统一挂，tab 级页面都有。
test("我的页有 Tab 栏，点「牌局」回到首页", async ({ page }) => {
  await page.goto("/me");
  const tabbar = page.locator(".tabbar");
  await expect(tabbar).toBeVisible();
  // 当前高亮的是「我的」。
  await expect(tabbar.locator(".tab.on")).toHaveText(/我的/);

  await tabbar.getByRole("button", { name: /牌局/ }).click();

  await expect(page).toHaveURL(new RegExp(`${ORIGIN.replace("/", "\\/")}/?$`));
  await expect(page.locator("h1")).toContainText("随时掼掼蛋");
  await expect(page.locator(".tab.on")).toHaveText(/牌局/);
});

// 非 tab 页（详情页）不挂 Tab 栏——那里有自己的操作栏，两个底栏会打架。
test("详情页不显示 Tab 栏", async ({ page }) => {
  await page.goto(`/g/${codes[2]}`);
  await expect(page.locator(".actionbar")).toBeVisible();
  await expect(page.locator(".tabbar")).toHaveCount(0);
});

// #108：底部「牌友」能点到 F1。服务端的 /api/friends、/api/config 在 #109：
// 服务端还没有这两个接口时（现在未知的 /api/* 会落到 SPA 兜底，返回 200 的 HTML），这里用固定数据顶上；
// 接口上线之后自动走真接口（新身份没有牌友，看到的是空状态）。
test("底部「牌友」能进 F1，标题对，约 TA 面板能打开", async ({ page }) => {
  const fallback: Record<string, unknown> = {
    "/api/friends": {
      friends: [{ user: { id: "e2e-u1", nickname: "E2E 老王" }, together_count: 3, last_together_at: 1791986400, starred: true }],
    },
    "/api/config": { group: { wechat_id: "", whatsapp_group_url: "" } },
  };
  for (const path of Object.keys(fallback)) {
    await page.route(`**${path}`, async (route) => {
      const resp = await route.fetch();
      const json = (resp.headers()["content-type"] ?? "").includes("application/json");
      if (resp.status() === 404 || !json) return route.fulfill({ status: 200, json: fallback[path] });
      return route.fulfill({ response: resp });
    });
  }

  await signIn(page, "E2E 牌友");
  await page.goto("/");
  await page.locator(".tabbar .tab", { hasText: "牌友" }).click();
  await expect(page).toHaveURL(/\/friends$/);
  await expect(page).toHaveTitle("牌友 · 随时掼掼蛋");
  await expect(page.locator(".tabbar .tab.on")).toContainText("牌友");

  const bar = await barBox(page, ".tabbar");
  expect(bar.position).toBe("fixed");
  expect(bar.bottom).toBe(bar.viewport);

  const friend = page.getByTestId("friend").first();
  await expect(friend.or(page.getByTestId("friends-empty"))).toBeVisible();
  if (await friend.isVisible()) {
    await friend.getByRole("button", { name: "约 TA" }).click();
    const sheet = page.getByTestId("invite-sheet");
    await expect(sheet).toContainText("打一局");
    const href = (await sheet.locator("a.option").getAttribute("href")) ?? "";
    expect(new URL(href).origin).toBe("https://wa.me");
    await sheet.getByRole("button", { name: "取消" }).click();
    await expect(sheet).toHaveCount(0);
  }
});
