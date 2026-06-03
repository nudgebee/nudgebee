import { test, expect, Page } from "@playwright/test";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToMessagingTab } from "./util";

test.describe.configure({ mode: "serial" });

let integrationMissing = false;

async function navigateToSlackIntegration(page: Page): Promise<IntegrationLocators> {
  const locators = await navigateToMessagingTab(page);
  await locators.slackBtn.click();
  await expect(locators.slackIntegrationBox).toBeVisible({ timeout: 30000 });
  return locators;
}

test(
  "API testing Admin -> Integrations -> Messaging -> Slack -> Verify Integration",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);

    const locators = await navigateToMessagingTab(page);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.slackBtn.click();
        await expect(locators.slackIntegrationBox).toBeVisible({ timeout: 30000 });
      },
      {
        testName: testInfo.title,
        operationNames: ["ListMessagingPlatforms"],
      },
    );

    const isIntegrationComplete = await locators.testSlackNotificationBtn.isEnabled();
    if (!isIntegrationComplete) {
      integrationMissing = true;
      console.warn(
        "\n⚠️  @qa — Slack integration is not configured or channel is not mapped.\n" +
          "   Please complete the Slack integration setup and map a channel first.\n" +
          "   All Slack integration tests will be skipped until this is resolved.\n",
      );
      test.skip(true, "@qa - Slack integration not configured or channel not mapped. Complete setup first.");
    }

    await expect(locators.addToSlackBtn).toBeDisabled({ timeout: 10000 });
    await expect(locators.testSlackNotificationBtn).toBeEnabled({ timeout: 10000 });
  },
);

test(
  "API testing Admin -> Integrations -> Messaging -> Slack -> Test Notification",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);
    test.skip(
      integrationMissing,
      "@qa - Slack integration not configured or channel not mapped. Complete setup first.",
    );

    const locators = await navigateToSlackIntegration(page);

    await expect(locators.testSlackNotificationBtn).toBeEnabled({ timeout: 15000 });

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.testSlackNotificationBtn.click();
        await locators.slackTestSuccessToast
          .or(locators.slackTestErrorToast)
          .first()
          .waitFor({ state: "visible", timeout: 30000 });
      },
      {
        testName: testInfo.title,
        operationNames: ["SendTestNotification"],
      },
    );
  },
);
