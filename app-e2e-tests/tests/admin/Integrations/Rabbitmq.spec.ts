import { test } from "@playwright/test";
import {
  navigateToMessagingQueueTab,
  testConnection,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  ensureIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";

const requiredEnv = [
  "RABBITMQ_INTEGRATION_CONFIG_NAME",
  "RABBITMQ_SECRET",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.RABBITMQ_INTEGRATION_CONFIG_NAME!;

test.describe.serial("RabbitMQ Account Integration", () => {
  let integrationExists = false;

  test("Check if RabbitMQ integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToMessagingQueueTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.rabbitmqBtn.click();
      },
      configName,
      serviceName: "RabbitMQ",
      statusFilterId: "rabbitmq-status-filter",
    });
  });

  test("Delete RabbitMQ integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "RabbitMQ integration not present — nothing to delete");
    const locators = await navigateToMessagingQueueTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.rabbitmqBtn.click();
      },
      configName,
      serviceName: "RabbitMQ",
      statusFilterId: "rabbitmq-status-filter",
    });
  });

  test("Add RabbitMQ Account Integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToMessagingQueueTab(page);

    await locators.rabbitmqBtn.click();
    await locators.addRabbitmqAccountBtn.click();

    await locators.rabbitmqAccountIdDropdown.click();
    await locators.rabbitmqAccountIdOption(process.env.CLUSTER!).first().click();
    await locators.rabbitmqAccountIdDropdown.press("Escape");
    await locators.rabbitmqConfigNameInput.fill(configName);
    await locators.rabbitmqK8sSecretInput.fill(process.env.RABBITMQ_SECRET!);

    await testConnection(page, {
      testConnectionBtn: locators.rabbitmqTestConnectionBtn,
      successToast: locators.rabbitmqTestConnectionSuccessToast,
      serviceName: "RabbitMQ",
      saveBtn: locators.saveBtn,
      operationNames: ["TestIntegrationConnectionConfig"],
      checkDataErrors: true,
    });

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.rabbitmqSuccessToast,
      testName: "Add RabbitMQ Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists", "already has"],
    });
  });

  test("Disable RabbitMQ integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToMessagingQueueTab(page);
    const present = await ensureIntegrationPresent(page, {
      openList: async () => {
        await locators.rabbitmqBtn.click();
      },
      configName,
      serviceName: "RabbitMQ",
      statusFilterId: "rabbitmq-status-filter",
      integrationKey: "rabbitmq",
    });
    test.skip(!present, "RabbitMQ integration is not Active — Slack notification sent");
    await disableIntegration(page, { configName, serviceName: "RabbitMQ" });
  });

  test("Enable RabbitMQ integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToMessagingQueueTab(page);
    const present = await ensureIntegrationPresent(page, {
      openList: async () => {
        await locators.rabbitmqBtn.click();
      },
      configName,
      serviceName: "RabbitMQ",
      statusFilterId: "rabbitmq-status-filter",
      integrationKey: "rabbitmq",
    });
    test.skip(!present, "RabbitMQ integration is not Active — Slack notification sent");
    await enableIntegration(page, {
      configName,
      serviceName: "RabbitMQ",
      statusFilterId: "rabbitmq-status-filter",
    });
  });
});
