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

const requiredEnv = ["POSTGRES_NAME", "POSTGRES_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.POSTGRES_NAME!;

test.describe.serial("Postgresql Account Integration", () => {
  let integrationExists = false;

  test("Check if Postgresql integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.postgresqlBtn.click();
      },
      configName,
      serviceName: "Postgresql",
      statusFilterId: "postgres-status-filter",
    });
  });

  test("Delete Postgresql integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "Postgresql integration not present — nothing to delete");
    const locators = await navigateToDatabaseTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.postgresqlBtn.click();
      },
      configName,
      serviceName: "Postgresql",
      statusFilterId: "postgres-status-filter",
    });
  });

  test("Add Postgresql Account Integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);

    await locators.postgresqlBtn.click();
    await locators.addPostgresqlAccountBtn.click();

    await locators.postgresqlConfigNameInput.fill(configName);
    await locators.postgresqlAccountIdDropdown.click();
    await locators.postgresqlAccountIdOption(process.env.CLUSTER!).first().click();
    await locators.postgresqlAccountIdDropdown.press("Escape");
    await locators.postgresqlK8sSecretInput.fill(process.env.POSTGRES_SECRET!);

    await testConnection(page, {
      testConnectionBtn: locators.postgresqlTestConnectionBtn,
      successToast: locators.postgresqlTestConnectionSuccessToast,
      serviceName: "Postgresql",
      saveBtn: locators.saveBtn,
      operationNames: ["TestIntegrationConnectionConfig"],
      checkDataErrors: true,
    });

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.postgresqlSuccessToast,
      testName: "Add Postgresql Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: [
        "already has a 'postgresql' integration",
        `integration config name '${configName}' already exists for this integration type`,
      ],
    });
  });

  test("Disable Postgresql integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);
    await locators.postgresqlBtn.click();
    await disableIntegration(page, { configName, serviceName: "Postgresql" });
  });

  test("Enable Postgresql integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToDatabaseTab(page);
    await locators.postgresqlBtn.click();
    await enableIntegration(page, {
      configName,
      serviceName: "Postgresql",
      statusFilterId: "postgres-status-filter",
    });
  });
});
