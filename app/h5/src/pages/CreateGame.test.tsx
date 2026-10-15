import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CreateGame, draftTimeLabel } from "./CreateGame";
import { installFakeSocket } from "../fakeSocket";

function respond(status: number, body: unknown) {
  const text = JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => text,
    json: async () => body,
  } as unknown as Response;
}

/** 起一个总是失败的接口，把每次请求带的幂等键记下来。 */
function setup(keys: (string | undefined)[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init: RequestInit) => {
      keys.push((init.headers as Record<string, string>)["Idempotency-Key"]);
      // 用 500：不触发 503 的自动重试，一次提交就是一次请求（503 的重试在 api.test.ts 里测）。
      return respond(500, { error: { code: "boom", message: "失败" } });
    }),
  );

  const container = document.createElement("div");
  document.body.appendChild(container);
  render(
    <CreateGame
      me={{ id: "u1", nickname: "老王" }}
      requireIdentity={async () => null}
      toast={() => {}}
      onBack={() => {}}
      onCreated={() => {}}
    />,
    container,
  );
  return container;
}

/**
 * 改场地。包在 act 里，让改内容触发的 effect（换新幂等键）当场跑完：Preact 的 useEffect 排在下一帧之后，
 * 光等按钮可点不够，机器一忙 effect 就落到提交之后，键换早换晚都会让用例时好时坏（#167）。
 */
function fill(container: HTMLElement, value: string) {
  const input = container.querySelector("#venue") as HTMLInputElement;
  act(() => {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function clickSubmit(container: HTMLElement) {
  (container.querySelector(".actionbar .btn") as HTMLButtonElement).click();
}

beforeEach(() => {
  installFakeSocket();
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("开局表单的幂等键（§11.4）", () => {
  // 512 那次其实可能已经在服务端成功了，只是响应没回来。用户再点一次时，
  // 如果换了新键，服务端就会再开一局。
  it("失败后再点一次，两次用的是同一个键", async () => {
    const keys: (string | undefined)[] = [];
    const container = setup(keys);
    fill(container, "Pacific Place");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(1));
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(2));

    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBe(keys[0]);
  });

  it("表单内容变了就换一个新键（那是另一次提交了）", async () => {
    const keys: (string | undefined)[] = [];
    const container = setup(keys);
    fill(container, "Pacific Place");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(1));

    fill(container, "PIK Avenue");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));
    clickSubmit(container);
    await vi.waitFor(() => expect(keys.length).toBe(2));

    expect(keys[1]).not.toBe(keys[0]);
  });
});

describe("敏感词（§14、原型 X1）", () => {
  /** 接口固定返回 422 sensitive_word，字段由用例指定。 */
  function setupSensitive(field: string) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        respond(422, {
          error: { code: "sensitive_word", message: "请勿涉及金钱：这里只约纯娱乐局", field },
        }),
      ),
    );

    const container = document.createElement("div");
    document.body.appendChild(container);
    render(
      <CreateGame
        me={{ id: "u1", nickname: "老王" }}
        requireIdentity={async () => null}
        toast={() => {}}
        onBack={() => {}}
        onCreated={() => {}}
      />,
      container,
    );
    return container;
  }

  function type(container: HTMLElement, id: string, value: string) {
    const el = container.querySelector(`#${id}`) as HTMLInputElement | HTMLTextAreaElement;
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  }

  it("备注命中时，提示在备注下面，而且不清空已经写好的内容", async () => {
    const container = setupSensitive("note");
    type(container, "venue", "Pacific Place");
    type(container, "note", "一把 50 块钱，输了请宵夜");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(container.querySelector(".errmsg")).not.toBeNull());

    expect(container.querySelector(".errmsg")?.textContent).toContain("请勿涉及金钱");
    // 提示挂在备注那个字段上：备注变红，地点没变。
    const note = container.querySelector("#note") as HTMLTextAreaElement;
    const venue = container.querySelector("#venue") as HTMLInputElement;
    expect(note.className).toContain("err");
    expect(venue.className).not.toContain("err");
    // 用户写的内容还在，改一改就能重新提交。
    expect(note.value).toBe("一把 50 块钱，输了请宵夜");
    // 提示只说一遍：上面那个笼统的错误行不该同时出现。
    expect(container.querySelectorAll(".error").length).toBe(0);
  });

  it("地点命中时，提示在地点下面", async () => {
    const container = setupSensitive("venue");
    type(container, "venue", "筹码酒吧");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(container.querySelector(".errmsg")).not.toBeNull());

    expect((container.querySelector("#venue") as HTMLInputElement).className).toContain("err");
    expect((container.querySelector("#note") as HTMLTextAreaElement).className).not.toContain("err");
  });

  it("用户开始改那个字段，提示就收起来", async () => {
    const container = setupSensitive("note");
    type(container, "venue", "Pacific Place");
    type(container, "note", "小赌怡情");
    await vi.waitFor(() => expect((container.querySelector(".actionbar .btn") as HTMLButtonElement).disabled).toBe(false));

    clickSubmit(container);
    await vi.waitFor(() => expect(container.querySelector(".errmsg")).not.toBeNull());

    type(container, "note", "新手局，随便打打");
    await vi.waitFor(() => expect(container.querySelector(".errmsg")).toBeNull());
    expect((container.querySelector("#note") as HTMLTextAreaElement).className).not.toContain("err");
  });
});

describe("从 07 蛋蛋退回来（#11）", () => {
  it("用草稿预填，并提示用户刚才说的话和缺什么", () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const startsAt = 1791980000;
    render(
      <CreateGame
        me={{ id: "u1", nickname: "老王" }}
        requireIdentity={async () => null}
        toast={() => {}}
        onBack={() => {}}
        onCreated={() => {}}
        prefill={{
          draft: { starts_at: startsAt, area: "PIK", kind: "teaching", note: "新手欢迎" },
          problem: "还没说在哪儿打",
          said: "周四晚上 PIK 带新手",
        }}
      />,
      container,
    );

    expect(container.querySelector(".prefill-note")?.textContent).toContain("周四晚上 PIK 带新手");
    expect(container.querySelector(".prefill-note .problem")?.textContent).toBe("还没说在哪儿打");
    expect((container.querySelector("#area") as HTMLSelectElement).value).toBe("PIK");
    expect((container.querySelector("#note") as HTMLTextAreaElement).value).toBe("新手欢迎");
    const on = [...container.querySelectorAll(".choices button.on")].map((b) => b.textContent);
    expect(on).toContain("带新手");
    // 蛋蛋给的时间不在三个常用时间里，加成第一个选项并选中。
    expect(container.querySelector(".choices button")?.classList.contains("on")).toBe(true);
  });
});

describe("草稿时间的写法和常用时间对齐（#69 复审建议 2）", () => {
  // 2026-10-14（周三）12:00 WIB
  const now = Math.floor(Date.UTC(2026, 9, 14, 5, 0) / 1000);
  const wib = (day: number, h: number, m = 0) => Math.floor(Date.UTC(2026, 9, day, h - 7, m) / 1000);

  it("今天 / 明天带前缀，和「明晚 周四 19:30」同一种写法", () => {
    expect(draftTimeLabel(wib(14, 21), now)).toBe("今天 周三 21:00");
    expect(draftTimeLabel(wib(15, 20), now)).toBe("明天 周四 20:00");
  });

  it("更远的日子写日期", () => {
    expect(draftTimeLabel(wib(17, 19, 30), now)).toBe("10/17 周六 19:30");
  });
});
