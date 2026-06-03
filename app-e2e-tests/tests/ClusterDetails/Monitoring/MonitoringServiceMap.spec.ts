import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("API testing Cluster Details->Monitoring-> Service Map", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();

  await expect(locators.MonitoringDropdownServicemap).toBeVisible();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.MonitoringDropdownServicemap.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});