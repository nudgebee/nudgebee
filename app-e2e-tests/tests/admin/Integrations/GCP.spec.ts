import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

const requiredEnv = [
  "GCP_DISPLAY_NAME",
  "GCP_PROJECT_ID",
  "GCP_BILLING_DATASET_NAME",
  "GCP_TABLE_NAME",
  "GCP_SERVICE_ACCOUNT_KEY",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test.skip("Add GCP Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  await expect(locators.kubernetestcloudTab).toBeVisible({ timeout: 15000 });
  await locators.kubernetestcloudTab.click();

  await locators.gcpBtn.click();
  await locators.addGcpAccountBtn.click();

  await locators.gcpDisplayNameInput.fill(process.env.GCP_DISPLAY_NAME!);

  // The GCP_SERVICE_ACCOUNT_KEY env var may arrive unquoted when GitHub Actions
  // secret escaping strips JSON quotes (e.g., {type: service_account, ...}).
  // If JSON.parse fails, reconstruct valid JSON by extracting known GCP SA fields.
  let cleanJson: string;
  const rawKey = process.env.GCP_SERVICE_ACCOUNT_KEY!;
  try {
    cleanJson = JSON.stringify(JSON.parse(rawKey), null, 2);
  } catch {
    // Known GCP service account key fields in order. private_key is special
    // because its value contains embedded newlines and dashes.
    const fields = [
      "type",
      "project_id",
      "private_key_id",
      "private_key",
      "client_email",
      "client_id",
      "auth_uri",
      "token_uri",
      "auth_provider_x509_cert_url",
      "client_x509_cert_url",
      "universe_domain",
    ];

    // Strip outer braces and trailing whitespace
    const inner = rawKey
      .replace(/^\s*\{/, "")
      .replace(/\}\s*$/, "")
      .trim();

    const obj: Record<string, string> = {};
    for (let i = 0; i < fields.length; i++) {
      const key = fields[i];
      const nextKey = fields[i + 1];
      // Capture value between "key:" and "nextKey:" (or end of string)
      const pattern = nextKey
        ? new RegExp(
            `(?:^|,\\s*)${key}\\s*:\\s*([\\s\\S]*?)(?=,\\s*${nextKey}\\s*:)`,
          )
        : new RegExp(`(?:^|,\\s*)${key}\\s*:\\s*([\\s\\S]*?)\\s*$`);
      const m = inner.match(pattern);
      if (m) {
        let val = m[1].trim().replace(/^["']|["']$/g, "");
        // private_key contains literal \n sequences that represent real newlines.
        // Convert them to actual newlines so JSON.stringify encodes them as \n.
        if (key === "private_key") {
          val = val.replace(/\\n/g, "\n");
        }
        obj[key] = val;
      }
    }

    try {
      cleanJson = JSON.stringify(obj, null, 2);
      // Verify it round-trips and has required fields
      const verify = JSON.parse(cleanJson);
      if (!verify.type || !verify.project_id || !verify.private_key) {
        throw new Error("Missing required fields after reconstruction");
      }
    } catch {
      cleanJson = rawKey;
    }
  }

  await locators.gcpServiceAccountKeyInput.fill(cleanJson);
  await locators.gcpCheckPermissionsBtn.click();
  await locators.gcpNextBtn.click();
  await locators.gcpDiscoverProjectsBtn.click();
  await expect(locators.gcpNextStep2Btn).toBeEnabled({ timeout: 60000 });
  await locators.gcpNextStep2Btn.click();
  await locators.gcpProjectIdInput.fill(process.env.GCP_PROJECT_ID!);
  await locators.gcpBillingDatasetNameInput.fill(
    process.env.GCP_BILLING_DATASET_NAME!,
  );
  await locators.gcpBillingTableNameInput.fill(process.env.GCP_TABLE_NAME!);
  await locators.gcpValidateBillingBtn.click();

  // Verify billing validation success message
  await expect(
    page.getByText("BigQuery billing table accessible.", { exact: false }),
  ).toBeVisible({ timeout: 30000 });

  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        const successToast = locators.gcpSuccessToast;
        const duplicateErrorToast = locators.gcpDuplicateErrorToast;

        const toastVisible = successToast
          .or(duplicateErrorToast)
          .first()
          .waitFor({ state: "visible", timeout: 60000 });

        await locators.gcpSaveBtn.click();
        await toastVisible;

        if (await successToast.isVisible()) {
          console.log("SUCCESS: GCP project(s) onboarded successfully");
          await expect(
            locators.getIntegrationByName(process.env.GCP_DISPLAY_NAME!),
          ).toBeVisible();
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
        testName: "Add GCP Account Integration",
        operationNames: ["GcpBulkOnboard"],
        ignoreErrorMessages: ["already exists"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
