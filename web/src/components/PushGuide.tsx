import { pushGuide } from "../guide";
import { isIOS, pushSupport } from "../push";
import { HomeScreenGuide } from "./HomeScreenGuide";
import { JumpOutGuide } from "./JumpOutGuide";

interface Props {
  signedIn: boolean;
  onCalendar: () => void;
  onClose: () => void;
}

/**
 * 「提醒我」点下去之后的引导屏入口（给 10b 的报名成功弹层用）：
 * 微信 / WhatsApp → H1，iOS Safari 标签页 → H2，其余环境不需要引导，返回 null。
 *
 * 调用方先用 `needsPushGuide()` 判断要不要渲染它，不需要就直接走订阅。
 */
export function PushGuide({ signedIn, onCalendar, onClose }: Props) {
  const guide = pushGuide(pushSupport(), isIOS());
  if (guide.kind === "jump-out") {
    return <JumpOutGuide app={guide.app} ios={guide.ios} signedIn={signedIn} onCalendar={onCalendar} onClose={onClose} />;
  }
  if (guide.kind === "home-screen") {
    return <HomeScreenGuide signedIn={signedIn} onCalendar={onCalendar} onClose={onClose} />;
  }
  return null;
}

export function needsPushGuide(): boolean {
  return pushGuide(pushSupport(), isIOS()).kind !== "none";
}
