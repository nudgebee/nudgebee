import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { SecurityAndToolsTabLocator } from "./SecurityAndToolsTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("API testing Cluster Details->Security And Tools-> Upgrade Planner", { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] }, async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new SecurityAndToolsTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToSecurityAndToolsTab();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.UpgradePlannerDropdown);
    },
    {
      testName: testInfo.title,
    }
  );
});

