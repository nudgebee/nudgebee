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
  configureNotificationsImSlack,
} from "../workflowHelper";

const SLACK_CHANNEL = process.env["SLACK-CHANNEL"]!;

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "llm_investigate",
        type: "llm.investigate",
        params: {
          message: "investigate the latest events coming from Prometheus",
        },
        timeout: "10m",
      },
      {
        id: "notifications_im",
        type: "notifications.im",
        params: {
          channel: SLACK_CHANNEL,
          message:
            "*This is for Action Details - Investigate*\n{{ Tasks['llm_investigate'].output.data }}",
          provider: "slack",
          team_id: "",
        },
        depends_on: ["llm_investigate"],
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

test("Automation workflow LLM Investigate", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("LLM Investigate");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);

  await locators.action_notifications_im.click();
  await configureNotificationsImSlack(page, SLACK_CHANNEL);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow LLM Investigate");
});
