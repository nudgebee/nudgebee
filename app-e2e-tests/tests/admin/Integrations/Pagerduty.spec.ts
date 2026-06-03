import { test, expect } from "@playwright/test";
import { navigateToTicketingTab, testConnection, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = ["PAGER_DUTY_NAME", "PAGER_DUTY_EMAIL", "PAGER_DUTY_TOKEN"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add PagerDuty Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToTicketingTab(page);

  await locators.pagerDutyBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.pagerDutyBtn.click();

  await locators.addPagerDutyAccountBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.addPagerDutyAccountBtn.click();

  await locators.pagerDutyNameInput.waitFor({ state: "visible", timeout: 10000 });
  await locators.pagerDutyNameInput.fill(process.env.PAGER_DUTY_NAME!);
  await locators.pagerDutyEmailInput.fill(process.env.PAGER_DUTY_EMAIL!);
  await locators.pagerDutyApiTokenInput.fill(process.env.PAGER_DUTY_TOKEN!);

  await testConnection(page, {
    testConnectionBtn: locators.pagerDutyTestConnectionBtn,
    successToast: locators.pagerDutyTestConnectionSuccessToast,
    serviceName: "PagerDuty",
    saveBtn: locators.pagerDutySaveButton,
    operationNames: ["TestTicketConnectionByConfig"],
  });

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.pagerDutySaveButton,
    successToast: locators.pagerDutySuccessToast,
    testName: "Add PagerDuty Account Integration",
    operationNames: ["CreateTicketIntegration"],
    ignoreErrorMessages: ["already exists", "already has"],
    inlineError: locators.pagerDutyInlineError,
    onSuccess: async () => {
      await expect(
        page.getByRole("cell", { name: process.env.PAGER_DUTY_NAME!, exact: true }),
      ).toBeVisible({ timeout: 10000 });
    },
  });
});
