import { test } from "@playwright/test";
import {
  navigateToDatabaseTab,
  testConnection,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";

const requiredEnv = ["CLICKHOUSE_INTEGRATION_CONFIG_NAME", "CLICKHOUSE_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.CLICKHOUSE_INTEGRATION_CONFIG_NAME!;

test.describe.serial("Clickhouse Account Integration", () => {
  let integrationExists = false;

  test("Check if Clickhouse integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.clickhouseBtn.click();
      },
      configName,
      serviceName: "Clickhouse",
      statusFilterId: "clickhouse-status-filter",
    });
  });

  test("Delete Clickhouse integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "Clickhouse integration not present — nothing to delete");
    const locators = await navigateToDatabaseTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.clickhouseBtn.click();
      },
      configName,
      serviceName: "Clickhouse",
      statusFilterId: "clickhouse-status-filter",
    });
  });

  test("Add Clickhouse Account Integration", async ({ page }) => {
    test.setTimeout(180000);
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);

    await locators.clickhouseBtn.click();
    await locators.addClickhouseAccountBtn.click();

    await locators.clickhouseConfigNameInput.fill(configName);
    await locators.clickhouseAccountIdDropdown.click();
    await locators.clickhouseAccountIdOption(process.env.CLUSTER!).first().click();
    await locators.clickhouseAccountIdDropdown.press("Escape");
    await locators.clickhouseK8sSecretInput.fill(process.env.CLICKHOUSE_SECRET!);

    const connectionOk = await testConnection(page, {
      testConnectionBtn: locators.clickhouseTestConnectionBtn,
      successToast: locators.clickhouseTestConnectionSuccessToast,
      serviceName: "Clickhouse",
      saveBtn: locators.saveBtn,
      operationNames: ["TestIntegrationConnectionConfig"],
      timeout: 90000,
      skipOnBackendError: true,
      checkDataErrors: true,
    });

    if (!connectionOk) return;

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.clickhouseSuccessToast,
      testName: "Add Clickhouse Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: [
        `account '${process.env.CLUSTER}' already has a 'clickhouse' integration ('clickhouse-test'); only one 'clickhouse' integration per account is supported — edit the existing one or remove it before adding another`,
        `integration config name '${configName}' already exists for this integration type`,
      ],
    });
  });

  test("Disable Clickhouse integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);
    await locators.clickhouseBtn.click();
    await disableIntegration(page, { configName, serviceName: "Clickhouse" });
  });

  test("Enable Clickhouse integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);
    await locators.clickhouseBtn.click();
    await enableIntegration(page, {
      configName,
      serviceName: "Clickhouse",
      statusFilterId: "clickhouse-status-filter",
    });
  });
});
