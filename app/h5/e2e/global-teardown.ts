import { rmSync } from "node:fs";

// 数据目录由 playwright.config.ts 建好、通过 E2E_DATA 交给服务端脚本；
// 这里在整轮测试结束后删掉，不依赖服务端怎么退出（被 SIGKILL 也一样）。
export default function globalTeardown() {
  const dir = process.env.E2E_DATA;
  if (dir) rmSync(dir, { recursive: true, force: true });
}
