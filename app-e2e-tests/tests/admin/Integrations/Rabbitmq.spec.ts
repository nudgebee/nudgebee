import { test } from "@playwright/test";
import { navigateToMessagingQueueTab, testConnection, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = [
  "RABBITMQ_TEST_HOST",
  "RABBITMQ_INTEGRATION_CONFIG_NAME",
  "RABBITMQ_SECRET",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

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
  await locators.rabbitmqHostInput.fill(process.env.RABBITMQ_TEST_HOST!);
  await locators.rabbitmqConfigNameInput.fill(process.env.RABBITMQ_INTEGRATION_CONFIG_NAME!);
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
