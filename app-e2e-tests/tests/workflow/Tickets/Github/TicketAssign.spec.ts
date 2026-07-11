import { test } from "@playwright/test";
import { WorkflowLocators } from "../../workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  runWorkflowWithGraphQLValidation,
  deleteCreatedWorkflow,
  selectTicketIntegration,
  selectProjectKey,
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
        id: "tickets_assign",
        type: "tickets.assign",
        params: {
          assignee: process.env.GITHUB_ASSIGNEE ?? "",
          project_key: process.env.GITHUB_PROJECT_KEY ?? "",
          ticket_id: process.env.GITHUB_TICKET_ID ?? "",
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

test("Automation workflow Github Ticket Assign", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Ticket Assign");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_assign.click();
  await locators.dialog.waitFor({ state: "visible", timeout: 15000 });

  await selectTicketIntegration(locators, process.env.GITHUB_NAME ?? "");
  await selectProjectKey(page, locators, process.env.GITHUB_PROJECT_KEY ?? "");

  const assignee = process.env.GITHUB_ASSIGNEE ?? "";
  const assigneeInput = locators.dialog.getByRole("combobox", { name: /assignee/i });
  await assigneeInput.fill(assignee);
  await assigneeInput.press("Enter");
  console.log(`Filled assignee: ${assignee}`);

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Github Ticket Assign");

  await deleteCreatedWorkflow(page, locators, workflowName);
});
