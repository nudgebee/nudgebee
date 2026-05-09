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
  await selectCluster(page, locators, config.cluster_name);
  await selectIntegration(page, locators, process.env.GITHUB_NAME ?? "");
  await locators.project_id_input.click();
  await page.getByRole("option", { name: "Testing_Purpose" }).click();
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Create ticket Github");
});
