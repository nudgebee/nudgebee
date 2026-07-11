import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator, OptimizeSections } from "./OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test.describe.configure({ mode: "serial" });

test("Optimize Auto Scaler -> Summary", async ({ page }, testInfo) => {
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

test("Optimize Auto Scaler -> Logs", async ({ page }, testInfo) => {
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
