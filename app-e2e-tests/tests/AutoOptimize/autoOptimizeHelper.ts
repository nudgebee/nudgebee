// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { AutoOptimizeLocators, OPTIMIZATIONS_TABLE, APPROVALS_TABLE } from "./autoOptimizeLocators";

// Every goto below is relative, resolved against `baseURL` in playwright.config.ts. That
// config reads BASE_URL without validating it, so an unset key does not fail here — it
// fails later as a goto against "about:blank". Asserted up front so the run names the
// missing key instead of reporting an unexplained navigation error.
if (!process.env.BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Sub-tab fragments from the Auto Optimize entry's tabOptions in app/src/pages/optimise/index.jsx.
export type AutoOptimizeSubTab = "optimizations" | "approvals";

// Precondition named once, so an environment that never rendered the tab fails with the
// reason rather than with an unexplained missing element on every test.
const NO_TAB_HINT =
  "The Auto Optimize tab never rendered. /optimise gates its tabs on the session's module grants " +
  "(recommendations / autooptimize) — check the run user still has read access to them.";

// A term no configuration name or id can match, so the "no results" assertion is about
// the filter working rather than about what the shared dev tenant happens to hold.
// Suffixed per call because this string reaches the backend query.
export function noMatchTerm(): string {
  return `zz-no-such-autopilot-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

// AnchorComponent marks the open top-level tab with data-tab-selected="true". Unlike the
// URL it is always in step with what is actually rendered.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
}

// MUI's <Tab> carries aria-selected, which is the sub-tab row's equivalent signal.
export async function expectSelectedSubTab(subTab: Locator): Promise<void> {
  await expect(subTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
}

// Lands on the Auto Optimize tab by hash and returns the locators plus the cluster the
// login settled on, so tests can assert the module scoped itself to that account without
// hardcoding a cluster name.
//
// Deep-linked rather than clicked: /optimise resolves its tab from window.location.hash
// (and AnchorComponent re-reads it on every router.asPath change), and unlike /vm nothing
// in this module rewrites the URL afterwards — AutoOptimizeTabs keeps its account pick in
// component state and never touches router. Clicking the strip is exercised on its own by
// the sub-tab navigation test, so the other tests do not all hang off that one interaction.
export async function openAutoOptimize(
  page: Page,
  subTab: AutoOptimizeSubTab = "optimizations"
): Promise<{ locators: AutoOptimizeLocators; cluster: string }> {
  const locators = new AutoOptimizeLocators(page);
  const selection = await new LoginPage(page).doFullLogin();

  const fragment = subTab === "optimizations" ? "auto-optimize" : `auto-optimize/${subTab}`;
  await page.goto(`/optimise#${fragment}`);

  await expect(locators.AutoOptimizeTab, NO_TAB_HINT).toBeVisible({ timeout: 60000 });
  await expectSelectedTab(locators.AutoOptimizeTab);

  if (subTab === "optimizations") {
    await settleOptimizations(locators);
  } else {
    await settleApprovals(locators);
  }

  return { locators, cluster: selection.cluster };
}

// The Optimizations listing is ready when its toolbar is up and the table has either
// landed its rows or rendered its empty heading in their place.
export async function settleOptimizations(locators: AutoOptimizeLocators): Promise<void> {
  await expect(locators.optimizationsListing).toBeVisible({ timeout: 60000 });
  await locators.waitForTable(OPTIMIZATIONS_TABLE, locators.optimizationsEmpty);
}

export async function settleApprovals(locators: AutoOptimizeLocators): Promise<void> {
  await expect(locators.approvalsListing).toBeVisible({ timeout: 60000 });
  await locators.waitForTable(APPROVALS_TABLE, locators.approvalsEmpty);
}

// Park the cursor away from the sub-tab row and the sidebar. Left on a tab strip the app
// can open a hover surface over whatever the next click is aimed at; parked at 0,0 the
// cursor sits on the sidebar rail, whose flyout is a Popover with an invisible backdrop
// that swallows clicks page-wide. Mid-viewport is neither.
export async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}
