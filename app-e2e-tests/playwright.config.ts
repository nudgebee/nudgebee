import { defineConfig, devices } from "@playwright/test";
import path from "path";
import dotenv from "dotenv";
import { getEnvironment } from "./config/environments";

dotenv.config({ path: path.resolve(__dirname, ".env") });

const env = getEnvironment();

export default defineConfig({
  testDir: "./tests",
  timeout: 60000,
  expect: {
    timeout: 10000,
  },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,

  metadata: {
    cluster: process.env.CLUSTER || env.cluster,
    tenant: process.env.SWITCH_TENANT || env.tenant,
    environment: env.name,
  },

  reporter: [
    ["list"],
    ["html", { outputFolder: "playwright-report", open: "never" }],
    ["json", { outputFile: "playwright-report/results.json" }],
    ["./notifications/SlackReporter.ts"],
    ["./notifications/DashboardReporter.ts"],
  ],

  use: {
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    baseURL: process.env.BASE_URL,
  },

  projects: [
    {
      name: "chromium",
      testIgnore: "**/tests/admin/Integrations/*.spec.ts",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "integration-tests",
      testMatch: "**/tests/**/Integrations/*.spec.ts",
      timeout: 120000, // 2 minutes per test (login + form fill + test connection + save)
      expect: { timeout: 60000 }, // 60 seconds for integration test assertions
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
