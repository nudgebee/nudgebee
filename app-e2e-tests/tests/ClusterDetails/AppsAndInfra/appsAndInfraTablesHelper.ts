// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { AppsAndInfraTablesLocators, Listing, LISTINGS } from "./appsAndInfraTablesLocators";

// openClusterFromConfig reads CLUSTER_NAME/CLUSTER itself, but it does so several
// navigations in. Naming the key here means an unset environment fails with the key
// that is missing rather than with a cluster dropdown that never matched.
const CLUSTER = process.env.CLUSTER_NAME || process.env.CLUSTER || "";
if (!CLUSTER) {
  throw new Error("CLUSTER (or CLUSTER_NAME) is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never reached the section fails
// with the reason instead of with an unexplained missing element.
const NO_SECTION_HINT =
  "The Apps & Infra sub-tab strip never rendered. The section is tabOptions[3] of the cluster detail page and " +
  "its sub-tabs are gated per module grant — check the run user still has Kubernetes read access on this cluster.";

export const NO_ROWS_HINT =
  "This listing came back empty on the shared dev cluster, so there was no row to act on. This case needs a " +
  "cluster that actually holds one; it is not a defect in the page.";

// A term no Kubernetes object name can match, so a "no results" assertion is about the
// search working rather than about what the shared dev cluster happens to hold.
// Suffixed per run because this string reaches the URL and the backend query.
export function noMatchTerm(): string {
  return `zz-no-such-resource-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

// Logs in (session reused via global-setup), opens the configured cluster and lands on
// the Apps & Infra section. Which sub-tab that opens is left unasserted here — it is
// what the sanity test exists to observe.
export async function setup(page: Page): Promise<AppsAndInfraTablesLocators> {
  const locators = new AppsAndInfraTablesLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.openClusterFromConfig();
  await locators.navigateToCluster();

  await expect(locators.subTab(LISTINGS.nodes), NO_SECTION_HINT).toBeVisible({ timeout: 60000 });
  return locators;
}

// Opens a listing and waits for it to settle into rows or its empty panel. Goes through
// AppsAndInfraLocators.clickTab, which clicks the sub-tab strip and falls back to the
// section's dropdown when the strip entry is off-screen.
export async function openListing(locators: AppsAndInfraTablesLocators, listing: Listing): Promise<void> {
  await locators.clickTab(listing.id);
  await expect(locators.subTab(listing)).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
  await locators.waitForListingSettled(listing);
}

// Asserts a listing renders every column header it declares. CustomTable renders the
// header row inside the id'd table, so this is scoped to that table rather than to the
// page — several listings declare overlapping header names.
export async function expectHeaders(locators: AppsAndInfraTablesLocators, listing: Listing): Promise<void> {
  const table = locators.table(listing);
  for (const header of listing.headers) {
    await expect(table.locator("th", { hasText: header }).first()).toBeVisible({ timeout: 30000 });
  }
}

// SearchInput commits on Enter (onEnterPress) — typing alone filters nothing.
export async function searchAndApply(input: Locator, term: string): Promise<void> {
  await input.click();
  await input.fill(term);
  await input.press("Enter");
}

// The X only renders while the field holds a value, so clearing goes through the field
// itself and re-commits, matching SearchInput's onChange('') fast path.
export async function clearSearch(input: Locator): Promise<void> {
  await input.click();
  await input.fill("");
  await input.press("Enter");
}

// Opens a FilterDropdown and commits one named option. Only used against filters whose
// options are declared in the component itself, never fetched — a filter whose options
// come from their own API can be legitimately empty on a given cluster, which tests the
// environment rather than the page. See the PR's Follow-ups.
export async function selectFilterOption(page: Page, locators: AppsAndInfraTablesLocators, trigger: Locator, optionLabel: string): Promise<void> {
  await expect(trigger).toBeVisible({ timeout: 30000 });
  await trigger.click();

  const option = locators.filterOption(optionLabel);
  await expect(
    option,
    `The filter panel never offered "${optionLabel}" — either the trigger did not open it, or the options are genuinely empty (which renders a "No results found" line and no option rows).`
  ).toBeVisible({ timeout: 30000 });
  await option.click();
  // Move off the trigger so the popover's backdrop stops intercepting later clicks.
  await page.mouse.move(0, 0);
}
