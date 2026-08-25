import { defineConfig, devices } from "@playwright/test";
import * as dotenv from "dotenv";
import * as path from "path";
import { existsSync } from "fs";
import { AUTH_STATE_PATH } from "./tests/utils/paths";

// Load the env file for the selected environment. dotenv never overrides a
// variable already present in process.env, so anything set by the CI runner
// still wins — this just fills in values from the file (e.g. the .env.oss that
// CI materializes from the E2E_OSS_ENV secret).
const env = process.env.E2E_ENVIRONMENT;
const envFile = env === "dev" ? ".env.dev" : env === "oss" ? ".env.oss" : ".env";
dotenv.config({ path: path.resolve(__dirname, envFile) });

const isDevEnv = process.env.E2E_ENVIRONMENT === "dev";

export default defineConfig({
  testDir: "./tests",
  timeout: isDevEnv ? 120000 : 240000,
  expect: {
    timeout: 30000,
  },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,

  // Log in once, reuse the session across every test (see global-setup.ts).
  globalSetup: require.resolve("./global-setup"),

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
    headless: process.env.E2E_HEADLESS === "1" || !!process.env.CI,
    actionTimeout: isDevEnv ? 10000 : 20000,
    navigationTimeout: isDevEnv ? 30000 : 60000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    baseURL: process.env.BASE_URL,
    // Reuse the session captured by global-setup so tests start authenticated.
    // Only when the file is actually there: pointing storageState at a missing
    // file makes EVERY test fail at context creation ("Error reading storage
    // state ... ENOENT") before a single line of test code runs. That happens
    // whenever global-setup has not run — notably the VS Code extension's
    // per-test ▶ Run Test, which does not run it. Without the state file tests
    // simply start logged out, and doFullLogin() performs the LDAP login itself.
    storageState: existsSync(AUTH_STATE_PATH) ? AUTH_STATE_PATH : undefined,
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
