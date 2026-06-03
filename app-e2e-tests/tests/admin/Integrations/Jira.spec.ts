import { test, expect } from "@playwright/test";
import { navigateToTicketingTab, testConnection, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = ["JIRA_NAME", "JIRA_ACCOUNT_URL", "JIRA_USERNAME", "JIRA_TOKEN"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

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

  await testConnection(page, {
    testConnectionBtn: locators.jiraTestConnectionBtn,
    successToast: locators.jiraTestConnectionSuccessToast,
    serviceName: "Jira",
    saveBtn: locators.jiraSaveButton,
    operationNames: ["TestTicketConnectionByConfig"],
  });

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.jiraSaveButton,
    successToast: locators.jiraSuccessToast,
    testName: "Add Jira Account Integration",
    operationNames: ["CreateTicketIntegration"],
    ignoreErrorMessages: ["already exists", "already has"],
    inlineError: locators.jiraInlineError,
    onSuccess: async () => {
      await expect(
        page.getByRole("cell", { name: process.env.JIRA_NAME!, exact: true }),
      ).toBeVisible();
    },
  });
});
