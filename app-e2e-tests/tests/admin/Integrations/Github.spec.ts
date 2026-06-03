import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToReposTab, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = ["GITHUB_NAME", "GITHUB_USERNAME", "GITHUB_TOKEN"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test.describe.configure({ mode: "serial" });

test("Add Github Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToReposTab(page);

  await locators.githubBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.githubBtn.click();

  await locators.addGithubAccountBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.addGithubAccountBtn.click();

  await locators.githubMethodUserTokenRadio.waitFor({ state: "visible", timeout: 15000 });
  await locators.githubMethodUserTokenRadio.click();

  await locators.githubNameInput.waitFor({ state: "visible", timeout: 10000 });
  await locators.githubNameInput.fill(process.env.GITHUB_NAME!);

  await locators.githubUsernameInput.waitFor({ state: "visible", timeout: 10000 });
  await locators.githubUsernameInput.fill(process.env.GITHUB_USERNAME!);

  await locators.githubTokenInput.waitFor({ state: "visible", timeout: 10000 });
  await locators.githubTokenInput.fill(process.env.GITHUB_TOKEN!);

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.githubSaveBtn,
    successToast: locators.githubSuccessToast,
    testName: "Add Github Account Integration",
    operationNames: [],
    ignoreErrorMessages: ["already exists", "already has", "uniqueness violation"],
    onSuccess: async () => {
      await expect(
        page.getByRole("cell", { name: process.env.GITHUB_NAME!, exact: true }),
      ).toBeVisible();
    },
  });
});

test("Test Github Connection", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToReposTab(page);

  await locators.githubBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.githubBtn.click();

  const githubRow = page
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name: process.env.GITHUB_NAME!, exact: true }) });

  const integrationExists = await githubRow
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);

  if (!integrationExists) {
    test.skip(true, `@qa -- Github integration "${process.env.GITHUB_NAME}" not found in the table.`);
    return;
  }

  const isDisabled = await githubRow.getByText("inactive", { exact: false }).isVisible();
  if (isDisabled) {
    test.skip(true, `@qa -- Github integration "${process.env.GITHUB_NAME}" is disabled.`);
    return;
  }

  const moreBtn = githubRow
    .getByRole("button", { name: "more" })
    .or(githubRow.locator("button").last())
    .first();
  await moreBtn.waitFor({ state: "visible", timeout: 10000 });
  await moreBtn.click();

  const editMenuItem = page.getByRole("menuitem", { name: "Edit" });
  await editMenuItem.waitFor({ state: "visible", timeout: 10000 });
  await editMenuItem.click();

  await locators.githubTestConnectionBtn.waitFor({ state: "visible", timeout: 15000 });

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
      } else {
        console.log("ERROR:", await locators.githubTestConnectionErrorToast.innerText());
      }
    },
    { testName: "Test Github Connection", operationNames: [] },
  );
});
