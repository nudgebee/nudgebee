import { test } from "@playwright/test";
import { WorkflowLocators } from "./workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  setWorkflowActiveAndSave,
  runWorkflowWithGraphQLValidation,
  dryRunAction,
  closeActionPanel,
} from "./workflowHelper";

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "300s",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "notifications_im",
        type: "notifications.im",
        params: {
          channel: "19:3CkThn1DTY3FsRW0MS1V57kc0f62T_GGjhLyKt9uQlY1@thread.tacv2",
          message: "PW Automation slack notification testing ",
          provider: "ms_teams",
          team_id: "4c38e495-c9e7-4784-99e8-512b78974eef",
        },
      },
    ],
    triggers: [{ type: "manual", params: {} }],
    retry_policy: {
      maximum_attempts: 3,
      initial_interval: "1s",
      maximum_interval: "60s",
      backoff_coefficient: 2,
    },
  },
  tags: {},
  status: "ACTIVE",
};

test("Automation workflow Ms-teams Notification", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Ms-teams Notification testing");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_notifications_im.click();
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow Ms-teams Notification");
});
