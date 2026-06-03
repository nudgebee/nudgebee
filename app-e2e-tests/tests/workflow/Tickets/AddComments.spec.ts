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

  await page.getByRole("button", { name: /Tickets Add Comment/i }).click();
  await locators.dialog.waitFor({ state: "visible", timeout: 15000 });

  const clusterName = process.env.CLUSTER ?? "";
  await locators.account_id_input.waitFor({ state: "visible", timeout: 10000 });
  await locators.account_id_input.click();
  await locators.account_id_input.fill(clusterName);
  await page.getByRole("option", { name: clusterName }).click();
  console.log(`Selected Account Id: ${clusterName}`);

  const githubName = process.env.GITHUB_NAME ?? "";
  await locators.integrationIdDropdown.waitFor({ state: "visible", timeout: 10000 });
  await locators.integrationIdDropdown.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(githubName);
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: githubName }).first().click();
  console.log(`Selected Integration: ${githubName}`);

  await locators.projectKeyDropdown.waitFor({ state: "visible", timeout: 10000 });
  await locators.projectKeyDropdown.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(process.env.GITHUB_PROJECT_KEY ?? "");
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: process.env.GITHUB_PROJECT_KEY ?? "" }).first().click();
  console.log(`Selected Project Key: ${process.env.GITHUB_PROJECT_KEY}`);

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Add Comment GitHub");
});
