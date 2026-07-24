import { test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { OptimizeLocators, OptimizeTabs } from "./OptimizeLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

test("Graphql testing Optimize-> Recommendations", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeLocators(page);
  await loginPage.doFullLogin();
  await locators.navigateToOptimize();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.gotoTab(OptimizeTabs.recommendations);
    },
    { testName: testInfo.title, operationNames: [], checkDataErrors: true }
  );
});
