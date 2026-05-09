import { test, expect, Page } from "@playwright/test";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToMessagingTab } from "./util";

test.describe.configure({ mode: "serial" });

let integrationMissing = false;

async function navigateToGoogleChatIntegration(page: Page): Promise<IntegrationLocators> {
  const locators = await navigateToMessagingTab(page);
  await locators.googleChatBtn.click();
  await expect(locators.googleChatIntegrationBox).toBeVisible({ timeout: 30000 });
  return locators;
}

test(
  "API testing Admin -> Integrations -> Messaging -> Google Chat -> Verify Integration",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);

    const locators = await navigateToMessagingTab(page);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.googleChatBtn.click();
        await expect(locators.googleChatIntegrationBox).toBeVisible({ timeout: 30000 });
      },
      {
        testName: testInfo.title,
        operationNames: ["ListMessagingPlatforms"],
      },
    );

    const isIntegrationComplete = await locators.testGoogleChatNotificationBtn.isEnabled();
    if (!isIntegrationComplete) {
      integrationMissing = true;
      console.warn(
        "\n⚠️  @qa — Google Chat integration is not configured or channel is not mapped.\n" +
          "   Please complete the Google Chat integration setup and map a channel first.\n" +
          "   All Google Chat integration tests will be skipped until this is resolved.\n",
      );
      test.skip(true, "@qa - Google Chat integration not configured or channel not mapped. Complete setup first.");
    }

    await expect(locators.addToGoogleChatBtn).toBeDisabled({ timeout: 10000 });
    await expect(locators.testGoogleChatNotificationBtn).toBeEnabled({ timeout: 10000 });
  },
);

test(
  "API testing Admin -> Integrations -> Messaging -> Google Chat -> Test Notification",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);
    test.skip(
      integrationMissing,
      "@qa - Google Chat integration not configured or channel not mapped. Complete setup first.",
    );

    const locators = await navigateToGoogleChatIntegration(page);

    await expect(locators.testGoogleChatNotificationBtn).toBeEnabled({ timeout: 15000 });

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.testGoogleChatNotificationBtn.click();
        await locators.googleChatTestSuccessToast
          .or(locators.googleChatTestErrorToast)
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
