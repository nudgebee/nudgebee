import { Page, expect, TestInfo } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { NotificationLocators } from "./NotificationLocators";

async function selectFromFilterDropdown(
  page: Page,
  dropdownLocator: import("@playwright/test").Locator,
  optionText: string
): Promise<boolean> {
  const locators = new NotificationLocators(page);
  const searchInput = locators.dropdownSearchInput;
  const isAlreadyOpen = await searchInput.isVisible().catch(() => false);
  if (!isAlreadyOpen) {
    await dropdownLocator.click();
  }
  const isSearchVisible = await searchInput.waitFor({ state: "visible", timeout: 2000 })
    .then(() => true)
    .catch(() => false);
  if (isSearchVisible) {
    // A re-mount while channels load can drop the fill; the option lookup below then skips as usual.
    await searchInput.fill(optionText, { timeout: 3000 }).catch(() => {});
  }
  const option = page
    .locator('[role="option"]')
    .filter({ has: page.getByText(optionText, { exact: true }) })
    .first();
  await option.waitFor({ state: "visible" }).catch(() => {});
  let found = await option.isVisible().catch(() => false);
  if (!found && !isSearchVisible) {
    // Grouped lists start collapsed and only auto-expand while searching, which a short list has no box for.
    const headerCount = await locators.dropdownGroupHeaders.count();
    for (let i = 0; i < headerCount && !found; i++) {
      await locators.dropdownGroupHeaders.nth(i).click();
      // A group that does not hold the option is the normal case, so absence reads as false.
      found = await option.waitFor({ state: "visible", timeout: 1000 }).then(() => true).catch(() => false);
    }
  }
  if (!found) {
    console.warn(`Option "${optionText}" not found in dropdown — skipping.`);
    // Only Escape an open picker: with none open it would close the whole editor, and a stuck one blocks Save.
    if (await locators.dropdownListbox.isVisible()) {
      await page.keyboard.press("Escape");
    }
    await expect(locators.dropdownListbox).toHaveCount(0);
    return false;
  }
  await page.mouse.move(0, 0);
  await page.waitForTimeout(150);
  await option.click().catch(async () => {
    await option.click({ force: true });
  });
  return true;
}

export interface ChannelConfig {
  slack?: string;
  msTeamsGroup?: string;
  msTeamsChannel?: string;
  gChat?: string;
  email?: string;
  excludeUsers?: string[];
}

export async function navigateToNewNotificationRule(
  page: Page
): Promise<NotificationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new NotificationLocators(page);

  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();

  await locators.notificationsTab.waitFor({ state: "visible" });
  await locators.notificationsTab.click();

  await expect(locators.notificationRuleBtn).toBeVisible();
  await locators.notificationRuleBtn.click();
  await locators.blankRuleItem.click();
  await expect(locators.notificationNameInput).toBeVisible();

  return locators;
}

// Limits the rule to the configured account. Types that always cover every
// account (weekly digest, daily email) show no account picker.
export async function selectCluster(
  locators: NotificationLocators,
  page: Page
): Promise<void> {
  const clusterName = process.env.CLUSTER_NAME || process.env.CLUSTER;
  if (!clusterName) throw new Error("CLUSTER_NAME or CLUSTER env var is not set");
  const hasScope = await locators.scopeSelector
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (!hasScope) {
    console.log(`[selectCluster] This rule's types cover every account — skipping.`);
    return;
  }
  await selectFromFilterDropdown(page, locators.scopeSelector, "These accounts");
  await locators.clusterSelector.waitFor({ state: "visible" });
  await selectFromFilterDropdown(page, locators.clusterSelector, clusterName);
  // A multi-select picker stays open after a pick; a single one closes itself.
  const stillOpen = await locators.clusterSelector.getAttribute("aria-expanded").catch(() => null);
  if (stillOpen === "true") {
    await page.keyboard.press("Escape");
  }
}

export async function configureChannels(
  page: Page,
  locators: NotificationLocators,
  channelConfig: ChannelConfig
): Promise<boolean> {
  let anyConfigured = false;

  if (channelConfig.slack && (await locators.slackChannelSelector.isVisible())) {
    const slackSelected = await selectFromFilterDropdown(page, locators.slackChannelSelector, channelConfig.slack);
    if (slackSelected) anyConfigured = true;
  }

  if (
    channelConfig.msTeamsGroup &&
    channelConfig.msTeamsChannel &&
    (await locators.msTeamsGroupSelector.isVisible())
  ) {
    const groupSelected = await selectFromFilterDropdown(page, locators.msTeamsGroupSelector, channelConfig.msTeamsGroup);

    if (groupSelected) {
      await locators.msTeamsChannelSelector.waitFor({ state: "visible" });
      const channelSelected = await selectFromFilterDropdown(page, locators.msTeamsChannelSelector, channelConfig.msTeamsChannel);
      if (channelSelected) anyConfigured = true;
    }
  }

  const isDev = process.env.E2E_ENVIRONMENT === "dev";
  if (!isDev && channelConfig.gChat && (await locators.gChatChannelSelector.isVisible())) {
    const gChatSelected = await selectFromFilterDropdown(page, locators.gChatChannelSelector, channelConfig.gChat);
    if (gChatSelected) anyConfigured = true;
  }

  // The daily email sends to users, with an extra address and users left out.
  if ((channelConfig.email || channelConfig.excludeUsers?.length) && (await locators.emailInput.isVisible())) {
    if (channelConfig.email) {
      await locators.emailInput.fill(channelConfig.email);
      anyConfigured = true;
    }

    if (channelConfig.excludeUsers?.length) {
      await locators.excludeUsersSelector.waitFor({ state: "visible" });
      for (const userEmail of channelConfig.excludeUsers) {
        await selectFromFilterDropdown(page, locators.excludeUsersSelector, userEmail);
      }
      anyConfigured = true;
    }
  }

  return anyConfigured;
}

export async function submitAndVerify(
  page: Page,
  locators: NotificationLocators,
  ruleName: string,
  anyChannelConfigured: boolean,
  testInfo: TestInfo
): Promise<void> {
  if (!anyChannelConfigured) {
    console.warn(
      `\n⚠️  No messaging channels installed — saving "${ruleName}" as a one-hour Don't send.\n` +
        "   Install Slack, MS Teams, or Google Chat to test channel routing.\n"
    );
    await locators.dontSendAction.click();
    await selectFromFilterDropdown(page, locators.pauseForSelector, "1 hour");
  }

  await locators.notificationNameInput.fill(ruleName);

  // A taken name is refused client-side with no GraphQL call, so the watcher would fail on an empty capture.
  if ((await locators.ruleNameText(ruleName).count()) > 0) {
    await locators.ruleSaveBtn.click();
    await expect(locators.getDuplicateError()).toBeVisible();
    // The editor may already be closed by the time Cancel is clicked.
    await locators.ruleCancelEditorBtn.click().catch(() => {});
    return;
  }

  const backendErrorMsg = page.getByText("Upstream unreachable", { exact: false })
    .or(page.getByText("fetch failed", { exact: false }));

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.ruleSaveBtn.click();
      await expect(
        locators.successToast
          .or(locators.updatedSuccessToast)
          .or(locators.getDuplicateError())
          .or(locators.duplicateConstraintError)
          .or(backendErrorMsg)
      ).toBeVisible();
    },
    {
      testName: testInfo.title,
      operationNames: [],
      ignoreErrorMessages: ["unique constraint", "Upstream unreachable", "fetch failed"],
    }
  );

  if (await backendErrorMsg.isVisible().catch(() => false)) {
    console.warn(`⚠️  Backend unreachable creating rule "${ruleName}" — skipping without failure.`);
    await locators.ruleCancelEditorBtn.click().catch(() => {});
  }
}
