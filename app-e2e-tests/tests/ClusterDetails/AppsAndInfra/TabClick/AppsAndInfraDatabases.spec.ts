import { test } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AppsAndInfraLocators } from "../AppsAndInfraLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";   

test("API testing Cluster Details->Apps And Infra-> Databases", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AppsAndInfraLocators(page);

  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();
  await locators.navigateToCluster();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.Databases);
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});
