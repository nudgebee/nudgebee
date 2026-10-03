// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { assertTroubleshootEnv, expectSelectedSubTab } from "../troubleshootEventsHelper";
import { EventSubTabs } from "../troubleshootEventsLocators";
import { TriageInboxLocators } from "./triageInboxLocators";

// Lands on the Triage Inbox the way a person does — sign in, then the sidebar
// button. Triage Inbox is sub-tab 0, so a bare /troubleshoot already opens it and
// nothing is clicked: clicking it would make the "it is the default" assertion in
// the first test observe its own click. doFullLogin seeds the tour-suppression
// preferences, so no tour dialog mounts over the toolbar.
export async function openTriageInbox(page: Page): Promise<TriageInboxLocators> {
  assertTroubleshootEnv();
  const locators = new TriageInboxLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.navigateToTroubleshoot();
  await expect(locators.eventTabsBox).toBeVisible({ timeout: 60000 });
  await expectSelectedSubTab(locators.eventSubTab(EventSubTabs.triageInbox));
  await locators.waitForListing();
  return locators;
}

// Clicks back onto Triage Inbox from a sibling sub-tab. openEventSubTab deliberately
// does NOT click this tab — it is sub-tab 0, so the shared helper treats it as already
// open — which is right on a fresh landing and wrong once another sub-tab has been
// opened. This clicks it and waits for the app's own aria-selected before settling.
export async function returnToTriageInbox(locators: TriageInboxLocators): Promise<void> {
  const target = locators.eventSubTab(EventSubTabs.triageInbox);
  await expect(target).toBeVisible({ timeout: 30000 });
  await target.click();
  // Park the cursor in open content: left on the strip, AnchorComponent opens a hover
  // popover over the toolbar below and the next click lands on that instead.
  await locators.parkCursor();
  await expectSelectedSubTab(target);
  await locators.waitForListing();
}

// Several journeys only mean something against a populated listing — "every row
// carries the NEW chip" is vacuously true over zero rows. This states that
// precondition once, so an empty dev inbox reports itself instead of turning into
// a green run that asserted nothing.
export async function requireRows(locators: TriageInboxLocators): Promise<number> {
  const count = await locators.rows.count();
  expect(count, "The Triage Inbox listed no events, so this journey has nothing to act on").toBeGreaterThan(0);
  return count;
}
