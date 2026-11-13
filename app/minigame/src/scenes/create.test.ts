// 开一局的「自定义」时间：在弹层里点胶囊 → 确认 → 提交的 starts_at 就是点的那个时间。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  (globalThis as any).__API_BASE__ = "http://test.invalid";
  (globalThis as any).wx = new Proxy({}, { get: () => () => undefined });
});

const api = vi.hoisted(() => ({ createGame: vi.fn() }));
vi.mock("../lib/api", async (orig) => ({ ...(await orig<typeof import("../lib/api")>()), createGame: api.createGame }));
vi.mock("../lib/session", () => ({ currentUser: () => ({ id: "me", nickname: "我" }) }));
vi.mock("./sheets", () => ({ friendPicker: vi.fn(), requireIdentity: vi.fn() }));

import { ApiError } from "../lib/api";
import { wibAt } from "../lib/fmt";
import { Create } from "./create";

// 2026-10-21 周三 20:10 WIB
const NOW = Date.UTC(2026, 9, 21, 13, 10);

/** 什么都不画的画布：文字宽度按字数估。 */
const ctx = new Proxy({} as any, {
  get: (t, k) => k in t ? t[k] : k === "measureText" ? (s: string) => ({ width: s.length * 14 }) : k === "createLinearGradient" ? () => ({ addColorStop() {} }) : () => {},
  set: (t, k, v) => { t[k] = v; return true; },
});

let hits: Record<string, (() => void) | null>;
let sheets: any[];
const app = {
  W: 375, H: 800,
  hit: (id: string, _x: number, _y: number, _w: number, _h: number, fn: (() => void) | null) => { hits[id] = fn; },
  isPressed: () => false,
  openSheet: (s: any) => sheets.push(s),
  closeSheet: (s: any) => { sheets = sheets.filter((x) => x !== s); },
  redraw: vi.fn(), toast: vi.fn(),
} as any;
const nav = { created: vi.fn() } as any;
const draw = (s: any) => { hits = {}; s.draw(ctx, 0, 375); };
const tap = (id: string) => { expect(hits[id], id).toBeTypeOf("function"); hits[id]!(); };

beforeEach(() => {
  vi.useFakeTimers(); vi.setSystemTime(NOW);
  hits = {}; sheets = [];
  api.createGame.mockReset().mockResolvedValue({ game: { short_code: "ABC123" } });
});
afterEach(() => { vi.useRealTimers(); });

describe("Create 自定义时间", () => {
  it("点「自定义」选后天 21:45 → 提交的 starts_at 就是它，幂等键换了", async () => {
    const c = new Create(app, nav) as any;
    c.venue = "PP";
    const key = c.idemKey;
    c.pickTime();
    const s = sheets[0];
    draw(s);
    tap("ct-day:2"); draw(s);
    tap(`ct-h:${21 - 9}`); draw(s);
    tap("ct-m:3"); draw(s);
    tap("ct-sb:0");
    const picked = wibAt(2, 21, 45);
    expect(sheets).toHaveLength(0);
    expect(c.startsAt).toBe(picked);
    expect(c.custom).toBe(picked);
    expect(c.idemKey).not.toBe(key);

    await c.submit();
    expect(api.createGame).toHaveBeenCalledTimes(1);
    expect(api.createGame.mock.calls[0][0].starts_at).toBe(picked);
    expect(nav.created).toHaveBeenCalledWith("ABC123");
  });

  it("「今晚 19:30」已经过了：默认选最近能开的一档；来不及的时间按钮不能点", () => {
    const c = new Create(app, nav) as any;
    c.pickTime();
    const s = sheets[0];
    draw(s);
    tap("ct-sb:0");
    expect(c.startsAt).toBe(wibAt(0, 20, 30));

    c.pickTime();
    const s2 = sheets[0];
    draw(s2);
    tap("ct-m:0"); draw(s2); // 20:00 已经过了
    expect(hits["ct-sb:0"]).toBeNull();
  });

  it("服务端拒了开局时间：错误文案显示在页面的错误行", async () => {
    api.createGame.mockRejectedValueOnce(new ApiError(400, "invalid_input", "开局时间要在 10 分钟后到 30 天之间"));
    const c = new Create(app, nav) as any;
    c.venue = "PP";
    await c.submit();
    expect(c.error).toBe("开局时间要在 10 分钟后到 30 天之间");
    expect(c.badField).toBe("");
  });
});
