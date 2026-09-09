// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NotificationsModuleLocators } from "./notificationsModuleLocators";
import { NOTIFICATIONS_PATH } from "./notificationsModuleConstants";

// Lands on Admin > Notifications and waits until the rules listing has settled.
//
// Goes to the fragment directly rather than clicking through the Admin flyout: the
// /user-management tab effect reads router.asPath's fragment on mount and only rewrites
// the URL when the fragment is missing or names a disabled tab, so #notifications is
// honoured on a cold load. The sidebar route is covered by the tab-navigation test.
export async function openNotificationsTab(page: Page): Promise<NotificationsModuleLocators> {
  const locators = new NotificationsModuleLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(NOTIFICATIONS_PATH);
  await expect(locators.notificationsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 60000 });

  await waitForRulesListing(locators);
  return locators;
}

// index.tsx starts `loading` true so the table renders a skeleton on mount, then swaps in
// either rows or the EmptyData panel. Waiting for one of those two is what "the fetch has
// landed" means here — a tenant with no rules is a legitimate outcome, not a failure.
export async function waitForRulesListing(locators: NotificationsModuleLocators): Promise<void> {
  await expect(locators.listingRoot).toBeVisible({ timeout: 60000 });
  // .first() on the alternation as well: .or() matches either side, so without it a state
  // where both resolved would be a strict-mode violation rather than a wait.
  await expect(locators.ruleRows.first().or(locators.emptyState).first()).toBeVisible({ timeout: 60000 });
}

export async function openCreateRuleModal(locators: NotificationsModuleLocators): Promise<void> {
  await expect(locators.notificationRuleBtn).toBeVisible({ timeout: 30000 });
  await locators.notificationRuleBtn.click();
  await expect(locators.ruleDialog).toBeVisible({ timeout: 30000 });
  // The modal's own open effect sets the source to troubleshoot after mount. Waiting for
  // the name field means a later tab click cannot be undone by that effect landing late.
  await expect(locators.notificationNameInput).toBeVisible({ timeout: 30000 });
}

// Switches the rule form to Daily Recap, one of TENANT_WIDE_SOURCES in
// NotificationRuleModal.tsx — so handleSubmit's `Account selection is required` branch
// never applies and the form needs no cloud account to save.
export async function selectDailyRecapSource(locators: NotificationsModuleLocators): Promise<void> {
  await expect(locators.dailyHighTab).toBeEnabled({ timeout: 15000 });
  await locators.dailyHighTab.click();
  // The tab marks itself with className 'active' from basedOnValue, so this is the app's
  // own record of which source is selected rather than a guess about the click landing.
  await expect(locators.dailyHighTab).toHaveClass(/active/, { timeout: 15000 });
}

// Turns delivery off so the saved rule is suppressed and carries no channel mapping.
// This is what keeps the suite safe on a shared tenant: a suppressed rule cannot deliver
// to anyone's Slack, Teams or inbox, and it is also the only way to save without picking
// a real channel from someone else's integration.
export async function disableDelivery(page: Page, locators: NotificationsModuleLocators): Promise<void> {
  const toggle = page.locator(locators.enableNotificationSwitch);
  await expect(toggle).toBeVisible({ timeout: 15000 });
  if (await toggle.isChecked()) {
    await toggle.click();
  }
  await expect(toggle).not.toBeChecked({ timeout: 15000 });
}

// Creates one inert rule and proves it persisted by reading it back out of the listing.
// The success toast alone is not the assertion: the row is, because the table is
// re-fetched from the server by listNotificationRules() after the modal closes.
export async function createSuppressedRule(page: Page, locators: NotificationsModuleLocators, ruleName: string): Promise<void> {
  await openCreateRuleModal(locators);
  await selectDailyRecapSource(locators);
  await locators.notificationNameInput.fill(ruleName);
  await expect(locators.notificationNameInput).toHaveValue(ruleName);
  await disableDelivery(page, locators);

  await locators.ruleSubmitBtn.click();

  // handleSubmit closes the modal only on success, so a form left open means a validation
  // branch rejected the input and the failure should read as that rather than a stale row.
  await expect(locators.ruleDialog).toBeHidden({ timeout: 60000 });
  await expect(locators.rowByName(ruleName)).toHaveCount(1, { timeout: 60000 });
}

// Removes a rule this suite created. Tolerant of the rule already being gone so it is
// safe to call from cleanup after a test that failed before creating anything.
export async function deleteRuleByName(page: Page, locators: NotificationsModuleLocators, ruleName: string): Promise<void> {
  const row = locators.rowByName(ruleName);
  // A rule that was never created is the normal case in cleanup, so absence is a branch
  // rather than a failure.
  const exists = await row
    .first()
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);
  if (!exists) return;

  await locators.deleteBtnForRule(ruleName).click();
  await expect(locators.deleteDialog).toBeVisible({ timeout: 30000 });
  await locators.deleteConfirmBtn.click();
  await expect(locators.deleteDialog).toBeHidden({ timeout: 30000 });
  await expect(locators.rowByName(ruleName)).toHaveCount(0, { timeout: 60000 });
}
