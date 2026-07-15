import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: "line",
  outputDir: "../bin/playwright-results",
  timeout: 45_000,
  use: {
    ...devices["Desktop Edge"],
    channel: "msedge",
    headless: true,
    trace: "off",
  },
});
