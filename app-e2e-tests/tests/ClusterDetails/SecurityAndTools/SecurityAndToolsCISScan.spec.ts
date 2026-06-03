import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { SecurityAndToolsTabLocator } from "./SecurityAndToolsTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("API testing Cluster Details->Security And Tools-> CIS Scan", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new SecurityAndToolsTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToSecurityAndToolsTab();

  await expect(locators.CISScanDropdown).toBeVisible();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.CISScanDropdown.click();
    },
    {
      testName: testInfo.title,
    }
  );
});

