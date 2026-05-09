import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = [
  "RABBITMQ_TEST_HOST",
  "RABBITMQ_INTEGRATION_CONFIG_NAME",
  "RABBITMQ_SECRET",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add RabbitMQ Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  // verify RabbitMQ integration section
  await expect(locators.messagingQueueTab).toBeVisible({ timeout: 15000 });
  await locators.messagingQueueTab.click();

  await locators.rabbitmqBtn.click();
  await locators.addRabbitmqAccountBtn.click();

  // Fill the RabbitMQ integration form
  await locators.rabbitmqAccountIdDropdown.click();
  await locators.rabbitmqAccountIdOption(process.env.CLUSTER!).first().click();
  await locators.rabbitmqAccountIdDropdown.press("Escape");
  await locators.rabbitmqHostInput.fill(process.env.RABBITMQ_TEST_HOST!);
  await locators.rabbitmqConfigNameInput.fill(
    process.env.RABBITMQ_INTEGRATION_CONFIG_NAME!,
  );
  await locators.rabbitmqK8sSecretInput.fill(process.env.RABBITMQ_SECRET!);

  // Test connection and verify save button becomes enabled (connection verified)
  await locators.rabbitmqTestConnectionBtn.click();
  await expect(locators.saveBtn).toBeEnabled();
  console.log("Test connection SUCCESS: save button is now enabled");

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveBtn.click();

      const successToast = locators.rabbitmqSuccessToast;
      const errorToast = locators.genericErrorToast.first();

      await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 30000 });

      if (await successToast.isVisible()) {
        const toastText = await successToast
          .innerText()
          .catch(() => "RabbitMQ account created successfully");
        console.log("SUCCESS:", toastText);
      } else if (await errorToast.isVisible()) {
        const errorText = await errorToast
          .innerText()
          .catch(() => "Unknown error");
        const trimmed = errorText.trim();
        if (trimmed.includes("already exists")) {
          console.log("ALREADY_EXISTS:", trimmed);
        } else {
          console.error("FAILED:", trimmed);
          throw new Error(`Account creation failed: ${trimmed}`);
        }
      } else {
        throw new Error("Neither success nor error toast appeared within 30s");
      }
    },
    {
      testName: "Add RabbitMQ Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists"],
    },
  );
});
