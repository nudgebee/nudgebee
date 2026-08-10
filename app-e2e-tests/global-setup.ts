import { chromium, FullConfig } from "@playwright/test";
import { mkdirSync } from "fs";
import * as path from "path";
import * as dotenv from "dotenv";
import { LoginPage } from "./pages/LoginPage";
import { AUTH_STATE_PATH } from "./tests/utils/paths";

// Runs once before the whole suite: logs in a single time and persists the
// authenticated session to AUTH_STATE_PATH. Every test then loads that state
// via `use.storageState`, so per-test doFullLogin() skips the slow LDAP form.
//
// Because this is now the single login for the whole suite, a transient failure
// here (slow page load, network blip) would abort every test. So the login is
// retried a few times before giving up — cheap insurance for a one-shot step.
const MAX_ATTEMPTS = 3;

async function globalSetup(_config: FullConfig) {
  if (!process.env.CI) {
    const envFile = process.env.E2E_ENVIRONMENT === "dev" ? ".env.dev" : ".env";
    dotenv.config({ path: path.resolve(__dirname, envFile) });
  }

  const browser = await chromium.launch({ headless: !!process.env.CI });
  try {
    for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
      const context = await browser.newContext({ baseURL: process.env.BASE_URL });
      const page = await context.newPage();
      try {
        // Select the cluster here too. ClusterDropDown persists the choice to
        // localStorage (`nudgebee.userPreferences.last_account`) and restores it
        // on mount, so it rides along in the saved storageState and every test
        // starts on the right cluster without driving the dropdown itself. The
        // retry loop around this call covers the dropdown's flakiness.
        //
        // force: this is the run that resolves and records the tenant, so it must
        // switch for real rather than trusting a tenant recorded by an earlier run.
        await new LoginPage(page).doFullLogin({ selectCluster: true, force: true });

        mkdirSync(path.dirname(AUTH_STATE_PATH), { recursive: true });
        await context.storageState({ path: AUTH_STATE_PATH });
        console.log(`[global-setup] Authenticated session saved to ${AUTH_STATE_PATH}`);
        await context.close();
        return;
      } catch (err) {
        await context.close().catch(() => {});
        const message = err instanceof Error ? err.message : String(err);
        console.warn(`[global-setup] Login attempt ${attempt}/${MAX_ATTEMPTS} failed: ${message}`);
        if (attempt === MAX_ATTEMPTS) throw err;
      }
    }
  } finally {
    await browser.close();
  }
}

export default globalSetup;
