import { test } from "@playwright/test";
import {
  navigateToDocsTab,
  testConnection,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  ensureIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";

const requiredEnv = [
  "CONFLUENCE_TEST_HOST",
  "CONFLUENCE_INTEGRATION_CONFIG_NAME",
  "CONFLUENCE_TOKEN",
  "CONFLUENCE_USER_NAME",
  "CONFLUENCE_NAMESPACE",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.CONFLUENCE_INTEGRATION_CONFIG_NAME!;

test.describe.serial("Confluence Account Integration", () => {
  let integrationExists = false;

  test("Check if Confluence integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDocsTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.confluenceBtn.click();
      },
      configName,
      serviceName: "Confluence",
      statusFilterId: "confluence-status-filter",
    });
  });

  test("Delete Confluence integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "Confluence integration not present — nothing to delete");
    const locators = await navigateToDocsTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.confluenceBtn.click();
      },
      configName,
      serviceName: "Confluence",
      statusFilterId: "confluence-status-filter",
    });
  });

  test("Add Confluence Account Integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDocsTab(page);

    await locators.confluenceBtn.click();
    await locators.addConfluenceAccountBtn.click();

    await locators.confluenceAccountIdDropdown.click();
    await locators.confluenceAccountIdOption(process.env.CLUSTER!).first().click();
    await page.keyboard.press("Escape");
    await locators.confluenceHostInput.fill(process.env.CONFLUENCE_TEST_HOST!);
    await locators.confluenceConfigNameInput.fill(configName);
    await locators.confluenceNamespaceInput.fill(process.env.CONFLUENCE_NAMESPACE!);
    await locators.confluenceTokenInput.fill(process.env.CONFLUENCE_TOKEN!);
    await locators.confluenceUserNameInput.fill(process.env.CONFLUENCE_USER_NAME!);

    const connected = await testConnection(page, {
      testConnectionBtn: locators.confluenceTestConnectionBtn,
      successToast: locators.confluenceTestConnectionSuccessToast,
      serviceName: "Confluence",
      saveBtn: locators.saveBtn,
      operationNames: ["TestIntegrationConnectionConfig"],
      skipOnBackendError: true,
      checkDataErrors: true,
    });
    if (!connected) return;

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.confluenceSuccessToast,
      testName: "Add Confluence Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists", "already has"],
    });
  });

  test("Disable Confluence integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDocsTab(page);
    const present = await ensureIntegrationPresent(page, {
      openList: async () => {
        await locators.confluenceBtn.click();
      },
      configName,
      serviceName: "Confluence",
      statusFilterId: "confluence-status-filter",
      integrationKey: "confluence",
    });
    test.skip(!present, "Confluence integration is not Active — Slack notification sent");
    await disableIntegration(page, { configName, serviceName: "Confluence" });
  });

  test("Enable Confluence integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDocsTab(page);
    const present = await ensureIntegrationPresent(page, {
      openList: async () => {
        await locators.confluenceBtn.click();
      },
      configName,
      serviceName: "Confluence",
      statusFilterId: "confluence-status-filter",
      integrationKey: "confluence",
    });
    test.skip(!present, "Confluence integration is not Active — Slack notification sent");
    await enableIntegration(page, {
      configName,
      serviceName: "Confluence",
      statusFilterId: "confluence-status-filter",
    });
  });
});
