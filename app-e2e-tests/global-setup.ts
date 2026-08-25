import { chromium, FullConfig } from "@playwright/test";
import { mkdirSync } from "fs";
import * as path from "path";
import * as dotenv from "dotenv";
import { LoginPage } from "./pages/LoginPage";
import { AUTH_STATE_PATH } from "./tests/utils/paths";
import { suppressTourPopups } from "./tests/utils/tourSuppression";

// Runs the whole entry flow once — LDAP login, tenant switch, cluster selection — and
// saves it to AUTH_STATE_PATH, which every test loads via `use.storageState`.
// Retried as a unit, since a blip here would otherwise abort the entire suite.
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
      // Seeded before the first page load so the tour flags land in the storageState saved below,
      // which every test context restores — that is what suppresses the popups suite-wide.
      await suppressTourPopups(context);
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
        const loginPage = new LoginPage(page);
        const selection = await loginPage.doFullLogin({ selectCluster: true, force: true });

        // Reload and re-check before saving. doFullLogin verified the live page, which
        // does not prove the selection is actually IN the state about to be persisted —
        // if `last_account` never landed, every test restores a session with no cluster
        // and the whole suite runs green against the wrong data. Throwing here fails one
        // setup attempt instead, which the retry loop covers.
        await loginPage.verifySelection(selection);

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
