import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AWSLocators } from "../AWSLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

test("API testing Cloud Account -> AWS -> Troubleshoot -> Triage Rules", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AWSLocators(page);

  await loginPage.doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  await expect(locators.AnchorTabTroubleshoot).toBeVisible();
  await locators.AnchorTabTroubleshoot.hover();
  await locators.TroubleshootTriageRules.waitFor({ state: "visible" });

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.TroubleshootTriageRules.click();
      await page.waitForLoadState("domcontentloaded");
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});
