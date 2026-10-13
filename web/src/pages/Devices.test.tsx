import { render } from "preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Device, UserRef } from "../api";
import { Devices } from "./Devices";

/** G4 · 登录的设备（§10.3）：看见陌生设备并踢掉它，是免注册身份唯一的补救手段。 */

function respond(status: number, body: unknown = null) {
  const text = body === null ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

const me: UserRef = { id: "u1", nickname: "阿明" };

/** 我自己的这台 + 另一台（微信里的）。 */
const mine: Device = { id: "d1", user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)", last_seen: 0 };
const other: Device = { id: "d2", user_agent: "Mozilla/5.0 MicroMessenger/8.0", last_seen: 0 };

function setup(devices: Device[], currentID: string) {
  const calls: string[] = [];
  let state = { devices, currentID };

  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      calls.push(`${method} ${url}`);
      if (method === "GET") return respond(200, { user: me, device_id: state.currentID, devices: state.devices });
      if (method === "DELETE") {
        const id = decodeURIComponent(url.split("/").pop() ?? "");
        state = { ...state, devices: state.devices.filter((d) => d.id !== id) };
        return respond(204);
      }
      return respond(404);
    }),
  );

  const container = document.createElement("div");
  document.body.appendChild(container);
  const toasts: string[] = [];
  let signedOut = 0;
  render(
    <Devices me={me} toast={(t) => toasts.push(t)} onBack={() => {}} onSignedOut={() => signedOut++} />,
    container,
  );
  return { container, calls, toasts, getSignedOut: () => signedOut };
}

/** 界面上的时间用「刚刚」之类的相对说法，用当前的时钟造一个最近的时间。 */
function now() {
  return Math.floor(Date.now() / 1000);
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("登录的设备（§10.3）", () => {
  it("列出设备，本机标出来，本机不能退", async () => {
    const { container } = setup(
      [{ ...mine, last_seen: now() }, { ...other, last_seen: now() - 7200 }],
      "d1",
    );

    await vi.waitFor(() => expect(container.textContent).toContain("微信内置浏览器"));
    expect(container.textContent).toContain("本机");
    expect(container.textContent).toContain("iPhone · Safari");
    expect(container.textContent).toContain("2 小时前");

    // 本机那一行没有「退出」，别的那台有。
    const rows = [...container.querySelectorAll(".li")];
    expect(rows[0].textContent).not.toContain("退出");
    expect(rows[1].textContent).toContain("退出");
  });

  it("退出其他设备：把非本机的都退掉", async () => {
    const { container, calls, toasts } = setup(
      [{ ...mine, last_seen: now() }, { ...other, last_seen: now() }],
      "d1",
    );
    await vi.waitFor(() => expect(container.textContent).toContain("退出其他设备"));

    (container.querySelector(".pad .btn") as HTMLButtonElement).click();

    await vi.waitFor(() => expect(toasts.join()).toContain("退掉了 1 台设备"));
    expect(calls).toEqual([
      "GET /api/me", // 进页面拉列表
      "GET /api/me", // signOutOtherDevices 自己再问一遍：要排除掉当前这台
      "DELETE /api/me/devices/d2",
      "GET /api/me", // 退完再拉一次列表
    ]);
    // 退完之后列表里只剩本机，按钮也就不显示了。
    await vi.waitFor(() => expect(container.querySelector(".pad .btn")).toBeNull());
  });

  it("只有本机时不显示「退出其他设备」", async () => {
    const { container } = setup([{ ...mine, last_seen: now() }], "d1");
    await vi.waitFor(() => expect(container.textContent).toContain("本机"));
    expect(container.querySelector(".pad .btn")).toBeNull();
  });

  it("cookie 已经失效（401）时，直接当作已经退出", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => respond(401, { error: { code: "unauthorized", message: "" } })));

    const container = document.createElement("div");
    document.body.appendChild(container);
    let signedOut = 0;
    render(<Devices me={null} toast={() => {}} onBack={() => {}} onSignedOut={() => signedOut++} />, container);

    await vi.waitFor(() => expect(signedOut).toBe(1));
  });
});
