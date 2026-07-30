import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("API testing Cluster Details->Monitoring-> Trace Group", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.MonitoringDropdownTraceGroup);
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});