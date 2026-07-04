import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator, OptimizeSections } from "./OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("Graphql testing Cluster Details->Optimize-> Abandoned Apps", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.gotoOptimizeSection(OptimizeSections.abandonedApps);
    },
    { testName: testInfo.title, operationNames: [] }
  );
});
