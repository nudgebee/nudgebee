import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToTicketingTab, submitWithDuplicateHandling } from "./util";
const requiredEnv = ["JIRA_NAME", "JIRA_ACCOUNT_URL", "JIRA_USERNAME", "JIRA_TOKEN"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test.describe.configure({ mode: "serial" });

test("Add Jira Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToTicketingTab(page);

  await locators.jiraBtn.click();
  await locators.addJiraAccountBtn.click();

  await locators.jiraNameInput.fill(process.env.JIRA_NAME!);
  await locators.jiraAccountUrlInput.fill(process.env.JIRA_ACCOUNT_URL!);
  await locators.jiraUsernameInput.fill(process.env.JIRA_USERNAME!);
  await locators.jiraTokenInput.fill(process.env.JIRA_TOKEN!);

  await submitWithDuplicateHandling(page, {
    saveButton: locators.jiraSaveButton,
    successToast: locators.jiraSuccessToast,
    duplicateErrorToast: locators.jiraDuplicateErrorToast,
    testName: "Add Jira Account Integration",
    operationNames: ["CreateTicketIntegration"],
    onSuccess: async () => {
      console.log("SUCCESS:", await locators.jiraSuccessToast.innerText());
      await expect(
        page.getByRole("cell", { name: process.env.JIRA_NAME!, exact: true }),
      ).toBeVisible();
    },
  });
});

test("Test Jira Connection", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToTicketingTab(page);

  await locators.jiraBtn.click();

  const jiraRow = page
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name: process.env.JIRA_NAME!, exact: true }) });

  // Skip if the Jira integration doesn't exist yet -- SlackReporter will
  // auto-fire a skipped alert tagging @qa when test.skip() is called.
  const integrationExists = await jiraRow
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);

  if (!integrationExists) {
    test.skip(
      true,
      `@qa -- Jira integration "${process.env.JIRA_NAME}" not found in the table. ` +
        `Please add the Jira account first before running this test.`,
    );
    return;
  }

  // Skip if disabled -- "Test Connection" only appears in the three-dots menu
  // when is_active=true; a disabled integration only shows "Enable".
  // SlackReporter auto-alerts @qa on skip.
  // exact: false guards against capitalisation changes in the UI label.
  const isDisabled = await jiraRow.getByText("inactive", { exact: false }).isVisible();
  if (isDisabled) {
    test.skip(
      true,
      `@qa -- Jira integration "${process.env.JIRA_NAME}" is disabled. ` +
        `Please enable it first before running this test.`,
    );
    return;
  }

  // aria-label="more" targets the ThreeDotsMenu IconButton without relying on
  // the shared id="three-dot-menu" which is duplicated across all table rows.
  await jiraRow.getByRole("button", { name: "more" }).click();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      // Click inside the callback so the watcher is active before the
      // GraphQL request fires, eliminating the race condition.
      await page.getByRole("menuitem", { name: "Test Connection" }).click();

      await locators.jiraTestConnectionSuccessToast
        .or(locators.jiraTestConnectionErrorToast)
        .first()
        .waitFor({ state: "visible", timeout: 30000 });

      if (await locators.jiraTestConnectionSuccessToast.isVisible()) {
        console.log("SUCCESS:", await locators.jiraTestConnectionSuccessToast.innerText());
        await expect(locators.jiraTestConnectionSuccessToast).toBeVisible();
      } else {
        console.log("ERROR:", await locators.jiraTestConnectionErrorToast.innerText());
        await expect(locators.jiraTestConnectionErrorToast).toBeVisible();
      }
    },
    {
      testName: "Test Jira Connection",
      operationNames: ["TestTicketConnection"],
    },
  );
});
