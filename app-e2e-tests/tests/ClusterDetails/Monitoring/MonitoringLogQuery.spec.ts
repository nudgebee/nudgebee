import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";


test("API testing Cluster Details->Monitoring-> Query Logs", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.MonitoringDropdownQueryLogs);
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});


test("API testing Cluster Details->Monitoring-> Query Logs -> Run Query", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();
  await locators.clickTab(locators.MonitoringDropdownQueryLogs);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.RunQueryButton.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});