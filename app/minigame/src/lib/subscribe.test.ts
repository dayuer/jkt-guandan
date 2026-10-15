import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const reported: string[][] = [];
vi.mock("./api", () => ({
  getPublicConfig: async () => ({ group: { wechat_id: "", whatsapp_group_url: "" }, wx_subscribe: { full: "TF", remind: "TR" } }),
  reportWxSubscribe: async (ids: string[]) => { reported.push(ids); return { added: ids.length }; },
}));

import { acceptedOf, askSubscribe, loadSubscribeTemplates, remindOn, reportAccepted, setSubscribeTemplates } from "./subscribe";

describe("订阅消息（#137）", () => {
  let calls: any[];
  beforeEach(() => {
    calls = [];
    reported.length = 0;
    (globalThis as any).wx = {
      requestSubscribeMessage: (o: any) => { calls.push(o); o.success({ errMsg: "requestSubscribeMessage:ok", TR: "accept", TF: "reject" }); },
    };
  });
  afterEach(() => { delete (globalThis as any).wx; });

  it("模板从 /api/config 拿，点按时同步调 wx.requestSubscribeMessage，返回同意了的", async () => {
    await loadSubscribeTemplates();
    const p = askSubscribe();
    expect(calls).toHaveLength(1); // 同步调用：还没 await 就已经调了
    expect(calls[0].tmplIds).toEqual(["TR", "TF"]);
    const ids = await p;
    expect(ids).toEqual(["TR"]);
    expect(remindOn(ids)).toBe(true);
    expect(remindOn(["TF"])).toBe(false);
    await reportAccepted(ids);
    await reportAccepted([]);
    expect(reported).toEqual([["TR"]]);
  });

  it("没配模板、基础库不支持、调用失败：不问，返回空", async () => {
    setSubscribeTemplates({});
    expect(await askSubscribe()).toEqual([]);
    expect(calls).toHaveLength(0);

    setSubscribeTemplates({ remind: "TR" });
    delete (globalThis as any).wx.requestSubscribeMessage;
    expect(await askSubscribe()).toEqual([]);

    (globalThis as any).wx.requestSubscribeMessage = (o: any) => o.fail({ errCode: 20004 });
    expect(await askSubscribe()).toEqual([]);
    (globalThis as any).wx.requestSubscribeMessage = () => { throw new Error("boom"); };
    expect(await askSubscribe()).toEqual([]);
  });

  it("acceptedOf 只认 accept（ban / filter / reject 都不算）", () => {
    expect(acceptedOf({ A: "accept", B: "ban", C: "filter", D: "reject" }, ["A", "B", "C", "D", "E"])).toEqual(["A"]);
    expect(acceptedOf(null, ["A"])).toEqual([]);
  });
});
