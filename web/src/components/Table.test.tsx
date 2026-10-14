import { render } from "preact";
import { afterEach, describe, expect, it } from "vitest";
import type { GameDetail } from "../api";
import { GameTable, initial } from "./Table";

function detail(seats: GameDetail["seats"]): GameDetail {
  return {
    game: {
      id: "g1",
      short_code: "8K2QXM",
      starts_at: 1791986400,
      area: "SCBD",
      venue: "Pacific Place",
      kind: "classic",
      status: seats.length === 4 ? "full" : "open",
      version: 1,
      capacity: 4,
    },
    host: { id: "u1", nickname: "老王" },
    seats,
    waitlist: [],
    missing: 4 - seats.length,
    me: { role: "none" },
  };
}

function draw(d: GameDetail, meID?: string): HTMLElement {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(<GameTable detail={d} meID={meID} />, container);
  return container;
}

describe("GameTable", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("座位圆点显示称呼首字，空座位是虚线", () => {
    const container = draw(
      detail([
        { seat_no: 1, user: { id: "u1", nickname: "老王" } },
        { seat_no: 3, user: { id: "u2", nickname: "阿明" } },
      ]),
    );

    expect(container.textContent).toContain("老");
    expect(container.textContent).toContain("阿");
    // 1 号在上、顺时针排（原型 POS = top/right/bottom/left）。
    expect(container.querySelectorAll(".seat.top .disc.host").length).toBe(1);
    // 空座位渲染成虚线圆点，没有文字。
    expect(container.querySelectorAll(".disc.empty").length).toBe(2);
  });

  // 皇冠只在「我本人是局头」时挂上：访客看到的局头座位没有皇冠，
  // 局头身份由名字胶囊「局头 · 老王」表达（原型 B1）。
  it("只有我本人是局头时才显示皇冠", () => {
    const seats = [{ seat_no: 1, user: { id: "u1", nickname: "老王" } }];
    expect(draw(detail(seats)).querySelectorAll(".crown").length).toBe(0);
    expect(draw(detail(seats), "u1").querySelectorAll(".crown").length).toBe(1);
  });

  it("我的座位单独标出来", () => {
    const container = draw(detail([{ seat_no: 1, user: { id: "u2", nickname: "我" } }]), "u2");
    expect(container.querySelectorAll(".disc.me").length).toBe(1);
  });

  it("牌桌中间显示 3/4 和「对家为搭档」", () => {
    const container = draw(
      detail([
        { seat_no: 1, user: { id: "u1", nickname: "老王" } },
        { seat_no: 2, user: { id: "u2", nickname: "阿明" } },
        { seat_no: 3, user: { id: "u3", nickname: "Lily" } },
      ]),
    );
    expect(container.textContent).toContain("3/4");
    expect(container.textContent).toContain("对家为搭档");
  });

  it("已取消的局把牌桌变灰", () => {
    const d = detail([{ seat_no: 1, user: { id: "u1", nickname: "老王" } }]);
    d.game.status = "cancelled";
    expect(draw(d).querySelector(".table.closed")).not.toBeNull();
  });

  it("空称呼兜底成问号", () => {
    expect(initial("")).toBe("?");
    expect(initial("阿明")).toBe("阿");
  });

  // 和服务端海报 internal/share 的 initial 同一张表（#60）。
  it("跳过开头的标点、符号和空白，拉丁字母转大写", () => {
    expect(initial("  lily")).toBe("L");
    expect(initial("「阿杰」")).toBe("阿");
    expect(initial("★小周")).toBe("小");
    expect(initial("😀Kevin")).toBe("K");
    expect(initial("❤️‍🔥王")).toBe("王");
    expect(initial("...")).toBe("?");
    expect(initial("😀🎉")).toBe("?");
    expect(initial("𠮷野")).toBe("𠮷");
    expect(initial("ßa")).toBe("ß");
    // \p{Cf}：零宽空格、从左到右标记、英格兰旗里的 tag 字符，都不该成为首字（#66 复审）。
    expect(initial("\u200B阿明")).toBe("阿");
    expect(initial("\u200E阿明")).toBe("阿");
    expect(initial("\u202ETom")).toBe("T");
    expect(initial("🏴\u{E0067}\u{E0062}\u{E0065}\u{E006E}\u{E0067}\u{E007F}Tom")).toBe("T");
    expect(initial("\u200B\u200C\uFEFF")).toBe("?");
  });
});
