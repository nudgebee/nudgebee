import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = ["PAGER_DUTY_NAME", "PAGER_DUTY_EMAIL", "PAGER_DUTY_TOKEN"];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add PagerDuty Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);

  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  await expect(locators.ticketingTab).toBeVisible({ timeout: 15000 });
  await locators.ticketingTab.click();

  // verify PagerDuty integration section
  await locators.pagerDutyBtn.click();
  await locators.addPagerDutyAccountBtn.click();

  // Fill required PagerDuty integration details
  await locators.pagerDutyNameInput.fill(process.env.PAGER_DUTY_NAME!);
  await locators.pagerDutyEmailInput.fill(process.env.PAGER_DUTY_EMAIL!);
  await locators.pagerDutyApiTokenInput.fill(process.env.PAGER_DUTY_TOKEN!);

  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.saveBtn.click();

        const successToast = locators.pagerDutySuccessToast;
        const duplicateErrorToast = locators.pagerDutyDuplicateErrorToast;

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
              name: process.env.PAGER_DUTY_NAME!,
              exact: true,
            }),
          ).toBeVisible({ timeout: 10000 });
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
        testName: "Add PagerDuty Account Integration",
        operationNames: ["AddIntegrations"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
