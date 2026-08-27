// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { SecurityLocators, SECURITY_SUBTABS, SecuritySubTab } from "./securityLocators";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does not
// fail here — it fails later as a goto against "about:blank". Named up front so the run
// says which key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never rendered the strip fails with the
// reason rather than with an unexplained missing element on every test.
const NO_STRIP_HINT =
  "The Optimize tab strip never rendered. /optimise gates its tabs on the session's module grants " +
  "(insights / recommendations / autooptimize) — check the run user still has read access to them.";

const NO_SUBSTRIP_HINT =
  "The Security sub-tab strip never rendered. AnchorComponent only draws it once the Security tab is " +
  "the active tab — check the Security tab is still reachable for the run user.";

// A term no image reference can match, so the "no results" assertion is about the filter
// working rather than about what the shared dev tenant happens to hold. Suffixed per run
// because this string reaches the backend query.
export function noMatchImage(): string {
  return `zz-no-such-image-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

// MUI Tab reports its selection through aria-selected. Unlike the URL it is always in step
// with what is actually rendered, so it is what "which sub-tab am I on" is asserted against.
export async function expectSelectedSubTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
}

export async function expectUnselectedSubTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("aria-selected", "false", { timeout: 30000 });
}

// Lands on the Security tab with no sub-fragment. Which sub-tab that opens is left
// unasserted on purpose — it is what the sanity test is there to observe.
export async function openSecurityTab(page: Page): Promise<SecurityLocators> {
  const locators = new SecurityLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/optimise#security");
  await expect(locators.securityTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.securityTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
  await expect(locators.imageScanSubTab, NO_SUBSTRIP_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// Lands on a Security sub-tab by hash. app/src/pages/optimise/index.jsx splits the hash on
// "/" and resolves the child against the Security tab's tabOptions, and AnchorComponent
// parses the same pair for the strip — so a deep link opens the sub-tab directly. Clicking
// the strip is exercised on its own by the navigation tests, so the other tests do not all
// depend on that one interaction.
export async function openSecuritySubTab(page: Page, key: SecuritySubTab): Promise<SecurityLocators> {
  const locators = new SecurityLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise#security/${SECURITY_SUBTABS[key].fragment}`);
  await expect(locators.securityTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.securityTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
  await expect(locators.imageScanSubTab, NO_SUBSTRIP_HINT).toBeVisible({ timeout: 60000 });
  await expectSelectedSubTab(subTabLocator(locators, key));

  return locators;
}

export function subTabLocator(locators: SecurityLocators, key: SecuritySubTab): Locator {
  switch (key) {
    case "imageScan":
      return locators.imageScanSubTab;
    case "cisScan":
      return locators.cisScanSubTab;
    case "vmVulnerabilities":
      return locators.vmVulnerabilitiesSubTab;
    case "cloudPosture":
      return locators.cloudPostureSubTab;
  }
}

// The copy SecurityView shows in place of a sub-view when the tenant holds no account of the
// type that sub-tab reports on. Which of the three renders is itself the assertion on that
// branch, so an empty environment still proves the module names the right missing resource.
export const NO_ACCOUNTS_COPY = {
  cluster: /No clusters connected\. Findings appear here once a cluster is connected and scanned\./,
  vm: /No self-hosted VM accounts connected\. Findings appear here once a VM account is added and scanned\./,
  cloud: /No cloud accounts connected\. Findings appear here once an AWS, Azure or GCP account is added and scanned\./,
} as const;

// Settles a sub-tab's body onto one of its two legitimate branches and asserts that branch.
// A shared environment can lack every account of a type, which is a real state of the module
// rather than a failure — so the empty branch is verified against the copy for that sub-tab
// and reported back, and callers assert the view's own controls only when there is a view.
export async function expectSecurityBody(
  locators: SecurityLocators,
  viewRoot: Locator,
  emptyCopy: RegExp
): Promise<boolean> {
  await expect(locators.securityView.or(locators.securityEmpty).first()).toBeVisible({ timeout: 60000 });
  await expect(locators.securityLoading).toHaveCount(0);

  // Safe to read without waiting: the expect above has already settled one of the two
  // branches, so this only asks which one landed. isVisible() reports a missing element as
  // false rather than throwing, so it needs no catch.
  const isEmpty = await locators.securityEmpty.isVisible();
  if (isEmpty) {
    await expect(locators.securityEmpty).toHaveText(emptyCopy);
    return false;
  }

  await expect(viewRoot).toBeVisible({ timeout: 60000 });
  return true;
}

// Waits out the fetch behind one of the four findings tables. CustomTable swaps in an
// unid'd skeleton tbody while loading, so `#<id>-body` being attached means the response has
// landed; the empty state is the other terminal state.
export async function waitForFindingsTable(locators: SecurityLocators, tableId: string, scope: Locator): Promise<void> {
  await locators.waitForTable(tableId, locators.emptyStateFor(tableId, scope));
}

// Rows or the empty state — the two ends of a settled table. Which one a shared environment
// produces is not what any of these tests is about, so this is what they assert instead.
export async function expectTableSettled(locators: SecurityLocators, tableId: string, scope: Locator): Promise<void> {
  await waitForFindingsTable(locators, tableId, scope);
  await expect(locators.rowsFor(tableId).first().or(locators.emptyStateFor(tableId, scope)).first()).toBeVisible({ timeout: 60000 });
}
