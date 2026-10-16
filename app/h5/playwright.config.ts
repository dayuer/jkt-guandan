import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { defineConfig } from "@playwright/test";

/**
 * 浏览器端到端冒烟（#72）：拦 #52 / #55 / #56 那类只有真浏览器里才看得出的布局回归。
 *
 * 服务端是本地构建的二进制（先 `make build`），开发模式 + `guandan seed`，数据放在临时目录。
 * 只连本机：测试里把所有非本机请求都拦掉并记下来。
 */
// 端口由 `make e2e` 每次运行单独分配（scripts/e2e-port.sh，#228）；18673 只是直接跑 `npx playwright test` 时的兜底。
const PORT = Number(process.env.E2E_PORT ?? 18673);

// 临时数据目录在这里建，由 globalTeardown 删：清理不依赖服务端怎么退出。
// 配置文件会在 worker 里再加载一次，所以用环境变量保证只建一次。
process.env.E2E_DATA ??= mkdtempSync(join(tmpdir(), "guandan-e2e."));

export default defineConfig({
  testDir: "e2e",
  testMatch: "*.e2e.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  timeout: 20_000,
  expect: { timeout: 5_000 },
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: {
    browserName: "chromium",
    baseURL: `http://127.0.0.1:${PORT}`,
    viewport: { width: 375, height: 667 },
    locale: "zh-CN",
    timezoneId: "Asia/Jakarta",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "../../scripts/e2e-server.sh",
    url: `http://127.0.0.1:${PORT}/healthz`,
    env: { E2E_PORT: String(PORT), E2E_DATA: process.env.E2E_DATA },
    reuseExistingServer: false,
    timeout: 20_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
