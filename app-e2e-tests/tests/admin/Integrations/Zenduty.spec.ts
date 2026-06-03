import { test, expect } from "@playwright/test";
import { navigateToTicketingTab, saveAndHandleAlreadyExists } from "./util";

test.skip("Add Zenduty Account Integration", async ({ page }) => {
  const locators = await navigateToTicketingTab(page);

  await locators.zendutyBtn.click();
  await locators.addZendutyAccountBtn.click();

  await locators.zendutyNameInput.fill(process.env.ZENDUTY_INTEGRATION_NAME!);
  await locators.zendutyEmailInput.fill(process.env.ZENDUTY_EMAIL!);
  await locators.zendutyApiTokenInput.fill(process.env.ZENDUTY_API_TOKEN!);

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.saveBtn,
    successToast: locators.zendutySuccessToast,
    testName: "Add Zenduty Account Integration",
    operationNames: ["CreateTicketIntegration"],
    ignoreErrorMessages: ["already exists", "already has"],
    onSuccess: async () => {
      await expect(
        page.getByRole("cell", { name: process.env.ZENDUTY_INTEGRATION_NAME!, exact: true }),
      ).toBeVisible();
    },
  });
});
