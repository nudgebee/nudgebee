import { test, expect } from "@playwright/test";
import { WorkflowLocators } from "./workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  setWorkflowActiveAndSave,
  runWorkflowWithGraphQLValidation,
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
        "params": {
          "channel": "",
          "message": "PW Automation slack notification testing ",
          "provider": "ms_teams",
          "team_id": ""
        },
      },
    ],
    triggers: [
      {
        type: "manual",
        params: {},
      },
    ],
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
  await locators.select_team.click();
  const teamValue = process.env.MSTEAMS_GROUP_NAME ?? "";
  await page.locator('[role="option"]').filter({ hasText: teamValue }).first().click();
  await expect(locators.select_channel).toBeEnabled({ timeout: 10000 });
  await locators.select_channel.click();
  const channelValue = process.env.MSTEAMS_AUTOMATION_CHANNEL ?? "";
  await page.getByPlaceholder("Select channel").fill(channelValue);
  await page.locator('[role="option"]').filter({ hasText: channelValue }).first().waitFor({ state: "visible", timeout: 10000 });
  await page.locator('[role="option"]').filter({ hasText: channelValue }).first().click();
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow Ms-teams Notification");
});
