import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Me } from "./Me";

function json(status: number, body: unknown) {
  return new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

type Call = { method: string; url: string; body: unknown };

/** 假后端：/api/me 带 wa_phone，其余按 handlers 返回。 */
function mount(opts: { wa?: string; handle?: (c: Call) => Response | undefined } = {}) {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const c = { method: init?.method ?? "GET", url, body: init?.body ? JSON.parse(String(init.body)) : null };
      calls.push(c);
      const r = opts.handle?.(c);
      if (r) return r;
      if (c.method === "GET" && url === "/api/me") {
        return json(200, { user: { id: "u1", nickname: "阿明" }, device_id: "d1", devices: [], wa_phone: opts.wa });
      }
      return json(404, { error: { code: "not_found", message: "x" } });
    }),
  );
  const toast = vi.fn();
  const container = document.createElement("div");
  document.body.appendChild(container);
  act(() =>
    render(
      <Me
        me={{ id: "u1", nickname: "阿明" }}
        requireIdentity={async () => null}
        toast={toast}
        onDevices={() => {}}
        onPrivacy={() => {}}
      />,
      container,
    ),
  );
  return { container, calls, toast };
}

/** 点按钮并让 Preact 把这次更新渲染出来。 */
async function click(c: Element, label: string) {
  const b = [...c.querySelectorAll("button")].find((x) => x.textContent?.includes(label)) as HTMLButtonElement;
  await act(async () => b.click());
}

function type(c: HTMLElement, selector: string, value: string) {
  const input = c.querySelector(selector) as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("我的 · 在别的设备上登录（G3）", () => {
  it("签发认领码并显示 6 位数字；复制的是带令牌的链接", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    const { container, calls, toast } = mount({
      handle: (c) =>
        c.method === "POST" && c.url === "/api/claim"
          ? json(200, { claim_token: "tok-1", code: "482731", expires_at: 1791980000 })
          : undefined,
    });
    await click(container, "在别的设备上登录");
    await vi.waitFor(() => expect(container.querySelector("[data-testid=claim-code]")).not.toBeNull());
    expect(container.querySelector(".digits")?.textContent).toBe("482731");
    expect(calls.filter((c) => c.url === "/api/claim")).toHaveLength(1);

    await click(container, "复制登录链接");
    await vi.waitFor(() => expect(toast).toHaveBeenCalled());
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/?claim=tok-1`);
  });

  it("签发太频繁（429）时说清楚，不显示空的认领码", async () => {
    const { container } = mount({
      handle: (c) => (c.url === "/api/claim" ? json(429, { error: { code: "rate_limited", message: "x" } }) : undefined),
    });
    await click(container, "在别的设备上登录");
    await vi.waitFor(() => expect(container.textContent).toContain("过一会儿再来"));
    expect(container.querySelector("[data-testid=claim-code]")).toBeNull();
  });
});

describe("我的 · 绑定 WhatsApp（G2）", () => {
  it("没绑定：显示「去绑定」；输入本地写法，规范成 E.164 再 PUT，回来显示脱敏号码", async () => {
    const { container, calls } = mount({
      handle: (c) => (c.method === "PUT" && c.url === "/api/me/wa" ? json(200, { wa_phone: c.body && (c.body as { phone: string }).phone }) : undefined),
    });
    await vi.waitFor(() => expect(calls.some((c) => c.url === "/api/me")).toBe(true));
    expect(container.querySelector("[data-testid=wa-status]")?.textContent).toBe("去绑定");

    await click(container, "绑定 WhatsApp");
    type(container, "[data-testid=wa-sheet] input", "0812-3456-7890");
    await act(async () => {});
    await click(container.querySelector("[data-testid=wa-sheet]")!, "绑定");
    await vi.waitFor(() => expect(container.querySelector("[data-testid=wa-sheet]")).toBeNull());

    const put = calls.find((c) => c.method === "PUT");
    expect(put?.body).toEqual({ phone: "+6281234567890" });
    expect(container.querySelector("[data-testid=wa-status]")?.textContent).toBe("+62 812****890");
  });

  it("号码认不出来：不发请求，提示写法", async () => {
    const { container, calls } = mount();
    await click(container, "绑定 WhatsApp");
    type(container, "[data-testid=wa-sheet] input", "12ab");
    await act(async () => {});
    await click(container.querySelector("[data-testid=wa-sheet]")!, "绑定");
    await act(async () => {});
    expect(container.textContent).toContain("号码不对");
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("服务端说号码不合法（400）：把它的话原样给用户", async () => {
    const { container } = mount({
      handle: (c) =>
        c.method === "PUT" ? json(400, { error: { code: "invalid_input", message: "不是有效的手机号" } }) : undefined,
    });
    await click(container, "绑定 WhatsApp");
    type(container, "[data-testid=wa-sheet] input", "0812 3456 7890");
    await act(async () => {});
    await click(container.querySelector("[data-testid=wa-sheet]")!, "绑定");
    await vi.waitFor(() => expect(container.textContent).toContain("不是有效的手机号"));
    expect(container.querySelector("[data-testid=wa-sheet]")).not.toBeNull();
  });

  it("已绑定：显示脱敏号码，可以解绑（DELETE），解绑后回到「去绑定」", async () => {
    const { container, calls, toast } = mount({
      wa: "+6281234567890",
      handle: (c) => (c.method === "DELETE" && c.url === "/api/me/wa" ? new Response(null, { status: 204 }) : undefined),
    });
    await vi.waitFor(() =>
      expect(container.querySelector("[data-testid=wa-status]")?.textContent).toBe("+62 812****890"),
    );
    await click(container, "绑定 WhatsApp");
    expect(container.querySelector("[data-testid=wa-current]")?.textContent).toContain("+62 812****890");
    // 页面上任何地方都不出现完整号码。
    expect(container.textContent).not.toContain("81234567890");

    await click(container, "解绑");
    await vi.waitFor(() => expect(container.querySelector("[data-testid=wa-sheet]")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/me/wa")).toBe(true);
    expect(toast).toHaveBeenCalledWith("已解绑");
    expect(container.querySelector("[data-testid=wa-status]")?.textContent).toBe("去绑定");
  });
});
