/**
 * F2「约 TA 打一局」的话术（#108）。
 *
 * 不带具体牌局：这是「最近有空吗」的私聊开场白，带上首页链接，对方点开就能看到有哪些局。
 * 不承诺站内通知对方——发不发、怎么发，交给用户自己的聊天软件。
 */
export function inviteText(nickname: string, origin: string = window.location.origin): string {
  return `${nickname}，最近有空打一局掼蛋吗？这里能看到雅加达最近的局 👉 ${origin}/`;
}
