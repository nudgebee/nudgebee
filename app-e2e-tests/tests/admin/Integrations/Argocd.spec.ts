import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = [
  "ARGOCD_INTEGRATION_CONFIG_NAME",
  "ARGOCD_SECRET",
  "ARGOCD_SERVER",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add Argocd Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  // verify Argocd integration section
  await expect(locators.cicdTab).toBeVisible({ timeout: 15000 });
  await locators.cicdTab.click();

  await locators.argocdBtn.click();
  await locators.addArgocdAccountBtn.click();

  // Fill the Argocd integration form
  await locators.argocdConfigNameInput.fill(
    process.env.ARGOCD_INTEGRATION_CONFIG_NAME!,
  );
  await locators.argocdServerInput.fill(process.env.ARGOCD_SERVER!);
  await locators.argocdK8sSecretInput.fill(process.env.ARGOCD_SECRET!);
  await locators.argocdAccountIdDropdown.click();
  await locators.argocdAccountIdOption(process.env.CLUSTER!).first().click();
  await locators.argocdAccountIdDropdown.press("Escape");

  // Test connection and verify save button becomes enabled (connection verified)
  await locators.argocdTestConnectionBtn.click();
  await expect(locators.saveBtn).toBeEnabled();
  console.log("Test connection SUCCESS: save button is now enabled");

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveBtn.click();

      const successToast = locators.argocdSuccessToast;
      const errorToast = locators.genericErrorToast.first();

      await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 60000 });

      if (await successToast.isVisible()) {
        const toastText = await successToast
          .innerText()
          .catch(() => "Argocd account created successfully");
        console.log("SUCCESS:", toastText);
        await expect(
          locators.getIntegrationByName(
            process.env.ARGOCD_INTEGRATION_CONFIG_NAME!,
          ),
        ).toBeVisible();
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
        throw new Error("Neither success nor error toast appeared within 60s");
      }
    },
    {
      testName: "Add Argocd Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists"],
    },
  );
});
