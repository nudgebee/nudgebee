// Not for OSS
import { Page, expect } from "@playwright/test";
import { EventSubTabs } from "../troubleshootEventsLocators";
import { openTroubleshoot, openEventSubTab, deepLinkTroubleshoot } from "../troubleshootEventsHelper";
import { AlertTuningLocators } from "./alertTuningLocators";

// The hash /troubleshoot uses for this sub-tab, per EventSubTabs.alertTuning.
export const ALERT_TUNING_HASH = "all-events/threshold-suggestions";

// Lands on Alert Tuning the way a person does — sign in, sidebar, All Events strip.
// openTroubleshoot already asserts the env and drives the login, so this only adds the
// sub-tab click; the locators are re-bound to the richer class afterwards, since both
// classes are stateless views over the same page.
export async function openAlertTuning(page: Page): Promise<AlertTuningLocators> {
  await openTroubleshoot(page);
  const locators = new AlertTuningLocators(page);
  await openEventSubTab(locators, EventSubTabs.alertTuning);
  await expect(locators.thresholdToolbar).toBeVisible({ timeout: 30000 });
  return locators;
}

// Opens /troubleshoot straight at the Alert Tuning hash, for the cases where the hash
// itself — not the click that would normally produce it — is under test.
export async function deepLinkAlertTuning(page: Page): Promise<AlertTuningLocators> {
  await deepLinkTroubleshoot(page, ALERT_TUNING_HASH);
  return new AlertTuningLocators(page);
}

// The listing resolves to exactly one of two states: CustomTable renders its TableBody
// when there is data and swaps it for <EmptyData> when there is not. Waiting on the
// union means neither branch is a fixed sleep, and returning which one landed lets the
// data-dependent journeys assert the right contract instead of pinning tenant data.
export async function settleListing(locators: AlertTuningLocators): Promise<boolean> {
  await expect(locators.rows.first().or(locators.emptyState)).toBeVisible({ timeout: 60000 });
  const rowCount = await locators.rows.count();
  return rowCount > 0;
}

// Asserts the two states are mutually exclusive — a table body and an empty state on
// screen together would mean the listing rendered a stale result beside a fresh one.
// The <table> itself is not the discriminator: CustomTable renders it and its header
// row unconditionally and only withholds the TableBody (renderTableContent guards on
// visibleTableData.length), dropping <EmptyData> in below it instead.
export async function expectExclusiveListingState(locators: AlertTuningLocators, hasRows: boolean): Promise<void> {
  if (hasRows) {
    await expect(locators.tableBody).toBeVisible();
    await expect(locators.emptyState).toHaveCount(0);
    return;
  }
  await expect(locators.emptyState).toBeVisible();
  await expect(locators.emptyHint).toBeVisible();
  await expect(locators.tableBody).toHaveCount(0);
}
