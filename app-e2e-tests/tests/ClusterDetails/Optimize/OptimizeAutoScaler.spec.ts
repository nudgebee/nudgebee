import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator, OptimizeSections } from "./OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("Optimize Auto Scaler -> Summary", { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] }, async ({ page }, testInfo) => {
  test.setTimeout(120000);
  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.gotoOptimizeSection(OptimizeSections.autoScaler);
    },
    { testName: testInfo.title, operationNames: [] }
  );

  await locators.Summary.waitFor({ state: "visible", timeout: 15000 });
  await locators.Summary.click();
});

test("Optimize Auto Scaler -> Logs", { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] }, async ({ page }, testInfo) => {
  test.setTimeout(120000);
  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();
  await locators.gotoOptimizeSection(OptimizeSections.autoScaler);

  await locators.Logs.waitFor({ state: "visible", timeout: 15000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.Logs.click();
    },
    { testName: testInfo.title, operationNames: [] }
  );
});
