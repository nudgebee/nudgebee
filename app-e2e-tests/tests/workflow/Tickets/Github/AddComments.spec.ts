import { test } from "@playwright/test";
import { WorkflowLocators } from "../../workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  runWorkflowWithGraphQLValidation,
  deleteCreatedWorkflow,
  closeActionPanel,
  dryRunAction,
} from "../../workflowHelper";

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "tickets_add_comment",
        type: "tickets.add_comment",
        params: {
          comment: "Workflow testing ",
          ticket_id: process.env.GITHUB_TICKET_ID ?? "",
          integration_id: "",
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

test("Automation workflow Add Comment", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Add Comment");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);

  await locators.action_tickets_add_comment.click();
  await locators.dialog.waitFor({ state: "visible", timeout: 15000 });

  const githubName = process.env.GITHUB_NAME ?? "";
  const integrationBtn = locators.dialog.getByRole("button", { name: /Ticket integration/i });
  await integrationBtn.waitFor({ state: "visible", timeout: 15000 });
  await integrationBtn.click();
  await locators.dialog.getByText(githubName, { exact: true }).click();
  console.log(`Selected GitHub integration: ${githubName}`);

  const projectKey = process.env.GITHUB_PROJECT_KEY ?? "";
  await locators.projectKeyDropdown.waitFor({ state: "visible", timeout: 15000 });
  await locators.projectKeyDropdown.click();
  await page.waitForTimeout(700);
  await page.keyboard.type(projectKey);
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: projectKey }).first().click();
  console.log(`Selected Project Key: ${projectKey}`);

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Add Comment GitHub");

  await deleteCreatedWorkflow(page, locators, workflowName);
});
