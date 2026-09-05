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

// Suite wall time is dominated by how many browsers run at once, so the two
// numbers that set it are env-tunable: a workflow_dispatch can trial a value
// before anyone merges a config change.
//
// PW_WORKERS: the suite ran on a single worker while the ARC runner reserves
// 6 vCPU (arc-v2-runner/values-nudgebee.yaml requests cpu: "6", one runner per
// c4-standard-8 node), so five cores idled for 3.2h. Raised to 2 rather than
// straight to 4: ~250 specs mutate one shared dev tenant under fixed fixture
// names, and a cross-file collision would look like a new test failure, not
// like a config change. Step it up once a couple of nightlies match the known
// failure list.
//
// PW_EXPECT_TIMEOUT: every genuine failure pays this twice (retries: 1). The
// median test finishes in 10s, so a shorter timeout only bites the failure
// path -- but a passing assertion that legitimately needs >15s would turn into
// a false failure, so the default is unchanged until a dispatch run proves a
// lower value safe.

// Blank, not just unset: the workflow always defines both vars and leaves them
// empty on every event but a dispatch that filled them in, and `"" ?? 2` keeps
// the empty string -- Number("") is 0, which would hand Playwright 0 workers.
function positiveIntFromEnv(name: string, fallback: number): number {
  const parsed = Number(process.env[name]);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback;
}

const workers = positiveIntFromEnv("PW_WORKERS", 2);
const expectTimeout = positiveIntFromEnv("PW_EXPECT_TIMEOUT", 30000);

export default defineConfig({
  testDir: "./tests",
  timeout: isDevEnv ? 120000 : 240000,
  expect: {
    timeout: expectTimeout,
  },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? workers : undefined,

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
    // Off, not "retain-on-failure": that setting records every test and then
    // throws the file away for the ~88% that pass, paying encode CPU on each
    // one and pushing the report artifact to 1.4GB. The trace above is what
    // failures are actually debugged from, and it carries a screenshot per step.
    video: "off",
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
