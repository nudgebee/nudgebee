import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";


test("API testing Cluster Details->Monitoring-> Query Logs", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();

  await expect(locators.MonitoringDropdownQueryLogs).toBeVisible();
  
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.MonitoringDropdownQueryLogs.click();
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
  await expect(locators.MonitoringDropdownQueryLogs).toBeVisible();
  await locators.MonitoringDropdownQueryLogs.click();

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