import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToReposTab } from "./util";

const requiredEnv = [
  "GITLAB_INTEGRATION_NAME",
  "GITLAB_HOSTURL",
  "GITLAB_USERNAME",
  "GITLAB_TOKEN",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add GitLab Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToReposTab(page);

  await locators.gitlabBtn.click();
  await locators.addGitlabAccountBtn.click();

  await locators.gitlabNameInput.fill(process.env.GITLAB_INTEGRATION_NAME!);
  await locators.gitlabHostUrlInput.fill(process.env.GITLAB_HOSTURL!);
  await locators.gitlabUsernameInput.fill(process.env.GITLAB_USERNAME!);
  await locators.gitlabTokenInput.fill(process.env.GITLAB_TOKEN!);

  let isDuplicateAccount = false;
  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.gitlabsavebutton.click();

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
            page.getByRole("cell", { name: process.env.GITLAB_INTEGRATION_NAME!, exact: true }),
          ).toBeVisible();
        } else if (await duplicateErrorToast.isVisible()) {
          console.log("DUPLICATE:", (await duplicateErrorToast.innerText()).trim());
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
      { testName: "Add GitLab Account Integration", operationNames: ["RepoIntegration"] },
    );
  } catch (error) {
    if (!isDuplicateAccount) throw error;
  }
});
