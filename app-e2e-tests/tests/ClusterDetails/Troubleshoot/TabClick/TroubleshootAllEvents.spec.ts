import { test, expect, Page } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { TroubleshootTabLocator } from "../TroubleshootTabLocator";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";
import { registerTicketCreationTests } from "../../../utils/createTicketHelper";

test("Graphql testing Cluster Details->Troubleshoot-> All Events", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new TroubleshootTabLocator(page);
  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();
  await expect(locators.AnchorTabTroubleshoot).toBeVisible();
  await locators.AnchorTabTroubleshoot.click();
  await expect(locators.TroubleshootdropdownSummary).toBeVisible();
  await locators.TroubleshootdropdownSummary.click();
  await expect(locators.TroubleshootAllEvents).toBeVisible();
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.TroubleshootAllEvents.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});

async function navigateToAllEvents(page: Page): Promise<void> {
  const loginPage = new LoginPage(page);
  const locators = new TroubleshootTabLocator(page);

  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();

  await expect(locators.AnchorTabTroubleshoot).toBeVisible();
  await locators.AnchorTabTroubleshoot.click();

  await expect(locators.TroubleshootdropdownSummary).toBeVisible();
  await locators.TroubleshootdropdownSummary.click();

  await expect(locators.TroubleshootAllEvents).toBeVisible();
  await locators.TroubleshootAllEvents.click();

  await page.locator('[aria-label="more"]').first().waitFor({ state: "visible", timeout: 30000 });
}

registerTicketCreationTests(navigateToAllEvents, "All Events");
