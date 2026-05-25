import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("API testing Cluster Details->Monitoring-> Alert Manager", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();

  await expect(locators.MonitoringDropdownAlertManager).toBeVisible();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.MonitoringDropdownAlertManager.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});