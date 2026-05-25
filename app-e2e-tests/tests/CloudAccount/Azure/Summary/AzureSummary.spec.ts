import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AzureLocators } from "../AzureLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> Azure -> Summary", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AzureLocators(page);

  await loginPage.doFullLogin();
  await locators.openAzureCloudAccountFromConfig();

  // Summary is the default tab so we navigate away first to ensure clicking it triggers GraphQL calls
  await expect(locators.AnchorTabServices).toBeVisible();
  await locators.AnchorTabServices.click();
  await page.waitForLoadState("networkidle");

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await expect(locators.AnchorTabSummary).toBeVisible();
      await locators.AnchorTabSummary.click();
      await page.waitForLoadState("networkidle");
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});
