import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { CloudAccountLocators } from "../../CloudAccountLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> GCP Cloud Storage -> Optimize", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new CloudAccountLocators(page);

  await loginPage.doFullLogin();
  await locators.openGCPCloudAccountFromConfig();

  await expect(locators.AnchorTabCloudStorage).toBeVisible();
  await locators.AnchorTabCloudStorage.hover();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.CloudStorageOptimize.click();
      await page.waitForLoadState("networkidle");
    },
    {
      testName: testInfo.title,
      operationNames: [],
      timeoutMs: 60000,
    }
  );
});
