import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { TroubleshootTabLocator } from "./TroubleshootTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("Graphql testing Cluster Details->Troubleshoot-> Event By Type", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new TroubleshootTabLocator(page);
  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();
  await locators.navigateToTroubleshootTab();
  await locators.clickTab(locators.TroubleshootdropdownSummary);
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.TroubleubleshootEventsByType);
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});