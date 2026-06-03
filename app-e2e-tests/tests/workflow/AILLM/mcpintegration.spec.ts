import { test } from "@playwright/test";
import { WorkflowLocators } from "../workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  setWorkflowActiveAndSave,
  runWorkflowWithGraphQLValidation,
  configureMcpIntegrationAction,
  dryRunAction,
  closeActionPanel,
  configureNotificationsImSlack,
} from "../workflowHelper";

const SLACK_CHANNEL = process.env["SLACK-CHANNEL"]!;
const MCP_INTEGRATION = process.env.MCP_INTEGRATION_CONFIG_NAME!;

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "llm_mcp_call",
        type: "llm.mcp_call",
        params: {
          arguments: {
            question: "How do I authenticate with the API?",
            repoName: "openai/openai-python",
          },
          connection_mode: "integration",
          tool_name: "ask_question",
        },
        timeout: "5m",
      },
      {
        id: "notifications_im",
        type: "notifications.im",
        params: {
          channel: SLACK_CHANNEL,
          message:
            "*This is automation MCP(Integration) testing*\n{{ Tasks['llm_mcp_call'].output.content[0].text }}",
          provider: "slack",
          team_id: "",
        },
        depends_on: ["llm_mcp_call"],
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

test("Automation workflow MCP Integration", async ({ page }) => {
  test.setTimeout(300000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("MCP Integration");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);

  await locators.action_llm_mcp_call.click();
  await configureMcpIntegrationAction(page, MCP_INTEGRATION);
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await locators.action_notifications_im.click();
  await configureNotificationsImSlack(page, SLACK_CHANNEL);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow MCP Integration");
});
