import { expect, Page, Locator } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

async function navigateToAdminIntegrationsPage(page: Page, locators: IntegrationLocators): Promise<void> {
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();

  const tabVisible = await locators.integrationsTab
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);

  if (!tabVisible) {
    console.log("Admin nav click did not navigate — falling back to direct URL");
    await page.goto(`${process.env.BASE_URL}/user-management`);
    await locators.integrationsTab.waitFor({ state: "visible", timeout: 20000 });
  }

  await locators.integrationsTab.click();
}

async function loginAndGoToIntegrations(page: Page): Promise<IntegrationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await navigateToAdminIntegrationsPage(page, locators);
  return locators;
}

export async function navigateToCloudTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.kubernetestcloudTab).toBeVisible({ timeout: 15000 });
  await locators.kubernetestcloudTab.click();
  return locators;
}

export async function navigateToCicdTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.cicdTab).toBeVisible({ timeout: 15000 });
  await locators.cicdTab.click();
  return locators;
}

export async function navigateToDatabaseTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.databaseTab).toBeVisible({ timeout: 15000 });
  await locators.databaseTab.click();
  return locators;
}

export async function navigateToDocsTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.docsTab).toBeVisible({ timeout: 15000 });
  await locators.docsTab.click();
  return locators;
}

export async function navigateToInMemoryTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.inmemoryTab).toBeVisible({ timeout: 15000 });
  await locators.inmemoryTab.click();
  return locators;
}

export async function navigateToMessagingQueueTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.messagingQueueTab).toBeVisible({ timeout: 15000 });
  await locators.messagingQueueTab.click();
  return locators;
}

export async function navigateToServersTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.serversTab).toBeVisible({ timeout: 15000 });
  await locators.serversTab.click();
  return locators;
}

export async function navigateToMessagingTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.messagingTab).toBeVisible({ timeout: 15000 });
  await locators.messagingTab.click();
  return locators;
}

export async function navigateToTicketingTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.ticketingTab).toBeVisible({ timeout: 15000 });
  await locators.ticketingTab.click();
  return locators;
}

export async function navigateToReposTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.reposTab).toBeVisible({ timeout: 15000 });
  await locators.reposTab.click();
  return locators;
}

export async function navigateToIntegrationsPage(page: Page): Promise<IntegrationLocators> {
  return loginAndGoToIntegrations(page);
}

export async function testConnection(
  page: Page,
  {
    testConnectionBtn,
    successToast,
    serviceName,
    saveBtn,
    operationNames = [],
  }: {
    testConnectionBtn: Locator;
    successToast: Locator;
    serviceName: string;
    saveBtn: Locator;
    operationNames?: string[];
  },
): Promise<void> {
  const errorToast = page.locator(
    '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
  ).first();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await testConnectionBtn.click();
      await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 40000 });

      if (await successToast.isVisible()) {
        console.log(`Test connection SUCCESS: ${await successToast.innerText().catch(() => `${serviceName} connection successful`)}`);
        await expect(saveBtn).toBeEnabled();
      } else if (await errorToast.isVisible()) {
        const errorText = (await errorToast.innerText().catch(() => "Unknown error")).trim();
        console.error(`Test connection FAILED: ${errorText}`);
        throw new Error(`${serviceName} test connection failed: ${errorText}`);
      } else {
        throw new Error("Neither success nor error toast appeared within 40s");
      }
    },
    { testName: `${serviceName} - Test Connection`, operationNames },
  );
}

export async function saveAndHandleAlreadyExists(
  page: Page,
  {
    saveBtn,
    successToast,
    testName,
    operationNames = ["AddIntegrations"],
    ignoreErrorMessages = [],
    onSuccess,
  }: {
    saveBtn: Locator;
    successToast: Locator;
    testName: string;
    operationNames?: string[];
    ignoreErrorMessages?: string[];
    onSuccess?: () => Promise<void>;
  },
): Promise<void> {
  const errorToast = page.locator(
    '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
  ).first();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await saveBtn.click();
      await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 30000 });

      if (await successToast.isVisible()) {
        const toastText = await successToast.innerText().catch(() => "Success");
        console.log("SUCCESS:", toastText);
        if (onSuccess) await onSuccess();
      } else if (await errorToast.isVisible()) {
        const trimmed = (await errorToast.innerText().catch(() => "Unknown error")).trim();
        const isDuplicate = trimmed.includes("already exists") || trimmed.includes("already has");
        if (isDuplicate) {
          console.log("ALREADY_EXISTS:", trimmed);
        } else {
          console.error("FAILED:", trimmed);
          throw new Error(`Account creation failed: ${trimmed}`);
        }
      } else {
        throw new Error("Neither success nor error toast appeared within 30s");
      }
    },
    { testName, operationNames, ignoreErrorMessages },
  );
}

export async function submitWithDuplicateHandling(
  page: Page,
  {
    saveButton,
    successToast,
    duplicateErrorToast,
    testName,
    operationNames,
    onSuccess,
  }: {
    saveButton: Locator;
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
        await saveButton.click();
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
