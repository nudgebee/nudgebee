// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { GlobalSearchLocators } from "./globalSearchLocators";

// The route every test starts from. The search box lives in Header1.jsx, which renders
// on every authenticated page, so this is only ever "somewhere with a header" — the
// lightest one available, and the page the app itself redirects to from `/`.
export const LANDING_PATH = "/home";

// Same precedence LoginPage.selectCluster uses, so a run configured either way resolves
// the account this suite scopes its "@account" test to.
export function requireClusterName(): string {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER || "";
  if (!cluster) {
    throw new Error("CLUSTER_NAME (or CLUSTER) is not set — add it to .env / .env.dev");
  }
  return cluster;
}

export function requireBaseUrl(): string {
  const baseUrl = process.env.BASE_URL || "";
  if (!baseUrl) {
    throw new Error("BASE_URL is not set — add it to .env / .env.dev");
  }
  return baseUrl;
}

export function escapeForRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// A query no page, dashboard, automation or integration can match, so the empty-result
// assertions are about the filter working rather than about what the dev tenant holds.
export function noMatchTerm(prefix = "zz-no-such-page"): string {
  return `${prefix}-${Date.now()}`;
}

// Lands on a page that has the header and returns the locators, without opening the
// panel — the tests that assert how it opens need it still closed.
export async function gotoSearchHost(page: Page): Promise<GlobalSearchLocators> {
  requireBaseUrl();
  const locators = new GlobalSearchLocators(page);

  await new LoginPage(page).doFullLogin();
  await page.goto(LANDING_PATH);
  await expect(locators.searchTrigger).toBeVisible({ timeout: 60000 });

  return locators;
}

// Opens the panel from its trigger button and waits for it to be usable. The input is
// focused on a 0ms timer after mount (searchRef effect in GlobalPageSearch.jsx), so the
// focus wait is what proves the panel is ready to type into, not just painted.
export async function openGlobalSearch(page: Page): Promise<GlobalSearchLocators> {
  const locators = await gotoSearchHost(page);
  await locators.searchTrigger.click();
  await expect(locators.searchInput).toBeFocused();
  return locators;
}

// Reopens the panel on the page the test has already navigated to.
export async function reopenGlobalSearch(locators: GlobalSearchLocators): Promise<void> {
  await expect(locators.searchTrigger).toBeVisible({ timeout: 60000 });
  await locators.searchTrigger.click();
  await expect(locators.searchInput).toBeFocused();
}

// Types a query and waits for the input to hold it. Filtering is a synchronous useMemo
// over an in-memory list, so the value landing is the point the result set is settled —
// and every result assertion after this retries anyway.
export async function searchFor(locators: GlobalSearchLocators, query: string): Promise<void> {
  await locators.searchInput.fill(query);
  await expect(locators.searchInput).toHaveValue(query);
}

export async function expectPanelClosed(locators: GlobalSearchLocators): Promise<void> {
  await expect(locators.searchInput).toBeHidden();
  await expect(locators.optionsList).toBeHidden();
}
