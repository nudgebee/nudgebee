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
        id: "tickets_update",
        type: "tickets.update",
        params: {
          description: "Hey this is for the workflow testing only.",
          labels: ["Lorem", "runbook"],
          project_key: process.env.JIRA_PROJECT_KEY ?? "",
          severity: "High",
          ticket_id: process.env.JIRA_TICKET_ID ?? "",
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

test("Automation workflow Ticket Update", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Ticket Update");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_update.click();
  await locators.dialog.waitFor({ state: "visible", timeout: 15000 });

  await selectTicketIntegration(locators, process.env.JIRA_NAME ?? "");

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Ticket Update");

  await deleteCreatedWorkflow(page, locators, workflowName);
});
