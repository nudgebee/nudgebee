import { test, expect } from "@playwright/test";
import { navigateToCloudTab, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = [
  "GCP_DISPLAY_NAME",
  "GCP_PROJECT_ID",
  "GCP_BILLING_DATASET_NAME",
  "GCP_TABLE_NAME",
  "GCP_SERVICE_ACCOUNT_KEY",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add GCP Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToCloudTab(page);

  await locators.gcpBtn.click();
  await locators.addGcpAccountBtn.click();

  await locators.gcpDisplayNameInput.fill(process.env.GCP_DISPLAY_NAME!);
  await locators.gcpServiceAccountKeyInput.fill(process.env.GCP_SERVICE_ACCOUNT_KEY!);
  await locators.gcpCheckPermissionsBtn.click();
  await locators.gcpNextBtn.click();
  await locators.gcpDiscoverProjectsBtn.click();
  await expect(page.getByText("project(s) found", { exact: false })).toBeVisible();
  await locators.gcpNextStep2Btn.click();
  await locators.gcpProjectIdInput.fill(process.env.GCP_PROJECT_ID!);
  await locators.gcpBillingDatasetNameInput.fill(process.env.GCP_BILLING_DATASET_NAME!);
  await locators.gcpBillingTableNameInput.fill(process.env.GCP_TABLE_NAME!);
  await locators.gcpValidateBillingBtn.click();

  await expect(
    page.getByText("BigQuery billing table accessible.", { exact: false }),
  ).toBeVisible({ timeout: 30000 });

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.gcpSaveBtn,
    successToast: locators.gcpSuccessToast,
    testName: "Add GCP Account Integration",
    operationNames: ["GcpBulkOnboard"],
    ignoreErrorMessages: ["already exists"],
    onSuccess: async () => {
      await expect(
        locators.getIntegrationByName(process.env.GCP_DISPLAY_NAME!),
      ).toBeVisible();
    },
  });
});
