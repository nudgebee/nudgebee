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
const KUBECTL_NAMESPACE = process.env.KUBECTL_NAMESPACE!;

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "llm_nubi",
        type: "llm.nubi",
        params: {
          message: `get me the list of active pods from ${KUBECTL_NAMESPACE} namespace`,
        },
        timeout: "5m",
      },
      {
        id: "notifications_im",
        type: "notifications.im",
        params: {
          channel: SLACK_CHANNEL,
          message:
            "*Completed testing of action Action Details - Im*\n{{ Tasks['llm_nubi'].output.session_id }} {{ Tasks['llm_nubi'].output.data }}",
          provider: "slack",
          team_id: "",
        },
        depends_on: ["llm_nubi"],
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

test("Automation workflow LLM Nubi", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("LLM Nubi");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);

  await locators.action_notifications_im.click();
  await configureNotificationsImSlack(page, SLACK_CHANNEL);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow LLM Nubi");
});
