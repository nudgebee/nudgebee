// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { ConfigurationLocators, CONFIG_RULES_TABLE } from "./configurationLocators";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does not
// fail here — it fails later as a goto against "about:blank". Named up front so the run
// says which key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never rendered the tab fails with the
// reason rather than with an unexplained missing element on every test.
const NO_STRIP_HINT =
  "The Optimize tab strip never rendered. /optimise gates its tabs on the session's module grants " +
  "(insights / recommendations / autooptimize) — check the run user still has read access to them.";

const NO_BODY_HINT =
  "The Configuration tab never mounted OptimizeNewPage. The tab is index 2 of filterOptions in " +
  "app/src/pages/optimise/index.jsx and is not feature-flagged — check the tab strip still renders it.";

// A term no resource name or object id can match, so the "no results" assertion is about
// the filter working rather than about what the shared dev tenant happens to hold.
// Suffixed per run because this string reaches the URL and the backend query.
export function noMatchResource(): string {
  return `zz-no-such-config-resource-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

// Opens the Configuration tab by hash. The page resolves its tab from window.location.hash
// on every filterOptions change, and OptimizeNewPage's updateUrl re-attaches the current
// hash on each filter write, so a deep link here survives the page's own URL rewrites.
// Clicking the strip is exercised on its own by the navigation test, so the other tests do
// not all depend on that one interaction.
export async function openConfigurationTab(page: Page): Promise<ConfigurationLocators> {
  const locators = new ConfigurationLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/optimise#configuration");
  await expect(locators.SummaryTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.ConfigurationTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
  await expect(locators.recommendationsRoot, NO_BODY_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// The rollup is ready when its table body has landed or its empty heading is on screen.
// Both are accepted because which one renders depends on whether the shared dev tenant
// has been scanned, not on the code under test.
export async function waitForRollup(locators: ConfigurationLocators): Promise<void> {
  await locators.waitForTable(CONFIG_RULES_TABLE, locators.configRulesEmpty);
}

// True when the tenant has at least one configuration check to act on. Tests that need a
// row branch on this rather than assuming the shared environment holds data — an
// unscanned tenant is a legitimate pass, asserted against the documented empty state.
export async function hasChecks(locators: ConfigurationLocators): Promise<boolean> {
  // Waiting for the first row or the empty state, not merely for the tbody to attach:
  // the container can attach a render before its rows do, and a count taken in that gap
  // reads 0 and would skip a test that had data to run against.
  await expect(locators.configRulesRows.first().or(locators.configRulesEmpty)).toBeVisible({ timeout: 60000 });
  return (await locators.configRulesRows.count()) > 0;
}

// Expands one rollup row and waits for the chevron to report the open state. The
// aria-expanded flip is the app's own signal that the Collapse has been asked to open,
// which is what replaces a sleep before reading the nested table.
export async function expandCheck(locators: ConfigurationLocators, row: Locator): Promise<void> {
  const toggle = locators.expandToggle(row);
  await expect(toggle).toBeVisible({ timeout: 30000 });
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true", { timeout: 30000 });
}

// The findings drawer of one expanded check settles on rows or on its own empty heading.
// A check is only listed because it has findings, but the rollup counts every severity
// band while the drawer re-queries under the active band filter, so a legitimately empty
// drawer is possible and is not a failure of the expand.
//
// Waiting on the first row rather than on the table element: the <table> renders as soon
// as the shell mounts, including while CustomTable is still showing skeleton rows, so a
// caller that counted rows straight after would read 0 mid-fetch and skip itself.
export async function waitForFindings(locators: ConfigurationLocators, row: Locator): Promise<void> {
  await expect(locators.findingsRowsIn(row).first().or(locators.findingsEmptyIn(row))).toBeVisible({ timeout: 60000 });
}

// Expands checks in order until one has findings to act on, and returns that row.
//
// The first check is not a safe bet: the rollup counts a check from a per-(rule,
// severity, account) aggregate, while the drawer re-queries the resources themselves
// under the same filter, and the two can disagree — so the top row's drawer can come
// back empty on a tenant that plainly has findings. Observed directly in run
// 33115952335, where the detail-panel test found rows in the first check and the
// overflow-menu test, six seconds later, found none and skipped itself.
//
// Returns null only when none of the first `limit` checks yield a row, which is the
// genuinely-empty case the callers skip on.
export async function expandCheckWithFindings(locators: ConfigurationLocators, limit = 3): Promise<Locator | null> {
  const available = Math.min(limit, await locators.configRulesRows.count());
  for (let index = 0; index < available; index++) {
    const row = locators.configRulesRows.nth(index);
    await expandCheck(locators, row);
    await waitForFindings(locators, row);
    if ((await locators.findingsRowsIn(row).count()) > 0) {
      return row;
    }
  }
  return null;
}
