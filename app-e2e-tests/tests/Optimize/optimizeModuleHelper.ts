// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { OptimizeModuleLocators, RECOMMENDATIONS_TABLE } from "./optimizeModuleLocators";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does
// not fail here — it fails later as a goto against "about:blank". Named up front so the
// run says which key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Fragments from filterOptions in app/src/pages/optimise/index.jsx. LLM Analyser and
// AI Gateway are omitted deliberately: both are feature-flagged per tenant, so a suite
// that asserted them would pass or fail on the flag rather than on the module.
export type OptimizeModuleTab = "summary" | "recommendations" | "resolutions" | "security" | "auto-optimize";

// Precondition named once, so an environment that never rendered the strip fails with
// the reason rather than with an unexplained missing element on every test.
const NO_STRIP_HINT =
  "The Optimize tab strip never rendered. /optimise gates its tabs on the session's module grants " +
  "(insights / recommendations / autooptimize) — check the run user still has read access to them.";

// A term no resource name or object id can match, so the "no results" assertion is
// about the filter working rather than about what the shared dev tenant happens to
// hold. Suffixed per run because this string reaches the URL and the backend query.
export function noMatchTerm(): string {
  return `zz-no-such-resource-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

// AnchorComponent marks the open tab with data-tab-selected="true". Unlike the URL it
// is always in step with what is actually rendered, so it is what "which tab am I on"
// is asserted against.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
}

// Lands on a bare /optimise, with no fragment, and waits for the strip. Which tab that
// opens is left unasserted on purpose — it is what the sanity test is there to observe.
export async function landOnOptimize(page: Page): Promise<OptimizeModuleLocators> {
  const locators = new OptimizeModuleLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/optimise");
  await expect(locators.SummaryTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  return locators;
}

// Lands on a tab by hash rather than by clicking the strip.
//
// The page resolves its tab from window.location.hash on every filterOptions change
// (app/src/pages/optimise/index.jsx), and OptimizeNewPage's own updateUrl re-attaches
// the current hash on each filter write — so unlike /vm, a deep link here survives the
// page's URL rewrites. Clicking the strip is exercised on its own, by the navigation
// test, so the other tests do not all depend on that one interaction.
export async function openOptimizeTab(page: Page, fragment: OptimizeModuleTab): Promise<OptimizeModuleLocators> {
  const locators = new OptimizeModuleLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise#${fragment}`);
  await expect(locators.SummaryTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  await expectSelectedTab(tabLocator(locators, fragment));

  return locators;
}

export function tabLocator(locators: OptimizeModuleLocators, fragment: OptimizeModuleTab): Locator {
  switch (fragment) {
    case "summary":
      return locators.SummaryTab;
    case "recommendations":
      return locators.RecommendationsTab;
    case "resolutions":
      return locators.ResolutionsTab;
    case "security":
      return locators.securityTab;
    case "auto-optimize":
      return locators.AutoOptimizeTab;
  }
}

// The Recommendations tab is ready when its table body has landed or its empty copy is
// on screen. Both empty strings are accepted because which one renders depends on
// whether a filter is active, and callers use this both before and after filtering.
export async function waitForRecommendations(locators: OptimizeModuleLocators): Promise<void> {
  await expect(locators.recommendationsRoot).toBeVisible({ timeout: 60000 });
  await locators.waitForTable(RECOMMENDATIONS_TABLE, locators.recommendationsNoMatchText.or(locators.recommendationsNoDataText).first());
}

// Park the cursor away from the tab strip. Left on the strip, AnchorComponent opens the
// hovered tab's sub-tab popover over the toolbar below and the next click hits that
// instead; parked at 0,0 it sits on the sidebar rail, whose hover flyout is a Popover
// with an invisible backdrop that swallows clicks page-wide. Mid-viewport is neither.
export async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}
