import { test, expect, Page } from "@playwright/test";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToMessagingTab } from "./util";

test.describe.configure({ mode: "serial" });

let integrationMissing = false;

async function navigateToMSTeamsIntegration(page: Page): Promise<IntegrationLocators> {
  const locators = await navigateToMessagingTab(page);
  await locators.msTeamsBtn.click();
  await expect(locators.msTeamsIntegrationBox).toBeVisible({ timeout: 30000 });
  return locators;
}

test(
  "API testing Admin -> Integrations -> Messaging -> MS Teams -> Verify Integration",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);

    const locators = await navigateToMessagingTab(page);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.msTeamsBtn.click();
        await expect(locators.msTeamsIntegrationBox).toBeVisible({ timeout: 30000 });
      },
      {
        testName: testInfo.title,
        operationNames: ["ListMessagingPlatforms"],
      },
    );

    const isIntegrationComplete = await locators.testMsTeamsNotificationBtn.isEnabled();
    if (!isIntegrationComplete) {
      integrationMissing = true;
      console.warn(
        "\n⚠️  @qa — MS Teams integration is not configured or channel is not mapped.\n" +
          "   Please complete the MS Teams integration setup and map a team + channel first.\n" +
          "   All MS Teams integration tests will be skipped until this is resolved.\n",
      );
      test.skip(true, "@qa - MS Teams integration not configured or channel not mapped. Complete setup first.");
    }

    await expect(locators.addToMsTeamsBtn).toBeDisabled({ timeout: 10000 });
    await expect(locators.testMsTeamsNotificationBtn).toBeEnabled({ timeout: 10000 });
  },
);

test(
  "API testing Admin -> Integrations -> Messaging -> MS Teams -> Test Notification",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);
    test.skip(
      integrationMissing,
      "@qa - MS Teams integration not configured or channel not mapped. Complete setup first.",
    );

    const locators = await navigateToMSTeamsIntegration(page);

    await expect(locators.testMsTeamsNotificationBtn).toBeEnabled({ timeout: 15000 });

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.testMsTeamsNotificationBtn.click();
        await locators.msTeamsTestSuccessToast
          .or(locators.msTeamsTestErrorToast)
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
