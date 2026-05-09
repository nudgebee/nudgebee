import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";
import { assert } from "console";

// Validate env variables
const requiredEnv = [
  "AZURE_DISPLAY_NAME",
  "AZURE_TENANT_ID",
  "AZURE_CLIENT_ID",
  "AZURE_CLIENT_SECRET",
  "AZURE_SUBSCRIPTION_ID",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add Azure Integration", async ({ page }, testInfo) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);

  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  await expect(locators.kubernetestcloudTab).toBeVisible({ timeout: 15000 });
  await locators.kubernetestcloudTab.click();

  // Azure integration flow
  await locators.azureBtn.click();
  await locators.addAzureAccountBtn.click();

  await locators.azDisplayNameInput.fill(process.env.AZURE_DISPLAY_NAME!);
  await locators.azTenantIdInput.fill(process.env.AZURE_TENANT_ID!);
  await locators.azClientIdInput.fill(process.env.AZURE_CLIENT_ID!);
  await locators.azClientSecretInput.fill(process.env.AZURE_CLIENT_SECRET!);
  await locators.azNextToSubscriptionBtn.click();
  await locators.azDiscoverSubscriptionBtn.click();
  await expect(
    locators.getSubscriptionID(process.env.AZURE_SUBSCRIPTION_ID!),
  ).toBeVisible();
  await locators.azNextToReviewBtn.click();

  await expect(
    locators.getSubscriptionID(process.env.AZURE_SUBSCRIPTION_ID!),
  ).toBeVisible();

  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        const successToast = locators.azSuccessToast;
        const duplicateErrorToast = locators.azDuplicateErrorToast;

        const toastVisible = successToast
          .or(duplicateErrorToast)
          .first()
          .waitFor({ state: "visible", timeout: 30000 });

        await locators.azNextOnboardSubscriptionBtn.click();
        await toastVisible;

        if (await successToast.isVisible()) {
          console.log("SUCCESS: Onboarded successfully");
          await locators.azDoneBtn.click();
        } else if (await duplicateErrorToast.isVisible()) {
          console.log(
            "DUPLICATE:",
            (await duplicateErrorToast.innerText()).trim(),
          );
          isDuplicateAccount = true;
          throw new Error("Duplicate account detected");
        } else {
          throw new Error("Neither success nor duplicate error appeared");
        }
      },
      {
        testName: testInfo.title,
        operationNames: ["AzureBulkOnboard"],
        instantSlackNotification: true,
        ignoreErrorMessages: ["already exists"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
