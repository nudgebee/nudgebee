import { test } from "@playwright/test";
import { navigateToMessagingTab } from "./util";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test(
  "API testing Admin -> Integrations -> Messaging -> Google Chat -> Verify Integration",
  async ({ page }, testInfo) => {
    test.setTimeout(120000);

    const locators = await navigateToMessagingTab(page);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.googleChatBtn.click();
      },
      {
        testName: testInfo.title,
        operationNames: [],
      },
    );
  },
);
