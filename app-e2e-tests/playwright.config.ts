import { defineConfig, devices } from "@playwright/test";
import * as dotenv from "dotenv";
import * as path from "path";

// CI: env vars already injected by workflow (from GitHub secret)
// Local: dev → .env.dev | test → .env
if (!process.env.CI) {
  const envFile = process.env.E2E_ENVIRONMENT === "dev" ? ".env.dev" : ".env";
  dotenv.config({ path: path.resolve(__dirname, envFile) });
}

export default defineConfig({
  testDir: "./tests",
  timeout: 120000,
  expect: {
    timeout: 30000,
  },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,

  metadata: {
    cluster: process.env.CLUSTER || process.env.CLUSTER_NAME || "",
    tenant: process.env.SWITCH_TENANT || "",
    environment: process.env.E2E_ENVIRONMENT || "test",
  },

  reporter: [
    ["list"],
    ["html", { outputFolder: "playwright-report", open: "never" }],
    ["json", { outputFile: "playwright-report/results.json" }],
    ["./notifications/SlackReporter.ts"],
  ],

  use: {
    headless: !!process.env.CI,
    actionTimeout: 10000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    baseURL: process.env.BASE_URL,
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
