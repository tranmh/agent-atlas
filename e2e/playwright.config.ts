import { defineConfig, devices } from "@playwright/test";
import { VIZ_URL, WEB_URL } from "./tests/helpers/env";

/**
 * Runs against the RUNNING stack (`make up`). Serial, one worker: runs share
 * the orchestrators and the event stream.
 */
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "list" : [["list"], ["html", { open: "never" }]],
  timeout: 150_000,
  expect: { timeout: 20_000 },
  use: { trace: "retain-on-failure", screenshot: "only-on-failure" },
  projects: [
    { name: "api", testMatch: /tests\/api\/.*\.spec\.ts/, use: { baseURL: VIZ_URL } },
    {
      name: "ui",
      testMatch: /tests\/ui\/.*\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1600, height: 950 }, baseURL: WEB_URL },
    },
    {
      // Not part of `npm test`: `make screenshots` writes docs/screenshots/*.png.
      name: "screenshots",
      testMatch: /tests\/screenshots\/.*\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1600, height: 950 }, deviceScaleFactor: 1, baseURL: WEB_URL },
    },
  ],
});
