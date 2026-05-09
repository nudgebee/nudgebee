import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AWSLocators } from "../AWSLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> AWS EC2 -> Events", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AWSLocators(page);

  await loginPage.doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  await expect(locators.AnchorTabEC2).toBeVisible();
  await locators.AnchorTabEC2.hover();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.EC2Events.click();
      await page.waitForLoadState("networkidle");
    },
    {
      testName: testInfo.title,
      operationNames: []
    }
  );
});
