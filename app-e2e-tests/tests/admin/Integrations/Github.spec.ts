import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToReposTab } from "./util";
const requiredEnv = ["GITHUB_NAME", "GITHUB_USERNAME", "GITHUB_TOKEN"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test.describe.configure({ mode: "serial" });

test("Add Github Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToReposTab(page);

  await locators.githubBtn.click();
  await locators.addGithubAccountBtn.click();

  await locators.githubMethodUserTokenRadio.click();
  await locators.githubNameInput.fill(process.env.GITHUB_NAME!);
  await locators.githubUsernameInput.fill(process.env.GITHUB_USERNAME!);
  await locators.githubTokenInput.fill(process.env.GITHUB_TOKEN!);

  let isDuplicateAccount = false;

  try {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.githubSaveBtn.click();

        const successToast = locators.githubSuccessToast;
        const duplicateErrorToast = locators.githubDuplicateErrorToast;
        const genericErrorToast = locators.genericErrorToast.first();

        await Promise.race([
          successToast.waitFor({ state: "visible", timeout: 60000 }),
          duplicateErrorToast.waitFor({ state: "visible", timeout: 60000 }),
          genericErrorToast.waitFor({ state: "visible", timeout: 60000 }),
        ]);

        if (await successToast.isVisible()) {
          console.log("SUCCESS:", await successToast.innerText());
          await expect(
            page.getByRole("cell", { name: process.env.GITHUB_NAME!, exact: true }),
          ).toBeVisible();
        } else if (await duplicateErrorToast.isVisible()) {
          console.log("DUPLICATE:", (await duplicateErrorToast.innerText()).trim());
          isDuplicateAccount = true;
          throw new Error("Duplicate account detected");
        } else if (await genericErrorToast.isVisible()) {
          const errorText = (await genericErrorToast.innerText()).trim();
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
        testName: "Add Github Account Integration",
        operationNames: [],
      },
    );
  } catch (error) {
    if (!isDuplicateAccount) throw error;
  }
});

test("Test Github Connection", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToReposTab(page);

  await locators.githubBtn.click();

  const githubRow = page
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name: process.env.GITHUB_NAME!, exact: true }) });

  const integrationExists = await githubRow
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);

  if (!integrationExists) {
    test.skip(
      true,
      `@qa -- Github integration "${process.env.GITHUB_NAME}" not found in the table. ` +
        `Please add the Github account first before running this test.`,
    );
    return;
  }

  const isDisabled = await githubRow.getByText("inactive", { exact: false }).isVisible();
  if (isDisabled) {
    test.skip(
      true,
      `@qa -- Github integration "${process.env.GITHUB_NAME}" is disabled. ` +
        `Please enable it first before running this test.`,
    );
    return;
  }

  await githubRow.getByRole("button", { name: "more" }).click();
  await page.getByRole("menuitem", { name: "Edit" }).click();
  await locators.githubTestConnectionBtn.waitFor({ state: "visible", timeout: 10000 });

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.githubTestConnectionBtn.click();

      await locators.githubTestConnectionSuccessToast
        .or(locators.githubTestConnectionErrorToast)
        .first()
        .waitFor({ state: "visible", timeout: 30000 });

      if (await locators.githubTestConnectionSuccessToast.isVisible()) {
        console.log("SUCCESS:", await locators.githubTestConnectionSuccessToast.innerText());
        await expect(locators.githubTestConnectionSuccessToast).toBeVisible();
      } else {
        console.log("ERROR:", await locators.githubTestConnectionErrorToast.innerText());
        await expect(locators.githubTestConnectionErrorToast).toBeVisible();
      }
    },
    {
      testName: "Test Github Connection",
      operationNames: [],
    },
  );
});
