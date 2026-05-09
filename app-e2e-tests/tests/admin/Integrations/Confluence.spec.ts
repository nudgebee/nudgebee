import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = [
  "CONFLUENCE_TEST_HOST",
  "CONFLUENCE_INTEGRATION_CONFIG_NAME",
  "CONFLUENCE_TOKEN",
  "CONFLUENCE_USER_NAME",
  "CONFLUENCE_NAMESPACE",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add Confluence Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);

  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  // verify Confluence integration section
  await expect(locators.docsTab).toBeVisible({ timeout: 15000 });
  await locators.docsTab.click();

  await locators.confluenceBtn.click();
  await locators.addConfluenceAccountBtn.click();

  // Fill the Confluence integration form
  await locators.confluenceAccountIdDropdown.click();
  await locators
    .confluenceAccountIdOption(process.env.CLUSTER!)
    .first()
    .click();
  await page.keyboard.press("Escape");
  await locators.confluenceHostInput.fill(process.env.CONFLUENCE_TEST_HOST!);
  await locators.confluenceConfigNameInput.fill(
    process.env.CONFLUENCE_INTEGRATION_CONFIG_NAME!,
  );
  await locators.confluenceNamespaceInput.fill(
    process.env.CONFLUENCE_NAMESPACE!,
  );
  await locators.confluenceTokenInput.fill(process.env.CONFLUENCE_TOKEN!);
  await locators.confluenceUserNameInput.fill(
    process.env.CONFLUENCE_USER_NAME!,
  );
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveBtn.click();

      const successToast = locators.confluenceSuccessToast;
      const errorToast = locators.genericErrorToast.first();

      await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 30000 });

      if (await successToast.isVisible()) {
        const toastText = await successToast
          .innerText()
          .catch(() => "Confluence account created successfully");
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
      testName: "Add Confluence Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: ["already exists"],
    },
  );
});
