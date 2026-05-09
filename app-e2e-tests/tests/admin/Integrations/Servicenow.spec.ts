import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = [
  "SERVICE_NOW_NAME",
  "SERVICE_NOW_INSTANCE_URL",
  "SERVICE_NOW_USERNAME",
  "SERVICE_NOW_PASSWORD",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add ServiceNow Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  await expect(locators.ticketingTab).toBeVisible({ timeout: 15000 });
  await locators.ticketingTab.click();

  // verify ServiceNow integration section
  await locators.serviceNowBtn.click();
  await locators.addServiceNowAccountBtn.click();

  // Fill required ServiceNow integration details
  await locators.serviceNowNameInput.fill(process.env.SERVICE_NOW_NAME!);
  await locators.serviceNowInstanceUrlInput.fill(
    process.env.SERVICE_NOW_INSTANCE_URL!,
  );
  await locators.serviceNowUsernameInput.fill(
    process.env.SERVICE_NOW_USERNAME!,
  );
  await locators.serviceNowPasswordInput.fill(
    process.env.SERVICE_NOW_PASSWORD!,
  );
  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.saveBtn.click();

        // Handle either success OR duplicate error
        const successToast = locators.serviceNowSuccessToast;
        const duplicateErrorToast = locators.serviceNowDuplicateErrorToast;

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
              name: process.env.SERVICE_NOW_NAME!,
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
        testName: "Add ServiceNow Account Integration",
        operationNames: ["AddIntegrations"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
