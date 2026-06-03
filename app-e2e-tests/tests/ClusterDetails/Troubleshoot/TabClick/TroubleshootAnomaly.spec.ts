import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { TroubleshootTabLocator } from "../TroubleshootTabLocator";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("Graphql testing Cluster Details->Troubleshoot-> Anomaly", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new TroubleshootTabLocator(page);
  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();
  await expect(locators.AnchorTabTroubleshoot).toBeVisible();
  await locators.AnchorTabTroubleshoot.click();
  await expect(locators.TroubleshootdropdownSummary).toBeVisible();
  await locators.TroubleshootdropdownSummary.click();
  await expect(locators.TroubleshootAnomaly).toBeVisible();
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.TroubleshootAnomaly.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});