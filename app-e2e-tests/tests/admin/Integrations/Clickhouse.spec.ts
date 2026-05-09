import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = ["CLICKHOUSE_INTEGRATION_CONFIG_NAME", "CLICKHOUSE_SECRET"];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add Clickhouse Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  // verify Clickhouse integration section
  await expect(locators.databaseTab).toBeVisible({ timeout: 15000 });
  await locators.databaseTab.click();

  await locators.clickhouseBtn.click();
  await locators.addClickhouseAccountBtn.click();

  await locators.clickhouseConfigNameInput.fill(
    process.env.CLICKHOUSE_INTEGRATION_CONFIG_NAME!,
  );
  await locators.clickhouseAccountIdDropdown.click();
  await locators
    .clickhouseAccountIdOption(process.env.CLUSTER!)
    .first()
    .click();
  await locators.clickhouseAccountIdDropdown.press("Escape");
  await locators.clickhouseK8sSecretInput.fill(process.env.CLICKHOUSE_SECRET!);

  // Test connection and verify save button becomes enabled (connection verified)
  await locators.clickhouseTestConnectionBtn.click();
  await expect(locators.saveBtn).toBeEnabled();
  console.log("Test connection SUCCESS: save button is now enabled");

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveBtn.click();

      // Handle either success OR duplicate error
      const successToast = locators.clickhouseSuccessToast;
      const errorToast = locators.genericErrorToast.first();

      await Promise.race([
        successToast.waitFor({ state: "visible", timeout: 30000 }),
        errorToast.waitFor({ state: "visible", timeout: 30000 }),
      ]);

      if (await successToast.isVisible()) {
        console.log("SUCCESS:", await successToast.innerText());
        await expect(successToast).toBeVisible();
      } else if (await errorToast.isVisible()) {
        const errorText = await errorToast.innerText();
        const trimmed = errorText.trim();
        if (trimmed.includes("already exists")) {
          console.log("ALREADY_EXISTS:", trimmed);
        } else {
          console.error("FAILED:", trimmed);
          throw new Error(`Account creation failed: ${trimmed}`);
        }
      } else {
        console.error("FAILED: No success or error toast found");
        throw new Error("Neither success nor error toast appeared");
      }
    },
    {
      testName: "Add Clickhouse Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists"],
    },
  );
});
