import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AWSLocators } from "../AWSLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> AWS -> Optimize -> Right Sizing", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AWSLocators(page);

  await loginPage.doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  await expect(locators.AnchorTabOptimize).toBeVisible();
  await locators.AnchorTabOptimize.hover();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.OptimizeRightSizing.click();
      await page.waitForLoadState("networkidle");
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});
