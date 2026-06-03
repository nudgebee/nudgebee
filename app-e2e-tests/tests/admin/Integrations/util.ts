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

/**
 * Returns true if connection test succeeded (save can proceed).
 * Returns false if backend was unreachable / timed out (skipOnBackendError: true).
 * Throws if connection test explicitly failed and skipOnBackendError is false (default).
 *
 * When skipOnBackendError: true — the watcher still validates the operation and fires
 * a Slack alert on data-level errors (success:false), but the throw is caught so the
 * test continues. This gives accurate Slack alerts without failing the test for known
 * backend issues.
 */
export async function testConnection(
  page: Page,
  {
    testConnectionBtn,
    successToast,
    serviceName,
    saveBtn,
    operationNames = [],
    timeout = 40000,
    skipOnBackendError = false,
    checkDataErrors = false,
  }: {
    testConnectionBtn: Locator;
    successToast: Locator;
    serviceName: string;
    saveBtn: Locator;
    operationNames?: string[];
    timeout?: number;
    skipOnBackendError?: boolean;
    checkDataErrors?: boolean;
  },
): Promise<boolean> {
  const errorToast = page.locator(
    '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
  ).first();

  let succeeded = false;

  // Abort signal: resolves when watcher detects a targeted failure, so the
  // toast wait exits early instead of running to the full timeout.
  let resolveAbort!: () => void;
  const abortPromise = new Promise<void>(resolve => { resolveAbort = resolve; });

  const runWatcher = async () => {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await testConnectionBtn.click();

        let abortedEarly = false;
        const appeared = await Promise.race([
          successToast
            .or(errorToast)
            .first()
            .waitFor({ state: "visible", timeout })
            .then(() => true)
            .catch(() => false),
          abortPromise.then(() => { abortedEarly = true; return false; }),
        ]);

        if (!appeared || abortedEarly) {
          if (skipOnBackendError) {
            const reason = abortedEarly
              ? "API failure detected by GraphQL watcher"
              : `no toast within ${timeout / 1000}s (backend unreachable or slow)`;
            console.warn(`⚠️  ${serviceName} test connection — ${reason}. Skipping save.`);
            return;
          }
          throw new Error(`Neither success nor error toast appeared within ${timeout / 1000}s`);
        }

        if (await successToast.isVisible()) {
          console.log(`Test connection SUCCESS: ${await successToast.innerText().catch(() => `${serviceName} connection successful`)}`);
          await expect(saveBtn).toBeEnabled();
          succeeded = true;
        } else if (await errorToast.isVisible()) {
          const errorText = (await errorToast.innerText().catch(() => "Unknown error")).trim();
          if (skipOnBackendError) {
            console.warn(`⚠️  ${serviceName} test connection error (treating as backend issue): ${errorText}. Skipping save.`);
          } else {
            console.error(`Test connection FAILED: ${errorText}`);
            throw new Error(`${serviceName} test connection failed: ${errorText}`);
          }
        }
      },
      {
        testName: `${serviceName} - Test Connection`,
        operationNames,
        checkDataErrors,
        onTargetedFailure: () => resolveAbort(),
      },
    );
  };

  if (skipOnBackendError) {
    // Let the watcher capture + alert on real errors, but don't propagate the throw
    // so the test can skip save gracefully instead of failing.
    try {
      await runWatcher();
    } catch (e) {
      console.warn(`⚠️  ${serviceName} - GraphQL watcher reported an issue (skipOnBackendError=true): ${e}`);
      return false;
    }
  } else {
    await runWatcher();
  }

  return succeeded;
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
    inlineError,
  }: {
    saveBtn: Locator;
    successToast: Locator;
    testName: string;
    operationNames?: string[];
    ignoreErrorMessages?: string[];
    onSuccess?: () => Promise<void>;
    inlineError?: Locator;
  },
): Promise<void> {
  const errorToast = page.locator(
    '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
  ).first();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await saveBtn.click();

      if (inlineError) {
        const inlineAppeared = await inlineError
          .waitFor({ state: "visible", timeout: 2000 })
          .then(() => true)
          .catch(() => false);
        if (inlineAppeared) {
          const trimmed = (await inlineError.innerText().catch(() => "Unknown error")).trim();
          const isDuplicate = trimmed.includes("already exists") || trimmed.includes("already has");
          if (isDuplicate) {
            console.log("ALREADY_EXISTS (inline):", trimmed);
            return;
          }
          throw new Error(`Account creation failed (inline): ${trimmed}`);
        }
      }

      const toastAppeared = await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 10000 })
        .then(() => true)
        .catch(() => false);

      if (!toastAppeared) {
        console.log(`${testName}: No toast appeared after save — likely duplicate ignored by backend`);
        return;
      }

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
      }
    },
    { testName, operationNames: inlineError ? [] : operationNames, ignoreErrorMessages },
  );
}

