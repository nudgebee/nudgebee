import { test } from "@playwright/test";
import { WorkflowLocators } from "../workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  setWorkflowActiveAndSave,
  runWorkflowWithGraphQLValidation,
  selectCluster,
  selectIntegration,
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
        id: "tickets_transition",
        type: "tickets.transition",
        params: {
          project_key: process.env.JIRA_PROJECT_KEY ?? "",
          status: process.env.JIRA_TRANSITION_STATUS ?? "",
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

test("Automation workflow Ticket Transition", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Ticket Transition");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_transition.click();
  await selectCluster(page, locators, process.env.CLUSTER ?? "");
  await selectIntegration(page, locators, process.env.JIRA_NAME ?? "");
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Ticket Transition");
});
