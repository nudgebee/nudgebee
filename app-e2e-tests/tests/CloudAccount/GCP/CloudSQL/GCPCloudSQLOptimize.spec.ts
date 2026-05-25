import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { CloudAccountLocators } from "../../CloudAccountLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> GCP Cloud SQL -> Optimize", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new CloudAccountLocators(page);

  await loginPage.doFullLogin();
  await locators.openGCPCloudAccountFromConfig();

  await expect(locators.AnchorTabCloudSQL).toBeVisible();
  await locators.AnchorTabCloudSQL.hover();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.CloudSQLOptimize.click();
      await page.waitForLoadState("networkidle");
    },
    {
      testName: testInfo.title,
      operationNames: [],
      timeoutMs: 60000,
    }
  );
});
