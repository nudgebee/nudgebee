// Not for OSS
import { Page, Locator, Browser, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { LlmAnalyserLocators, AnalyserScreen } from "./llmAnalyserLocators";
import { discardUnsavedEdits, openTenantSettings, toast } from "../../admin/TenantSettings/tenantSettingsHelper";
import { FEATURE_LLM_ANALYSER, SAVE_SUCCESS_TEXT } from "../../admin/TenantSettings/tenantSettingsConstants";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does not
// fail here — it fails later as a goto against "about:blank". Named up front so the run
// says which key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never rendered the module fails with the
// reason rather than with an unexplained missing element on every test.
const NO_TAB_HINT =
  "The LLM Analyser tab is not on the Optimize strip. app/src/pages/optimise/index.jsx admits it only when " +
  "the LLM_ANALYSER tenant feature flag is on AND the run user has read access to the selected account — " +
  "with the flag off the page silently falls back to Summary. Check the flag for this tenant before reading " +
  "this as a module failure.";

const NO_ROOT_HINT =
  "The Cost Analyser body never mounted behind its tab. CostAnalyser.tsx renders #cost-analyser-root " +
  "unconditionally once the tab is active, so this means the tab was reached but the view failed to load.";

// Lands on the module by hash rather than by clicking the Optimize strip.
//
// The Optimize page resolves its tab from window.location.hash on every filterOptions
// change, and CostAnalyser resolves its own screen from the sub-fragment after the slash —
// so `#llm-analyser/models` opens that screen directly. Clicking the strip is exercised on
// its own by the navigation test, so the other tests do not all depend on that interaction.
export async function openAnalyser(page: Page, screen?: AnalyserScreen): Promise<LlmAnalyserLocators> {
  const locators = new LlmAnalyserLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise#llm-analyser${screen ? `/${screen}` : ""}`);
  await expect(locators.llmAnalyserTab, NO_TAB_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.root, NO_ROOT_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// Lands on a screen whose fragment the module does not recognise, so the caller can assert
// what the rejection does. Deliberately does not wait for a selected screen — which one
// ends up selected is what the test observes.
export async function openAnalyserWithFragment(page: Page, fragment: string): Promise<LlmAnalyserLocators> {
  const locators = new LlmAnalyserLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise#llm-analyser/${fragment}`);
  await expect(locators.llmAnalyserTab, NO_TAB_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.root, NO_ROOT_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// Cross-checks the LLM_ANALYSER tenant flag before the suite spends every test timing out on
// NO_TAB_HINT one at a time, and turns it on if that is why the tab is missing. Called once
// from this spec's test.beforeAll — not per test — since flipping and saving the tenant flag
// is an admin write, and redoing it before every test would just race the flag against
// itself for no benefit once the first check has already settled it.
export async function ensureLlmAnalyserFeatureEnabled(browser: Browser, baseURL?: string): Promise<void> {
  // browser.newPage() does NOT inherit playwright.config.ts's `use` block (baseURL,
  // storageState, …) the way the test/context fixtures do — it opens a bare context. The
  // relative goto() below still resolves because doFullLogin() first navigates to an
  // absolute BASE_URL itself, but passing baseURL here keeps this context honest about what
  // it actually is rather than relying on that ordering.
  const context = await browser.newContext({ baseURL });
  const page = await context.newPage();
  try {
    const locators = new LlmAnalyserLocators(page);
    await new LoginPage(page).doFullLogin();
    await page.goto("/optimise#llm-analyser");

    // Short on purpose: this is a presence probe, not the real test assertion — by the time
    // the page is idle the tab has already rendered or the flag is off, and burning the real
    // tests' full 60s here just to reach the same answer would only slow the one-time check.
    // A false result is the normal "flag off" branch, not a failure — it's what routes into
    // the Tenant Settings check below.
    const tabVisible = await locators.llmAnalyserTab
      .waitFor({ state: "visible", timeout: 15000 })
      .then(() => true)
      .catch(() => false);
    if (tabVisible) {
      console.log("LLM Analyser tab is visible — feature flag is already on, nothing to do");
      return;
    }

    console.log("LLM Analyser tab did not appear — checking the LLM_ANALYSER tenant feature flag");
    const ts = await openTenantSettings(page);
    await ts.openTab(ts.featuresTab);
    await expect(ts.featuresTable).toBeVisible({ timeout: 60000 });

    const toggle = ts.featureToggle(FEATURE_LLM_ANALYSER);
    await expect(
      toggle,
      `"${FEATURE_LLM_ANALYSER}" is not on the Features table — the flag catalog may have renamed it`
    ).toBeVisible({ timeout: 30000 });

    if (await toggle.isChecked()) {
      // The flag is on and the tab still didn't render — not the case this helper fixes, and
      // clicking the toggle here would turn a working flag off.
      throw new Error(
        `"${FEATURE_LLM_ANALYSER}" is already enabled but the LLM Analyser tab never appeared — ` +
          "this is not a flag-off case; check the run user's read access to the selected account instead."
      );
    }

    if (!(await ts.canEdit())) {
      throw new Error(
        `"${FEATURE_LLM_ANALYSER}" is disabled and this run's user lacks tenants:Write to enable it — ` +
          "grant the flag on this tenant directly, or point the suite at a user who can."
      );
    }

    const attempts = 3;
    for (let attempt = 1; attempt <= attempts; attempt++) {
      // Checked before every attempt, not assumed off: a retry after a save that actually
      // landed but read back flaky would otherwise click a checked box and turn it off again.
      if (!(await toggle.isChecked())) {
        await toggle.click();
        await expect(toggle).toBeChecked();
        await ts.saveButton().click();
        await expect(toast(page, SAVE_SUCCESS_TEXT)).toBeVisible({ timeout: 60000 });
      }

      // Read back from a reload rather than trusting the click+toast — the same reasoning
      // the Features suite itself uses: only a reload proves the flag actually persisted.
      const reloaded = await discardUnsavedEdits(page);
      await reloaded.openTab(reloaded.featuresTab);
      await expect(reloaded.featuresTable).toBeVisible({ timeout: 60000 });
      const confirmed = await reloaded.featureToggle(FEATURE_LLM_ANALYSER).isChecked();
      if (confirmed) {
        console.log(`"${FEATURE_LLM_ANALYSER}" enabled and verified on attempt ${attempt}/${attempts}`);
        return;
      }
      console.log(`"${FEATURE_LLM_ANALYSER}" did not read back as enabled on attempt ${attempt}/${attempts}, retrying...`);
    }

    throw new Error(`Failed to enable "${FEATURE_LLM_ANALYSER}" after ${attempts} attempts`);
  } finally {
    await page.close();
    await context.close();
  }
}

// MUI Tab carries aria-selected on the tab itself, which — unlike the URL — is always in
// step with what is actually rendered, so it is what "which screen am I on" is asserted
// against.
export async function expectScreenSelected(locators: LlmAnalyserLocators, screen: AnalyserScreen): Promise<void> {
  await expect(locators.screenTab(screen)).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
}

export async function expectScreenNotSelected(locators: LlmAnalyserLocators, screen: AnalyserScreen): Promise<void> {
  await expect(locators.screenTab(screen)).toHaveAttribute("aria-selected", "false", { timeout: 30000 });
}

// Clicks a screen on the inner strip and waits for it to take. CustomTabs runs in
// behavior='router' mode here, so the click both navigates and moves the selection — the
// URL is asserted separately by the navigation test rather than folded in here.
export async function openScreen(locators: LlmAnalyserLocators, screen: AnalyserScreen): Promise<void> {
  await locators.screenTab(screen).click();
  await expectScreenSelected(locators, screen);
}

// The checked option of a ds/ToggleGroup. Each option is a role="radio" carrying
// aria-checked, so this reads the group's committed state rather than its styling.
export async function expectToggleChecked(option: Locator, checked: boolean): Promise<void> {
  await expect(option).toHaveAttribute("aria-checked", String(checked), { timeout: 30000 });
}

// Park the cursor away from the Optimize tab strip. Left on the strip, AnchorComponent
// opens the hovered tab's sub-tab popover over the content below and the next click hits
// that instead; parked at 0,0 it sits on the sidebar rail, whose hover flyout is a Popover
// with an invisible backdrop that swallows clicks page-wide. Mid-viewport is neither.
export async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}
