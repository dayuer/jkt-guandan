/**
 * F2「约 TA 打一局」的话术（#108）。
 *
 * 不带具体牌局：这是「最近有空吗」的私聊开场白，带上首页链接，对方点开就能看到有哪些局。
 * 不承诺站内通知对方——发不发、怎么发，交给用户自己的聊天软件。
 *
 * #253 P6.75 起 `origin` 由调用方**必填**：原来默认读 `window.location.origin`，
 * 而小游戏那边 `window` 是 `shim.ts` 垫的全局对象、**没有 `location`** ——
 * 一旦哪次漏传 origin 就是运行时的 `TypeError`，编译期看不出来。默认值留在各端的壳里
 * （H5 在 `app/h5/src/invite.ts`，小游戏的调用点直接传 `API_BASE`）。
 */
export function inviteText(nickname: string, origin: string): string {
  return `${nickname}，最近有空打一局掼蛋吗？这里能看到雅加达最近的局 👉 ${origin}/`;
}
