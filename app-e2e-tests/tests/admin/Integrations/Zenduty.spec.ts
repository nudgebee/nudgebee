import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = [
  "ZENDUTY_INTEGRATION_NAME",
  "ZENDUTY_EMAIL",
  "ZENDUTY_API_TOKEN",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test.skip("Add Zenduty Account Integration", async ({ page }) => {
  // Skipped: Zenduty API token expired / account limit reached.
  // Re-enable once a valid Zenduty account/token is available.
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  await expect(locators.ticketingTab).toBeVisible({ timeout: 15000 });
  await locators.ticketingTab.click();

  // verify Zenduty integration section
  await locators.zendutyBtn.click();
  await locators.addZendutyAccountBtn.click();

  // Fill required Zenduty integration details
  await locators.zendutyNameInput.fill(process.env.ZENDUTY_INTEGRATION_NAME!);
  await locators.zendutyEmailInput.fill(process.env.ZENDUTY_EMAIL!);
  await locators.zendutyApiTokenInput.fill(process.env.ZENDUTY_API_TOKEN!);
  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.saveBtn.click();

        const successToast = locators.zendutySuccessToast;
        const duplicateErrorToast = locators.zendutyDuplicateErrorToast;

        await successToast
          .or(duplicateErrorToast)
          .first()
          .waitFor({ state: "visible", timeout: 30000 });

        if (await successToast.isVisible()) {
          const toastText = await successToast
            .innerText()
            .catch(() => "Account added successfully");
          console.log("SUCCESS:", toastText);
          await expect(
            page.getByRole("cell", {
              name: process.env.ZENDUTY_INTEGRATION_NAME!,
              exact: true,
            }),
          ).toBeVisible();
        } else if (await duplicateErrorToast.isVisible()) {
          const errorText = (await duplicateErrorToast.innerText()).trim();
          console.log("DUPLICATE:", errorText);
          isDuplicateAccount = true;
          throw new Error("Duplicate account detected");
        } else {
          throw new Error("Neither success nor duplicate error appeared");
        }
      },
      {
        testName: "Add Zenduty Account Integration",
        operationNames: ["CreateTicketIntegration"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
