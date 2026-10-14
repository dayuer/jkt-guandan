/**
 * Service Worker（docs/server-design.md §5.3、docs/h5-pwa-plan.md §6）。
 *
 * 只做两件事：把推送显示成通知、点通知时回到那一个局。**不缓存任何东西**——
 * 页面永远走网络，免得出现「报名的局还在，但服务端说已经取消了」这种缓存骗人的情况。
 *
 * 服务端发过来的内容（#48 定的格式）：
 *   {title, body, tag, url, kind, game}
 * 其中 tag 是 outbox 的去重键，同一条提醒重发时设备上只显示一次。
 */

self.addEventListener("install", () => {
  // 新版直接接管，不等所有页面关掉：提醒晚到就没有意义了。
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("push", (event) => {
  event.waitUntil(showPush(event));
});

async function showPush(event) {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    // 不是 JSON 就当纯文本处理，别把整条通知丢掉。
    data = { title: "随时掼掼蛋", body: event.data ? event.data.text() : "" };
  }

  const title = data.title || "随时掼掼蛋";
  const url = data.url || "/";

  await self.registration.showNotification(title, {
    body: data.body || "",
    // tag 相同的通知会互相替换：同一条提醒重发（进程在发送和写回之间崩过）时
    // 用户不会看到两条。
    tag: data.tag || undefined,
    renotify: false,
    icon: "/apple-touch-icon.png",
    badge: "/apple-touch-icon.png",
    data: { url, kind: data.kind || "", game: data.game || "" },
  });
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || "/";
  event.waitUntil(openOrFocus(url));
});

/** 已经有这个页面就聚焦它，否则新开一个。 */
async function openOrFocus(url) {
  const target = new URL(url, self.location.origin);
  const clients = await self.clients.matchAll({ type: "window", includeUncontrolled: true });

  for (const client of clients) {
    const current = new URL(client.url);
    // 已经在那个局（或者首页）上，就不要再开一个。
    if (current.pathname === target.pathname) {
      await client.focus();
      return;
    }
  }

  // clients.openWindow 只接受同源地址，服务端发的一定是站内路径（/g/XXXXXX）。
  await self.clients.openWindow(target.pathname);
}
