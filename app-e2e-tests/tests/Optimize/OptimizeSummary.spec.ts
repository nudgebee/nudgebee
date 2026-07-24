import { test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { OptimizeLocators } from "./OptimizeLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

test("Graphql testing Optimize-> Summary", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeLocators(page);
  await loginPage.doFullLogin();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.navigateToOptimize();
    },
    { testName: testInfo.title, operationNames: [], checkDataErrors: true }
  );
});
