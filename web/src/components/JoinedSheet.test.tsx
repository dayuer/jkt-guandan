import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GameDetail } from "../api";
import type { PushSupport, SubscribeResult } from "../push";

const push = vi.hoisted(() => ({
  support: { kind: "ready" } as PushSupport,
  permission: "default" as NotificationPermission | "unsupported",
  subscribed: false,
  result: { kind: "ok" } as SubscribeResult,
  subscribeCalls: 0,
}));

vi.mock("../push", () => ({
  pushSupport: () => push.support,
  notificationPermission: () => push.permission,
  hasPushSubscription: async () => push.subscribed,
  subscribeToPush: async () => {
    push.subscribeCalls++;
    return push.result;
  },
}));

const telemetry = vi.hoisted(() => ({ track: vi.fn() }));
vi.mock("../telemetry", () => telemetry);

import { JoinedSheet } from "./JoinedSheet";

function detail(seated: number): GameDetail {
  const names = ["陈姐", "小周", "阿明", "Kevin"];
  return {
    game: {
      id: "g1",
      short_code: "AB12",
      starts_at: 1791980000,
      area: "PIK",
      venue: "PIK Avenue · 茶室二楼",
      kind: "no-level",
      status: seated >= 4 ? "full" : "open",
      version: 3,
      capacity: 4,
    },
    host: { id: "u0", nickname: "陈姐" },
    seats: names.slice(0, seated).map((n, i) => ({ seat_no: i + 1, user: { id: `u${i}`, nickname: n } })),
    waitlist: [],
    missing: 4 - seated,
    me: { role: "seat", seat_no: 3 },
  };
}

const flush = async () => {
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
};

function mount(d: GameDetail, extra: { onGuide?: () => void } = {}) {
  const toast = vi.fn();
  const onCalendar = vi.fn();
  const onClose = vi.fn();
  const container = document.createElement("div");
  document.body.appendChild(container);
  act(() =>
    render(
      <JoinedSheet detail={d} meID="u2" toast={toast} onCalendar={onCalendar} onClose={onClose} {...extra} />,
      container,
    ),
  );
  return { container, toast, onCalendar, onClose };
}

const buttons = (c: HTMLElement) => [...c.querySelectorAll(".stack .btn")].map((b) => b.textContent);
const click = (c: HTMLElement, label: string) =>
  ([...c.querySelectorAll("button")].find((b) => b.textContent?.includes(label)) as HTMLButtonElement).click();

beforeEach(() => {
  push.support = { kind: "ready" };
  push.permission = "default";
  push.subscribed = false;
  push.result = { kind: "ok" };
  push.subscribeCalls = 0;
  telemetry.track.mockClear();
});

afterEach(() => {
  document.body.innerHTML = "";
});

describe("报名成功弹层（B4–B6）", () => {
  it("B4：占上了，3/4，「提醒我」→ 订阅成功就关掉并提示", async () => {
    const { container, toast, onClose } = mount(detail(3));
    expect(container.querySelector("h3")?.textContent).toBe("占上了！");
    expect(container.querySelector(".count")?.textContent).toBe("3/4");
    expect(container.querySelectorAll(".dots i.blank")).toHaveLength(1);
    expect(container.querySelector(".dots i.host")?.textContent).toBe("陈");
    expect(container.querySelector(".dots i.me")?.textContent).toBe("阿");
    expect(buttons(container)).toEqual(["提醒我", "加到日历"]);

    click(container, "提醒我");
    await flush();
    expect(push.subscribeCalls).toBe(1);
    expect(toast).toHaveBeenCalledWith("好，开局前 1 小时提醒你");
    // 漏斗：问提醒、开成提醒（#117）。
    expect(telemetry.track).toHaveBeenCalledWith("push_prompt", { game: "AB12" });
    expect(telemetry.track).toHaveBeenCalledWith("push_ok", { game: "AB12" });
    expect(onClose).toHaveBeenCalled();
  });

  it("B6：刚好坐满，标题换成「成局了！4 人到齐」", () => {
    const { container } = mount(detail(4));
    expect(container.querySelector("h3")?.textContent).toBe("成局了！4 人到齐");
    expect(container.querySelectorAll(".dots i.blank")).toHaveLength(0);
    expect(buttons(container)).toEqual(["提醒我", "加到日历"]);
  });

  it("B5：这台设备不支持推送，一进来就是「加到日历」+「用 WhatsApp 提醒我」", () => {
    push.support = { kind: "unsupported" };
    const { container } = mount(detail(3));
    expect(container.textContent).toContain("这台手机收不到网页提醒，换个方式");
    expect(buttons(container)).toEqual(["加到日历", "用 WhatsApp 提醒我"]);
    const wa = container.querySelector("a.btn") as HTMLAnchorElement;
    expect(wa.href).toMatch(/^https:\/\/wa\.me\/\?text=/);
    expect(decodeURIComponent(wa.href)).toContain("/g/AB12");
  });

  it("通知权限之前被拒过：不再弹系统框，直接换方式", () => {
    push.permission = "denied";
    const { container } = mount(detail(3));
    expect(buttons(container)).toEqual(["加到日历", "用 WhatsApp 提醒我"]);
  });

  it("用户在系统框里点了「不允许」：换成 B5", async () => {
    push.result = { kind: "denied" };
    const { container, onClose } = mount(detail(3));
    click(container, "提醒我");
    await flush();
    expect(onClose).not.toHaveBeenCalled();
    expect(container.textContent).toContain("设置 › 通知");
    expect(buttons(container)).toEqual(["加到日历", "用 WhatsApp 提醒我"]);
  });

  it("订阅失败（比如 Service Worker 一直没就绪）：换成 B5，不会一直转圈", async () => {
    push.result = { kind: "failed", reason: "Service Worker 没有就绪" };
    const { container } = mount(detail(3));
    click(container, "提醒我");
    await flush();
    expect(container.textContent).toContain("这台手机没开成提醒");
  });

  it("微信里点「提醒我」：交给引导屏（#70），不直接订阅", async () => {
    push.support = { kind: "in-app-browser", app: "wechat" };
    const onGuide = vi.fn();
    const { container } = mount(detail(3), { onGuide });
    expect(buttons(container)).toEqual(["提醒我", "加到日历"]);
    click(container, "提醒我");
    await flush();
    expect(onGuide).toHaveBeenCalled();
    expect(push.subscribeCalls).toBe(0);
    expect(telemetry.track).toHaveBeenCalledWith("jumpout", { game: "AB12" });
  });

  it("没有引导屏可用时：iOS Safari 标签页退回 B5 并说明原因", async () => {
    push.support = { kind: "ios-needs-home" };
    const { container } = mount(detail(3));
    click(container, "提醒我");
    await flush();
    expect(container.textContent).toContain("iPhone 要先加到主屏幕");
    expect(push.subscribeCalls).toBe(0);
  });

  it("没有引导屏可用时：微信里说「微信」，认不准的内置浏览器不乱点名", async () => {
    push.support = { kind: "in-app-browser", app: "wechat" };
    const { container } = mount(detail(3));
    click(container, "提醒我");
    await flush();
    expect(container.textContent).toContain("微信里收不到网页提醒");

    document.body.innerHTML = "";
    push.support = { kind: "in-app-browser", app: "instagram" };
    const other = mount(detail(3)).container;
    click(other, "提醒我");
    await flush();
    expect(other.textContent).toContain("聊天软件里收不到网页提醒");
    expect(other.textContent).not.toContain("WhatsApp 里");
  });

  it("这台设备已经订阅过：显示「提醒已经开着」", async () => {
    push.permission = "granted";
    push.subscribed = true;
    const { container } = mount(detail(3));
    await flush();
    expect(container.textContent).toContain("提醒已经开着");
    expect(buttons(container)).toEqual(["好的，到时见", "加到日历"]);
  });

  it("每一种都能「先不用」；「加到日历」交给日历弹层", () => {
    const { container, onClose, onCalendar } = mount(detail(3));
    click(container, "加到日历");
    expect(onCalendar).toHaveBeenCalled();
    click(container, "先不用");
    expect(onClose).toHaveBeenCalled();
  });
});
