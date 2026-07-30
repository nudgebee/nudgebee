import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { TroubleshootTabLocator } from "./TroubleshootTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("Graphql testing Cluster Details->Troubleshoot-> Summary", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new TroubleshootTabLocator(page);
  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();
  await locators.navigateToTroubleshootTab();
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.TroubleshootdropdownSummary);
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});