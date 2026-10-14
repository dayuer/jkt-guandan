import type { Friend } from "../api";
import { inviteText } from "../invite";
import { copyText, whatsappURL } from "../share";

interface Props {
  friend: Friend;
  toast: (text: string) => void;
  /** 「生成海报」：先去开一局，开好之后的分享面板里就有海报（#54）。 */
  onPoster: () => void;
  onClose: () => void;
}

/**
 * F2 · 约 TA 打一局。三个出口：复制话术、WhatsApp、生成海报。
 *
 * 牌友列表里没有手机号（隐私），所以「WhatsApp 发给 TA」用通用的 wa.me/?text=，由用户自己选联系人。
 */
export function InviteSheet({ friend, toast, onPoster, onClose }: Props) {
  const text = inviteText(friend.user.nickname);

  const copy = async () => {
    if (await copyText(text)) {
      toast("话术已复制，发给 TA 吧");
      onClose();
    } else {
      toast("复制不了，换 WhatsApp 试试");
    }
  };

  return (
    <>
      <div class="scrim" onClick={onClose} />
      <div class="sheet invite" role="dialog" aria-modal="true" data-testid="invite-sheet">
        <div class="grab" />
        <h3>约 {friend.user.nickname} 打一局</h3>
        <div class="options">
          <button class="option" onClick={() => void copy()}>
            <span class="ico" aria-hidden="true">
              ⧉
            </span>
            <span class="tx">
              复制邀请话术
              <small>{friend.user.nickname}，最近有空打一局掼蛋吗？</small>
            </span>
            <span class="chev" aria-hidden="true">
              ›
            </span>
          </button>
          <a class="option" href={whatsappURL(text)} target="_blank" rel="noopener noreferrer" onClick={onClose}>
            <span class="ico wa" aria-hidden="true">
              W
            </span>
            <span class="tx">
              WhatsApp 发给 TA
              <small>打开 WhatsApp，话术已填好</small>
            </span>
            <span class="chev" aria-hidden="true">
              ›
            </span>
          </a>
          <button class="option" onClick={onPoster}>
            <span class="ico heart" aria-hidden="true">
              ♥
            </span>
            <span class="tx">
              生成海报
              <small>先定时间地点，海报马上出</small>
            </span>
            <span class="chev" aria-hidden="true">
              ›
            </span>
          </button>
        </div>
        <button class="btn soft cancel" onClick={onClose}>
          取消
        </button>
      </div>
    </>
  );
}
