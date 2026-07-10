import { test } from "@playwright/test";
import {
  navigateToCicdTab,
  testConnection,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";

const requiredEnv = [
  "ARGOCD_INTEGRATION_CONFIG_NAME",
  "ARGOCD_SECRET",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.ARGOCD_INTEGRATION_CONFIG_NAME!;

test.describe.serial("Argocd Account Integration", () => {
  // Shared across the serial tests: whether the config already existed on entry.
  let integrationExists = false;

  test("Check if Argocd integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToCicdTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.argocdBtn.click();
      },
      configName,
      serviceName: "Argocd",
      statusFilterId: "argocd-status-filter",
    });
  });

  test("Delete Argocd integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "Argocd integration not present — nothing to delete");
    const locators = await navigateToCicdTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.argocdBtn.click();
      },
      configName,
      serviceName: "Argocd",
      statusFilterId: "argocd-status-filter",
    });
  });

  test("Add Argocd Account Integration", async ({ page }) => {
    test.setTimeout(180000);
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToCicdTab(page);

    await locators.argocdBtn.click();
    await locators.addArgocdAccountBtn.click();

    await locators.argocdConfigNameInput.fill(configName);
    await locators.argocdAccountIdDropdown.click();
    await locators.argocdAccountIdOption(process.env.CLUSTER!).first().click();
    await locators.argocdAccountIdDropdown.press("Escape");
    await locators.argocdK8sSecretInput.fill(process.env.ARGOCD_SECRET!);

    await locators.argocdInsecureDropdown.click();
    await locators.argocdInsecureOption("True").click();

    const connected = await testConnection(page, {
      testConnectionBtn: locators.argocdTestConnectionBtn,
      successToast: locators.argocdTestConnectionSuccessToast,
      serviceName: "ArgoCD",
      saveBtn: locators.saveBtn,
      operationNames: ["TestIntegrationConnectionConfig"],
      skipOnBackendError: true,
      checkDataErrors: true,
      timeout: 20000,
    });
    if (!connected) return;

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.argocdSuccessToast,
      testName: "Add Argocd Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists", "already has"],
    });
  });

  test("Disable Argocd integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToCicdTab(page);
    await locators.argocdBtn.click();
    await disableIntegration(page, { configName, serviceName: "Argocd" });
  });

  test("Enable Argocd integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToCicdTab(page);
    await locators.argocdBtn.click();
    await enableIntegration(page, {
      configName,
      serviceName: "Argocd",
      statusFilterId: "argocd-status-filter",
    });
  });
});
