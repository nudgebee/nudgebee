import { expect, Page, Locator } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

export async function navigateToMessagingTab(page: Page): Promise<IntegrationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  await locators.integrationsTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.integrationsTab.click();
  await expect(locators.messagingTab).toBeVisible({ timeout: 15000 });
  await locators.messagingTab.click();
  return locators;
}

export async function navigateToTicketingTab(page: Page): Promise<IntegrationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  await locators.integrationsTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.integrationsTab.click();
  await expect(locators.ticketingTab).toBeVisible({ timeout: 15000 });
  await locators.ticketingTab.click();
  return locators;
}

export async function navigateToReposTab(page: Page): Promise<IntegrationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  await locators.integrationsTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.integrationsTab.click();
  await expect(locators.reposTab).toBeVisible({ timeout: 15000 });
  await locators.reposTab.click();
  return locators;
}

/**
 * Submits an integration form, validates the GraphQL operation, and
 * gracefully handles duplicate-account errors by swallowing them instead
 * of failing the test. Re-throws on any other error.
 */
export async function submitWithDuplicateHandling(
  page: Page,
  {
    jiraSaveButton ,
    successToast,
    duplicateErrorToast,
    testName,
    operationNames,
    onSuccess,
  }: {
    jiraSaveButton : Locator;
    successToast: Locator;
    duplicateErrorToast: Locator;
    testName: string;
    operationNames: string[];
    onSuccess: () => Promise<void>;
  },
): Promise<void> {
  let isDuplicateAccount = false;
  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await jiraSaveButton .click();
        await successToast
          .or(duplicateErrorToast)
          .first()
          .waitFor({ state: "visible", timeout: 30000 });
        if (await successToast.isVisible()) {
          await onSuccess();
        } else if (await duplicateErrorToast.isVisible()) {
          console.log("DUPLICATE:", (await duplicateErrorToast.innerText()).trim());
          isDuplicateAccount = true;
          throw new Error("Duplicate account detected");
        } else {
          throw new Error("Neither success nor duplicate error appeared");
        }
      },
      { testName, operationNames },
    );
  } catch (error) {
    if (!isDuplicateAccount) throw error;
  }
}
