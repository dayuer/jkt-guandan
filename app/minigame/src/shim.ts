// 小游戏没有 window / localStorage。共享层（app/shared 的 status.ts、learn.ts）会读 window.localStorage，
// 这里用 wx 的本地存储垫一层。必须是 main.ts 的第一个 import。

const g: any = typeof GameGlobal !== "undefined" ? GameGlobal : globalThis;

const storage = {
  getItem(key: string): string | null {
    try {
      const v = wx.getStorageSync(key);
      return v === "" || v === undefined || v === null ? null : String(v);
    } catch {
      return null;
    }
  },
  setItem(key: string, value: string): void {
    wx.setStorageSync(key, String(value));
  },
  removeItem(key: string): void {
    try {
      wx.removeStorageSync(key);
    } catch {
      // 删不掉就算了
    }
  },
};

if (typeof g.window === "undefined") g.window = g;
if (!g.window.localStorage) g.window.localStorage = storage;
if (typeof g.localStorage === "undefined") g.localStorage = storage;

export {};
