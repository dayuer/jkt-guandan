import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * PWA 元数据和服务端推送内容的契约（#10、§5.3）。
 *
 * 这些东西没有任何一处会被构建拦住：sw.js 里少写一个 handler、manifest 里少一个字段，
 * 类型检查、打包、页面渲染全都照常通过，只有真机上收不到推送才发现。所以这里把
 * 它们和服务端（#48）约定好的形状钉住。
 */

const read = (path: string) => readFileSync(resolve(process.cwd(), path), "utf8");

const swSource = readFileSync(resolve(process.cwd(), "public/sw.js"), "utf8");

describe("Service Worker（public/sw.js）", () => {
  const sw = swSource;

  it("接了 push 和 notificationclick 两种事件", () => {
    // 少了 push：通知根本不会显示；少了 notificationclick：点了通知没反应。
    expect(sw).toMatch(/addEventListener\(\s*"push"/);
    expect(sw).toMatch(/addEventListener\(\s*"notificationclick"/);
  });

  it("通知的 tag 用服务端给的去重键，重发时设备上只显示一条", () => {
    expect(sw).toMatch(/tag:\s*data\.tag/);
  });

  it("点通知打开的是服务端给的那个局的路径", () => {
    expect(sw).toMatch(/notification\.data\.url|data\.url/);
    expect(sw).toMatch(/openWindow/);
  });

  it("不缓存页面——页面永远走网络", () => {
    // 缓存会带来「页面说还有位、服务端说已经满了」这种自相矛盾，这个站承受不起。
    expect(sw).not.toMatch(/caches\./);
  });
});

describe("manifest 和 iOS 的元数据", () => {
  const html = read("index.html");
  const manifest = JSON.parse(read("public/manifest.json"));

  it("manifest 以 standalone 打开，并有图标", () => {
    expect(manifest.display).toBe("standalone");
    expect(manifest.start_url).toBe("/");
    expect(Array.isArray(manifest.icons) && manifest.icons.length > 0).toBe(true);
  });

  it("index.html 挂上了 manifest 和 apple-touch-icon", () => {
    expect(html).toMatch(/rel="manifest"\s+href="\/manifest\.json"/);
    expect(html).toMatch(/rel="apple-touch-icon"/);
    // iOS 的 web push 只有在主屏幕上打开才可用，这两行是它的前提。
    expect(html).toMatch(/apple-mobile-web-app-capable/);
  });

  it("用的是 .json 而不是 .webmanifest", () => {
    // Go 的 mime 表里没有 .webmanifest，内嵌的静态服务会把它发成 octet-stream，
    // Chrome 会因此拒绝这份 manifest。
    // 只看 href 本身：注释里提到 .webmanifest 没关系（那里正在解释为什么不用它）。
    expect(html).not.toMatch(/href="[^"]*\.webmanifest"/);
  });

  it("manifest.json 里引用的图标文件都在", () => {
    for (const icon of manifest.icons) {
      expect(() => read("public" + icon.src)).not.toThrow();
    }
  });
});

describe("推送内容的格式（和 #48 的约定）", () => {
  const sw = read("public/sw.js");

  it("sw.js 读的就是 title / body / tag / url 这几个字段", () => {
    // 服务端发的是 {"title","body","tag","url","kind","game"}（见 #48 的 PR 描述）。
    for (const field of ["title", "body", "tag", "url"]) {
      expect(sw).toContain(field);
    }
  });

  it("不是 JSON 的推送也照样显示，不整条丢掉", () => {
    expect(sw).toMatch(/catch/);
  });
});

/**
 * sw.js 里没有一行会被 TypeScript 检查（它不在模块图里），所以真正的保证是拿假事件
 * 把它跑一遍。这里把 sw.js 当函数执行，注入一个假的 self，然后自己触发 push。
 */
function runServiceWorker() {
  const listeners: Record<string, ((ev: unknown) => void)[]> = {};
  const shown: { title: string; options: Record<string, unknown> }[] = [];
  const opened: string[] = [];
  const focused: string[] = [];

  const fakeSelf = {
    addEventListener: (name: string, fn: (ev: unknown) => void) => {
      (listeners[name] ||= []).push(fn);
    },
    skipWaiting: () => {},
    location: { origin: "https://guandan.id" },
    registration: {
      showNotification: async (title: string, options: Record<string, unknown>) => {
        shown.push({ title, options });
      },
    },
    clients: {
      matchAll: async () => [
        {
          url: "https://guandan.id/",
          focus: async () => focused.push("/"),
        },
      ],
      openWindow: async (url: string) => {
        opened.push(url);
      },
    },
  };

  // 用 Function 而不是 import：sw.js 不是模块，只有 Service Worker 环境里的 self。
  new Function("self", swSource)(fakeSelf);
  return { listeners, shown, opened, focused };
}

describe("把推送显示成通知（用假事件真跑一遍 sw.js）", () => {
  const payload = {
    title: "还有 1 小时开局",
    body: "10月14日 19:00 · Pacific Place",
    tag: "t_minus_1h:SEED01:u1:3",
    url: "/g/SEED01",
    kind: "t_minus_1h",
    game: "SEED01",
  };

  it("服务端的 title / body / tag / url 原样落到通知上", async () => {
    const { listeners, shown } = runServiceWorker();
    const event = {
      data: { json: () => payload },
      waitUntil: (p: Promise<unknown>) => p,
    };
    await (listeners.push[0](event) as unknown as Promise<void>);

    expect(shown).toHaveLength(1);
    expect(shown[0].title).toBe("还有 1 小时开局");
    expect(shown[0].options.body).toBe("10月14日 19:00 · Pacific Place");
    // tag 用服务端的去重键：同一条提醒重发时设备上只显示一次。
    expect(shown[0].options.tag).toBe("t_minus_1h:SEED01:u1:3");
    expect(shown[0].options.data).toMatchObject({ url: "/g/SEED01", kind: "t_minus_1h", game: "SEED01" });
  });

  it("没有 data 的推送（纯文本）也能显示，不抛异常", async () => {
    const { listeners, shown } = runServiceWorker();
    const event = {
      data: {
        json: () => {
          throw new Error("不是 JSON");
        },
        text: () => "服务端说了句话",
      },
      waitUntil: (p: Promise<unknown>) => p,
    };
    await (listeners.push[0](event) as unknown as Promise<void>);

    expect(shown).toHaveLength(1);
    expect(shown[0].options.body).toBe("服务端说了句话");
  });

  it("点通知时打开服务端给的那个局", async () => {
    const { listeners, opened } = runServiceWorker();
    const event = {
      notification: { close: () => {}, data: { url: "/g/SEED01" } },
      waitUntil: (p: Promise<unknown>) => p,
    };
    await (listeners.notificationclick[0](event) as unknown as Promise<void>);
    expect(opened).toEqual(["/g/SEED01"]);
  });
});
