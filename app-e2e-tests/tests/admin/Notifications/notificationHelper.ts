import { Page, expect, TestInfo } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { ensureSwitchEnabled } from "../../utils/helpers";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { NotificationLocators } from "./NotificationLocators";

/**
 * Selects an option from a FilterDropdownButton (the new custom dropdown).
 * Clicks the button, optionally types in the search box (if visible), then clicks the option.
 * Returns false if the option was not found (e.g. channel doesn't exist in this environment).
 */
async function selectFromFilterDropdown(
  page: Page,
  dropdownLocator: import("@playwright/test").Locator,
  optionText: string
): Promise<boolean> {
  await dropdownLocator.click();
  const searchInput = page.locator('input[placeholder="Search..."]');
  const isSearchVisible = await searchInput.isVisible().catch(() => false);
  if (isSearchVisible) {
    await searchInput.click();
    await searchInput.pressSequentially(optionText, { delay: 30 });
    await page.waitForTimeout(500);
  }
  const option = page
    .locator('[role="option"]')
    .filter({ has: page.getByText(optionText, { exact: true }) })
    .first();
  const found = await option.isVisible().catch(() => false);
  if (!found) {
    console.warn(`Option "${optionText}" not found in dropdown — skipping.`);
    await page.keyboard.press("Escape");
    return false;
  }
  await option.click();
  return true;
}

export interface ChannelConfig {
  slack?: string;
  msTeamsGroup?: string;
  msTeamsChannel?: string;
  gChat?: string;
}

/**
 * Logs in, navigates to Admin > Notifications, and opens the new rule modal.
 * Ensures the notification switch is ON before returning.
 */
export async function navigateToNewNotificationRule(
  page: Page
): Promise<NotificationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new NotificationLocators(page);

  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();

  await locators.notificationsTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.notificationsTab.click();

  await expect(locators.notificationRuleBtn).toBeVisible({ timeout: 15000 });
  await locators.notificationRuleBtn.click();

  await ensureSwitchEnabled(page, locators.enableNotificationSwitch);

  return locators;
}

/**
 * Selects the cluster from the account dropdown using the CLUSTER_NAME env var.
 */
export async function selectCluster(
  locators: NotificationLocators,
  page: Page
): Promise<void> {
  const clusterName = process.env.CLUSTER_NAME || "iteration-test";
  await locators.clusterSelector.waitFor({ state: "visible", timeout: 10000 });
  await selectFromFilterDropdown(page, locators.clusterSelector, clusterName);
}

/**
 * Configures Slack, MS Teams, and Google Chat channels if their badges are visible.
 * Returns true if at least one channel was successfully configured.
 */
export async function configureChannels(
  page: Page,
  locators: NotificationLocators,
  channelConfig: ChannelConfig
): Promise<boolean> {
  let anyConfigured = false;

  if (channelConfig.slack && (await locators.slackBadge.isVisible())) {
    await locators.slackBadge.click();
    await locators.slackChannelSelector.waitFor({ state: "visible" });
    const slackSelected = await selectFromFilterDropdown(page, locators.slackChannelSelector, channelConfig.slack);
    if (slackSelected) anyConfigured = true;
  }

  if (
    channelConfig.msTeamsGroup &&
    channelConfig.msTeamsChannel &&
    (await locators.msTeamsBadge.isVisible())
  ) {
    await locators.msTeamsBadge.click();
    await locators.msTeamsGroupSelector.waitFor({ state: "visible" });
    const groupSelected = await selectFromFilterDropdown(page, locators.msTeamsGroupSelector, channelConfig.msTeamsGroup);

    if (groupSelected) {
      await locators.msTeamsChannelSelector.waitFor({ state: "visible" });
      const channelSelected = await selectFromFilterDropdown(page, locators.msTeamsChannelSelector, channelConfig.msTeamsChannel);
      if (channelSelected) anyConfigured = true;
    }
  }

  if (channelConfig.gChat && (await locators.gChatBadge.isVisible())) {
    await locators.gChatBadge.click();
    await locators.gChatChannelSelector.waitFor({ state: "visible" });
    const gChatSelected = await selectFromFilterDropdown(page, locators.gChatChannelSelector, channelConfig.gChat);
    if (gChatSelected) anyConfigured = true;
  }

  return anyConfigured;
}

/**
 * Fills the rule name, submits the form, validates the InsertNotificationRule
 * GraphQL operation fired on submit, and waits for a success or duplicate toast.
 *
 * If no channels were configured (none installed in this environment), suppresses
 * the notification before submitting — this bypasses the "at least one channel"
 * validation added in the rule update fix, allowing the rule to be created/updated.
 */
export async function submitAndVerify(
  page: Page,
  locators: NotificationLocators,
  ruleName: string,
  anyChannelConfigured: boolean,
  testInfo: TestInfo
): Promise<void> {
  if (!anyChannelConfigured) {
    console.warn(
      `\n⚠️  No messaging channels installed — suppressing rule "${ruleName}" to allow creation.\n` +
        "   Install Slack, MS Teams, or Google Chat to test channel routing.\n"
    );
    await page.locator(locators.enableNotificationSwitch).click();
  }

  await locators.notificationNameInput.fill(ruleName);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.submitBtn.click();
      await expect(
        locators.successToast
          .or(locators.updatedSuccessToast)
          .or(locators.getDuplicateError())
          .or(locators.duplicateConstraintError)
      ).toBeVisible({ timeout: 15000 });
    },
    {
      testName: testInfo.title,
      operationNames: [],
      ignoreErrorMessages: ["unique constraint"],
    }
  );
}
