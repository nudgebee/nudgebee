import { test } from "@playwright/test";
import {
  navigateToInMemoryTab,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
  selectAccountFromDropdown,
} from "./util";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

const requiredEnv = ["REDIS_INTEGRATION_CONFIG_NAME", "REDIS_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.REDIS_INTEGRATION_CONFIG_NAME!;

test.describe.serial("Redis Account Integration", () => {
  let integrationExists = false;

  test("Check if Redis integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToInMemoryTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.redisBtn.click();
      },
      configName,
      serviceName: "Redis",
      statusFilterId: "redis-status-filter",
    });
  });

  test("Delete Redis integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "Redis integration not present — nothing to delete");
    const locators = await navigateToInMemoryTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.redisBtn.click();
      },
      configName,
      serviceName: "Redis",
      statusFilterId: "redis-status-filter",
    });
  });

  test("Add Redis Account Integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToInMemoryTab(page);

    await locators.redisBtn.click();
    await locators.addRedisAccountBtn.click();

    await locators.redisConfigNameInput.fill(configName);
    await selectAccountFromDropdown(page, locators.redisAccountIdDropdown, process.env.CLUSTER!);
    await locators.redisK8sSecretInput.fill(process.env.REDIS_SECRET!);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.redisTestConnectionBtn.click();
        await locators.saveBtn.waitFor({ state: "attached" });
        await locators.saveBtn.isEnabled();
        console.log("Test connection SUCCESS: save button is now enabled");
      },
      {
        testName: "Add Redis Account Integration - Test Connection",
        operationNames: ["TestIntegrationConnectionConfig"],
        checkDataErrors: true,
      },
    );

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.redisSuccessToast,
      testName: "Add Redis Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: [
        "already has a 'redis' integration",
        `integration config name '${configName}' already exists for this integration type`,
      ],
    });
  });

  test("Disable Redis integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToInMemoryTab(page);
    await locators.redisBtn.click();
    await disableIntegration(page, { configName, serviceName: "Redis" });
  });

  test("Enable Redis integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToInMemoryTab(page);
    await locators.redisBtn.click();
    await enableIntegration(page, {
      configName,
      serviceName: "Redis",
      statusFilterId: "redis-status-filter",
    });
  });
});
