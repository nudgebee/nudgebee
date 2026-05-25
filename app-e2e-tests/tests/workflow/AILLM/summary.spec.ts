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
  dryRunAction,
  closeActionPanel,
  configureNotificationsImSlack,
} from "../workflowHelper";

const SLACK_CHANNEL = process.env["SLACK-CHANNEL"]!;
const KUBECTL_NAMESPACE = process.env.KUBECTL_NAMESPACE!;
const CLUSTER = (process.env.CLUSTER ?? process.env.CLUSTER_NAME)!;

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "k8s_cli",
        type: "k8s.cli",
        params: {
          command: `kubectl get pods -n ${KUBECTL_NAMESPACE}`,
        },
      },
      {
        id: "llm_summary",
        type: "llm.summary",
        params: {
          message: "{{ Tasks['k8s_cli'].output.data }}",
        },
        depends_on: ["k8s_cli"],
      },
      {
        id: "notifications_im",
        type: "notifications.im",
        params: {
          channel: SLACK_CHANNEL,
          message:
            "*Automation testing of Action Details - Summary*\n {{ Tasks['llm_summary'].output.data }}",
          provider: "slack",
          team_id: "",
        },
        depends_on: ["llm_summary"],
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

test("Automation workflow LLM Summary", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("LLM Summary");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);

  await locators.action_k8s_cli.click();
  await selectCluster(page, locators, CLUSTER);
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await locators.action_notifications_im.click();
  await configureNotificationsImSlack(page, SLACK_CHANNEL);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow LLM Summary");
});
