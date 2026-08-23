import { test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { TroubleshootLocators, TroubleshootTabs } from "./TroubleshootLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

test("Graphql testing Troubleshoot-> All Events", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new TroubleshootLocators(page);
  await loginPage.doFullLogin();
  await locators.navigateToTroubleshoot();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.gotoTab(TroubleshootTabs.allEvents);
    },
    { testName: testInfo.title, operationNames: [] }
  );
});
