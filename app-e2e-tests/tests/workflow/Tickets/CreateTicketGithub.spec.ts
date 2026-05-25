import { test } from "@playwright/test";
import { WorkflowLocators } from "../workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  setWorkflowActiveAndSave,
  runWorkflowWithGraphQLValidation,
  closeActionPanel,
  dryRunAction,
} from "../workflowHelper";

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "tickets_create",
        type: "tickets.create",
        params: {
          ticket_type: "bug",
          severity: "",
          title: "This is created for Automation workflow testing",
          description: "This is created for Automation workflow testing",
        },
      },
    ],
    triggers: [{ type: "manual", params: {} }],
    retry_policy: {
      maximum_attempts: 3,
      initial_interval: "1s",
      maximum_interval: "",
      backoff_coefficient: 2,
    },
  },
  tags: {},
  status: "ACTIVE",
};

test("Automation workflow Create ticket Github", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Create Ticket Github");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_create.click();
  await locators.dialog.waitFor({ state: "visible", timeout: 15000 });

  const githubName = process.env.GITHUB_NAME ?? "";
  await locators.integrationIdDropdown.waitFor({ state: "visible", timeout: 10000 });
  await locators.integrationIdDropdown.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(githubName);
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: githubName }).first().click();
  console.log(`Selected GitHub integration: ${githubName}`);

  await locators.projectKeyDropdown.waitFor({ state: "visible", timeout: 10000 });
  await locators.projectKeyDropdown.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(process.env.GITHUB_PROJECT_KEY ?? "");
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: process.env.GITHUB_PROJECT_KEY ?? "" }).first().click();
  console.log(`Selected project: ${process.env.GITHUB_PROJECT_KEY}`);

  await locators.dialogContent.evaluate((el) => (el.scrollTop = el.scrollHeight));
  await page.waitForTimeout(500);

  await locators.dialog.getByText("Select issue type", { exact: false }).first().waitFor({ state: "visible", timeout: 15000 });
  await locators.dialog.getByText("Select issue type", { exact: false }).first().click();
  await page.getByText("Issue", { exact: true }).waitFor({ state: "visible", timeout: 10000 });
  await page.getByText("Issue", { exact: true }).click();
  console.log("Selected ticket type: Issue");

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Create ticket Github");
});
