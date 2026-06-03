import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AzureLocators } from "../AzureLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> Azure SQL -> Summary", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AzureLocators(page);

  await loginPage.doFullLogin();
  await locators.openAzureCloudAccountFromConfig();

  await expect(locators.AnchorTabSQL).toBeVisible();
  await locators.AnchorTabSQL.hover();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.SQLSummary.click();
      await page.waitForLoadState("networkidle");
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});
