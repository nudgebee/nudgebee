import { test } from "@playwright/test";
import { WorkflowLocators } from "../workflowlocators";
import config from "../../../e2e-config.json";
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
    timeout: "5m",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "tickets_get",
        type: "tickets.get",
        params: {
          account_id: process.env.GITHUB_ACCOUNT_ID ?? "",
          integration_id: process.env.GITHUB_INTEGRATION_ID ?? "",
          project_key: "VanshikaR7/Testing_Purpose",
          ticket_id: "405",
        },
      },
    ],
    triggers: [{ type: "manual", params: {} }],
    retry_policy: {
      maximum_attempts: 3,
      initial_interval: "1s",
      maximum_interval: "1m",
      backoff_coefficient: 2,
    },
  },
  tags: {},
  status: "ACTIVE",
};

test("Automation workflow Get ticket Github", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Get Ticket");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_get.click();
  await selectCluster(page, locators, config.cluster_name);
  await locators.ticket_id_input.fill("405");
  await selectIntegration(page, locators, process.env.GITHUB_NAME ?? "");
  await locators.project_key_input.fill("VanshikaR7/Testing_Purpose");
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Get ticket Github");
});
