import { test, expect } from "@playwright/test";
import { navigateToTicketingTab, submitWithDuplicateHandling } from "./util";

// Skipped: Zenduty API token expired / account limit reached.
// Re-enable once a valid Zenduty account/token is available.
test.skip("Add Zenduty Account Integration", async ({ page }) => {
  const locators = await navigateToTicketingTab(page);

  await locators.zendutyBtn.click();
  await locators.addZendutyAccountBtn.click();

  await locators.zendutyNameInput.fill(process.env.ZENDUTY_INTEGRATION_NAME!);
  await locators.zendutyEmailInput.fill(process.env.ZENDUTY_EMAIL!);
  await locators.zendutyApiTokenInput.fill(process.env.ZENDUTY_API_TOKEN!);

  await submitWithDuplicateHandling(page, {
    saveButton: locators.saveBtn,
    successToast: locators.zendutySuccessToast,
    duplicateErrorToast: locators.zendutyDuplicateErrorToast,
    testName: "Add Zenduty Account Integration",
    operationNames: ["CreateTicketIntegration"],
    onSuccess: async () => {
      await expect(
        page.getByRole("cell", { name: process.env.ZENDUTY_INTEGRATION_NAME!, exact: true }),
      ).toBeVisible();
    },
  });
});
