// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { LlmAnalyserLocators, AnalyserScreen } from "./llmAnalyserLocators";

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
// so `#cost-analyser/models` opens that screen directly. Clicking the strip is exercised on
// its own by the navigation test, so the other tests do not all depend on that interaction.
export async function openAnalyser(page: Page, screen?: AnalyserScreen): Promise<LlmAnalyserLocators> {
  const locators = new LlmAnalyserLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise#cost-analyser${screen ? `/${screen}` : ""}`);
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

  await page.goto(`/optimise#cost-analyser/${fragment}`);
  await expect(locators.llmAnalyserTab, NO_TAB_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.root, NO_ROOT_HINT).toBeVisible({ timeout: 60000 });

  return locators;
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
