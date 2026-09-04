// Not for OSS

import { Page, Locator, expect } from "@playwright/test";
import { TenantSettingsLocators } from "./tenantSettingsLocators";

// The tenant this run is pointed at. Read from the environment rather than
// written into the suite: the same specs run against dev and test, which carry
// different tenant names, and a hardcoded one would assert the wrong thing on
// whichever environment it was not copied from.
export function requiredTenantName(): string {
  const tenant = process.env.SWITCH_TENANT || "";
  if (!tenant) throw new Error("SWITCH_TENANT is not set — add it to .env / .env.dev");
  return tenant;
}

// Lands on Admin -> Tenant Settings with the General tab open, which is the
// component's own initial activeTab.
export async function openTenantSettings(page: Page): Promise<TenantSettingsLocators> {
  const locators = new TenantSettingsLocators(page);
  await locators.open();
  await expect(locators.generalTab).toBeVisible({ timeout: 30000 });
  return locators;
}

// A toast by its copy. SnackbarComponent renders role="alert" for errors and
// role="status" for everything else, so matching on the text alone would also
// match the same words rendered in the page body.
export function toast(page: Page, text: string): Locator {
  return page.locator('[role="alert"], [role="status"]').filter({ hasText: text }).first();
}

// Restores the General tab to the state it was found in, without saving.
//
// Both validation guards this suite exercises return before any network write,
// so nothing has been persisted at this point — but the form still holds the
// invalid values, and a later test in the same worker would inherit them. A
// reload is what discards them, and it is also the assertion that the edits
// really were never committed.
export async function discardUnsavedEdits(page: Page): Promise<TenantSettingsLocators> {
  // A reload rather than a second goto. The fragment survives a reload, and
  // /user-management honours #tenant-settings on mount (its hash effect only
  // rewrites the fragment when the target tab is disabled, which it is not here
  // or open() would already have failed), so re-navigating would be a second
  // page load to the destination the page is already on. The tour handler is
  // registered per page via addLocatorHandler and is idempotent, so it survives
  // the reload and does not need re-arming.
  await page.reload({ waitUntil: "domcontentloaded" });
  const locators = new TenantSettingsLocators(page);
  await locators.tabList.waitFor({ state: "visible", timeout: 90000 });
  await expect(locators.generalTab).toBeVisible({ timeout: 30000 });
  return locators;
}

// Turns the self-onboarding checkbox on if it is not already, and reports
// whether this call was the one that changed it, so the caller can put it back.
export async function ensureSelfOnboardingChecked(locators: TenantSettingsLocators): Promise<boolean> {
  await expect(locators.selfOnboardingCheckbox).toBeVisible({ timeout: 30000 });
  if (await locators.selfOnboardingCheckbox.isChecked()) return false;
  await locators.selfOnboardingCheckbox.check();
  await expect(locators.selfOnboardingCheckbox).toBeChecked();
  return true;
}
