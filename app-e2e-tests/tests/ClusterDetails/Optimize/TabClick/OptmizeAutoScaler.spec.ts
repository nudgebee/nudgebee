import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { OptimizeTabLocator } from "../OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("Graphql testing Cluster Details->Optimize-> AutoScaler", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();
  await expect(locators.OptimizeTab).toBeVisible();
  await locators.OptimizeTab.click();
  await expect(locators.OptimizedropdownAutoScaler).toBeVisible();
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.OptimizedropdownAutoScaler.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});