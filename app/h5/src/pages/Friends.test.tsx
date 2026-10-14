import { render } from "preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Friend } from "../api";
import { installFakeSocket } from "../fakeSocket";
import { Friends } from "./Friends";

function respond(status: number, body: unknown) {
  const text = body === undefined ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

const friend = (id: string, nickname: string, together_count: number, starred: boolean, last = 1791986400): Friend => ({
  user: { id, nickname },
  together_count,
  last_together_at: last,
  starred,
});

// 故意不按顺序给：本地也要能排（星标切换之后要重排）。
const LIST: Friend[] = [friend("u2", "Lily", 8, false), friend("u3", "老王", 12, true), friend("u4", "阿杰", 6, false)];

interface Setup {
  friends?: Response;
  config?: Response;
  follow?: Response;
}

function setup(opts: Setup = {}) {
  const calls: { method: string; url: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      calls.push({ method, url });
      if (url === "/api/friends") return opts.friends ?? respond(200, { friends: LIST });
      if (url === "/api/config") return opts.config ?? respond(200, { group: { wechat_id: "", whatsapp_group_url: "" } });
      if (url.startsWith("/api/follows/")) return opts.follow ?? respond(204, undefined);
      return respond(404, { error: { code: "not_found", message: "" } });
    }),
  );
  vi.stubGlobal("navigator", { userAgent: "Mozilla/5.0 (iPhone)", clipboard: { writeText: vi.fn(async () => {}) } });

  const toasts: string[] = [];
  const nav = { games: 0, newGame: 0 };
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(
    <Friends
      toast={(t) => toasts.push(t)}
      onGames={() => nav.games++}
      onNewGame={() => nav.newGame++}
    />,
    container,
  );
  return { container, calls, toasts, nav };
}

const names = (c: HTMLElement) => [...c.querySelectorAll("[data-testid=friend] .tx b")].map((b) => b.textContent);
const button = (c: HTMLElement, text: string) =>
  [...c.querySelectorAll("button, a")].find((b) => b.textContent?.includes(text)) as HTMLElement | undefined;

beforeEach(() => installFakeSocket());
afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("09 牌友（F1）", () => {
  it("局数相同时按最近一次排", async () => {
    const { container } = setup({
      friends: respond(200, {
        friends: [friend("a", "早", 3, false, 1000), friend("b", "近", 3, false, 2000)],
      }),
    });
    await vi.waitFor(() => expect(names(container)).toEqual(["近", "早"]));
  });

  it("星标的排在前面，再按一起打过的局数；显示「一起打过 N 局」", async () => {
    const { container } = setup();
    await vi.waitFor(() => expect(names(container)).toEqual(["老王", "Lily", "阿杰"]));
    expect(container.textContent).toContain("一起打过 12 局");
    expect(container.querySelector("[data-testid=friend] .avatar.starred")?.textContent).toBe("老");
  });

  it("点星标：立刻排到前面，并 PUT /api/follows/{id}；再点 DELETE", async () => {
    const { container, calls } = setup();
    await vi.waitFor(() => expect(names(container)).toHaveLength(3));

    (container.querySelector('[aria-label="星标 阿杰"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(names(container)).toEqual(["老王", "阿杰", "Lily"]));
    expect(calls).toContainEqual({ method: "PUT", url: "/api/follows/u4" });

    (container.querySelector('[aria-label="取消星标 阿杰"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(calls).toContainEqual({ method: "DELETE", url: "/api/follows/u4" }));
    await vi.waitFor(() => expect(names(container)).toEqual(["老王", "Lily", "阿杰"]));
  });

  it("星标失败就改回来，并提示", async () => {
    const { container, toasts } = setup({ follow: respond(404, { error: { code: "not_friend", message: "" } }) });
    await vi.waitFor(() => expect(names(container)).toHaveLength(3));
    (container.querySelector('[aria-label="星标 Lily"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(toasts).toContain("没标上，稍后再试"));
    expect(names(container)).toEqual(["老王", "Lily", "阿杰"]);
    expect(container.querySelector('[aria-label="星标 Lily"]')).not.toBeNull();
  });

  it("还没和谁打过局：空状态，点「去找一局」回首页", async () => {
    const { container, nav } = setup({ friends: respond(200, { friends: [] }) });
    await vi.waitFor(() => expect(container.querySelector("[data-testid=friends-empty]")).not.toBeNull());
    button(container, "去找一局")!.click();
    expect(nav.games).toBe(1);
  });

  it("还没有身份（401）也是空状态，不报错", async () => {
    const { container } = setup({ friends: respond(401, { error: { code: "unauthorized", message: "" } }) });
    await vi.waitFor(() => expect(container.querySelector("[data-testid=friends-empty]")).not.toBeNull());
    expect(container.querySelector(".error")).toBeNull();
  });

  it("群信息没配：不显示横幅", async () => {
    const { container } = setup();
    await vi.waitFor(() => expect(names(container)).toHaveLength(3));
    expect(container.querySelector("[data-testid=group-banner]")).toBeNull();
  });

  it("群信息配了：横幅里有「复制微信号」和「WhatsApp 群」；只配一个就只显示一个", async () => {
    const { container, toasts } = setup({
      config: respond(200, { group: { wechat_id: "guandan_jkt", whatsapp_group_url: "https://chat.whatsapp.com/abc" } }),
    });
    await vi.waitFor(() => expect(container.querySelector("[data-testid=group-banner]")).not.toBeNull());
    expect(button(container, "WhatsApp 群")?.getAttribute("href")).toBe("https://chat.whatsapp.com/abc");
    button(container, "复制微信号")!.click();
    await vi.waitFor(() => expect(toasts.join()).toContain("guandan_jkt"));
    document.body.innerHTML = "";

    const only = setup({ config: respond(200, { group: { wechat_id: "guandan_jkt", whatsapp_group_url: "" } }) });
    await vi.waitFor(() => expect(only.container.querySelector("[data-testid=group-banner]")).not.toBeNull());
    expect(button(only.container, "WhatsApp 群")).toBeUndefined();
    expect(button(only.container, "复制微信号")).toBeTruthy();
  });
});

describe("约 TA（F2）", () => {
  async function openInvite() {
    const s = setup();
    await vi.waitFor(() => expect(names(s.container)).toHaveLength(3));
    const lily = [...s.container.querySelectorAll("[data-testid=friend]")].find((el) => el.textContent?.includes("Lily"))!;
    (lily.querySelector(".btn.primary") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(s.container.querySelector("[data-testid=invite-sheet]")).not.toBeNull());
    return s;
  }

  it("标题是「约 Lily 打一局」，三个出口都在", async () => {
    const { container } = await openInvite();
    const sheet = container.querySelector("[data-testid=invite-sheet]")!;
    expect(sheet.querySelector("h3")?.textContent).toBe("约 Lily 打一局");
    expect(sheet.textContent).toContain("复制邀请话术");
    expect(sheet.textContent).toContain("WhatsApp 发给 TA");
    expect(sheet.textContent).toContain("生成海报");
  });

  it("WhatsApp 链接带着话术", async () => {
    const { container } = await openInvite();
    const href = button(container, "WhatsApp 发给 TA")!.getAttribute("href")!;
    const text = new URL(href).searchParams.get("text")!;
    expect(href.startsWith("https://wa.me/?text=")).toBe(true);
    expect(text).toContain("Lily，最近有空打一局掼蛋吗？");
    expect(text).toContain(`${window.location.origin}/`);
  });

  it("复制话术：写进剪贴板、提示并关掉面板", async () => {
    const { container, toasts } = await openInvite();
    button(container, "复制邀请话术")!.click();
    await vi.waitFor(() => expect(toasts).toContain("话术已复制，发给 TA 吧"));
    const write = (navigator.clipboard.writeText as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(write).toContain("Lily，最近有空打一局掼蛋吗？");
    await vi.waitFor(() => expect(container.querySelector("[data-testid=invite-sheet]")).toBeNull());
  });

  it("生成海报：去开局表单", async () => {
    const { container, nav } = await openInvite();
    button(container, "生成海报")!.click();
    expect(nav.newGame).toBe(1);
    await vi.waitFor(() => expect(container.querySelector("[data-testid=invite-sheet]")).toBeNull());
  });

  it("取消关掉面板", async () => {
    const { container } = await openInvite();
    (container.querySelector(".sheet.invite .cancel") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(container.querySelector("[data-testid=invite-sheet]")).toBeNull());
  });
});
