import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = ["POSTGRES_NAME", "POSTGRES_SECRET"];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add Postgresql Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  // verify Postgresql integration section
  await expect(locators.databaseTab).toBeVisible({ timeout: 15000 });
  await locators.databaseTab.click();

  await locators.postgresqlBtn.click();
  await locators.addPostgresqlAccountBtn.click();

  await locators.postgresqlConfigNameInput.fill(process.env.POSTGRES_NAME!);
  await locators.postgresqlAccountIdDropdown.click();
  await locators
    .postgresqlAccountIdOption(process.env.CLUSTER!)
    .first()
    .click();
  await locators.postgresqlAccountIdDropdown.press("Escape");
  await locators.postgresqlK8sSecretInput.fill(process.env.POSTGRES_SECRET!);

  // Test connection and verify save button becomes enabled (connection verified)
  await locators.postgresqlTestConnectionBtn.click();
  await expect(locators.saveBtn).toBeEnabled();
  console.log("Test connection SUCCESS: save button is now enabled");

  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.saveBtn.click();

        const successToast = locators.postgresqlSuccessToast;
        const duplicateErrorToast = locators.postgresqlDuplicateErrorToast;

        await successToast
          .or(duplicateErrorToast)
          .first()
          .waitFor({ state: "visible", timeout: 30000 });

        if (await successToast.isVisible()) {
          console.log("SUCCESS:", await successToast.innerText());
          await expect(successToast).toBeVisible();
        } else if (await duplicateErrorToast.isVisible()) {
          console.log(
            "DUPLICATE:",
            (await duplicateErrorToast.innerText()).trim(),
          );
          isDuplicateAccount = true;
          throw new Error("Duplicate account detected");
        } else {
          console.error("FAILED: No success or error toast found");
          throw new Error("Neither success nor duplicate error appeared");
        }
      },
      {
        testName: "Add Postgresql Account Integration",
        operationNames: ["AddIntegrations"],
        ignoreErrorMessages: ["already exists"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
