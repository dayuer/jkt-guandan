import { describe, expect, it } from "vitest";
import { calendarLinks, calendarOption, isWeChat } from "./calendar";

const WECHAT_IOS =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.47(0x18002f2c) NetType/WIFI Language/zh_CN";
const WECHAT_ANDROID =
  "Mozilla/5.0 (Linux; Android 13; V2244A) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/111.0.5563.116 Mobile Safari/537.36 XWEB/1110005 MMWEBSDK/20231201 MicroMessenger/8.0.45.2521(0x28002D3B) WeChat/arm64";
const SAFARI =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1";

describe("calendar", () => {
  it("识别微信内置浏览器", () => {
    expect(isWeChat(WECHAT_IOS)).toBe(true);
    expect(isWeChat(WECHAT_ANDROID)).toBe(true);
    expect(isWeChat(SAFARI)).toBe(false);
  });

  it("浏览器里给出 .ics 和 Google Calendar 两个链接", () => {
    expect(calendarOption("SEED01", SAFARI)).toEqual({
      kind: "links",
      ics: "/g/SEED01/game.ics",
      google: "/g/SEED01/google",
    });
  });

  it("微信里不给下载链接，引导跳出", () => {
    expect(calendarOption("SEED01", WECHAT_IOS)).toEqual({ kind: "leave-wechat" });
  });

  it("短码会被编码，拼不出别的路径", () => {
    expect(calendarLinks("../x?y").ics).toBe("/g/..%2Fx%3Fy/game.ics");
  });
});
