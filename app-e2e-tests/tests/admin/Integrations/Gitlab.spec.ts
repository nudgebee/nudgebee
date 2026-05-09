import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import "dotenv/config";

// Validate env variables
const requiredEnv = [
  "GITLAB_INTEGRATION_NAME",
  "GITLAB_HOSTURL",
  "GITLAB_USERNAME",
  "GITLAB_TOKEN",
];

for (const key of requiredEnv) {
  if (!process.env[key]) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test.skip("Add GitLab Account Integration", async ({ page }) => {
  // Skipped: GitLab token in CI env is expired (401 invalid_token).
  // Re-enable once GITLAB_TOKEN secret is refreshed.
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  console.log("Clicked on Admin button");

  await locators.integrationsTab.click();

  await expect(locators.reposTab).toBeVisible({ timeout: 15000 });
  await locators.reposTab.click();

  // verify Gitlab integration section
  await locators.gitlabBtn.click();
  await locators.addGitlabAccountBtn.click();

  // Fill required GitLab integration details
  await locators.gitlabNameInput.fill(process.env.GITLAB_INTEGRATION_NAME!);
  await locators.gitlabHostUrlInput.fill(process.env.GITLAB_HOSTURL!);
  await locators.gitlabUsernameInput.fill(process.env.GITLAB_USERNAME!);
  await locators.gitlabTokenInput.fill(process.env.GITLAB_TOKEN!);
  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.saveBtn.click();

        // Handle success, duplicate error, or generic error
        const successToast = locators.gitlabSuccessToast;
        const duplicateErrorToast = locators.gitlabDuplicateErrorToast;
        const errorToast = locators.genericErrorToast.first();

        await Promise.race([
          successToast.waitFor({ state: "visible", timeout: 60000 }),
          duplicateErrorToast.waitFor({ state: "visible", timeout: 60000 }),
          errorToast.waitFor({ state: "visible", timeout: 60000 }),
        ]);

        if (await successToast.isVisible()) {
          console.log("SUCCESS:", await successToast.innerText());
          await expect(successToast).toBeVisible();
          await expect(
            page.getByRole("cell", {
              name: process.env.GITLAB_INTEGRATION_NAME!,
              exact: true,
            }),
          ).toBeVisible();
        } else if (await duplicateErrorToast.isVisible()) {
          const errorText = (await duplicateErrorToast.innerText()).trim();
          console.log("DUPLICATE:", errorText);
          isDuplicateAccount = true;
          throw new Error("Duplicate account detected");
        } else if (await errorToast.isVisible()) {
          const errorText = (await errorToast.innerText()).trim();
          const isDuplicate =
            errorText.toLowerCase().includes("already exist") ||
            errorText.toLowerCase().includes("uniqueness violation");
          if (isDuplicate) {
            isDuplicateAccount = true;
            throw new Error("Duplicate account detected");
          }
          throw new Error(`Account creation failed: ${errorText}`);
        } else {
          throw new Error("Neither success nor error appeared");
        }
      },
      {
        testName: "Add GitLab Account Integration",
        operationNames: ["RepoIntegration"],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) {
      throw error;
    }
  }
});
