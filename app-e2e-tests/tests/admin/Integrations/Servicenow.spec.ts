import { test, expect } from "@playwright/test";
import { navigateToTicketingTab, submitWithDuplicateHandling } from "./util";

const requiredEnv = [
  "SERVICE_NOW_NAME",
  "SERVICE_NOW_INSTANCE_URL",
  "SERVICE_NOW_USERNAME",
  "SERVICE_NOW_PASSWORD",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add ServiceNow Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToTicketingTab(page);

  await locators.serviceNowBtn.click();
  await locators.addServiceNowAccountBtn.click();

  await locators.serviceNowNameInput.fill(process.env.SERVICE_NOW_NAME!);
  await locators.serviceNowInstanceUrlInput.fill(process.env.SERVICE_NOW_INSTANCE_URL!);
  await locators.serviceNowUsernameInput.fill(process.env.SERVICE_NOW_USERNAME!);
  await locators.serviceNowPasswordInput.fill(process.env.SERVICE_NOW_PASSWORD!);

  await submitWithDuplicateHandling(page, {
    saveButton: locators.servicenowsavebutton,
    successToast: locators.serviceNowSuccessToast,
    duplicateErrorToast: locators.serviceNowDuplicateErrorToast,
    testName: "Add ServiceNow Account Integration",
    operationNames: ["AddIntegrations"],
    onSuccess: async () => {
      await expect(
        page.getByRole("cell", { name: process.env.SERVICE_NOW_NAME!, exact: true }),
      ).toBeVisible();
    },
  });
});
