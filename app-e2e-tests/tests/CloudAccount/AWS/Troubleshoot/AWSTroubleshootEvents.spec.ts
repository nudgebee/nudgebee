import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AWSLocators } from "../AWSLocators";

test("API testing Cloud Account -> AWS -> Troubleshoot -> Events", async ({
  page,
}) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new AWSLocators(page);

  await loginPage.doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  await locators.navigateToSubTab(locators.AnchorTabTroubleshoot, locators.TroubleshootEvents, locators.TroubleshootEventsUrl);
  await page.waitForLoadState("domcontentloaded");

  await expect(locators.AnchorTabTroubleshoot).toBeVisible();
});
