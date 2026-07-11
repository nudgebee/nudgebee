import { test, expect } from "@playwright/test";
import { WorkflowLocators } from "./workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  runWorkflowWithGraphQLValidation,
  deleteCreatedWorkflow,
  dryRunAction,
  closeActionPanel,
} from "./workflowHelper";

const SLACK_CHANNEL = process.env["SLACK-CHANNEL"]!;

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "notifications_im",
        type: "notifications.im",
        params: {
          channel: "",
          message: "PW Automation Slack notification testing",
          provider: "slack",
          team_id: "",
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

test("Automation workflow Slack Notification", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Slack Notification testing");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_notifications_im.click();

  const dialog = page.locator("div.MuiDialog-container");
  await dialog.waitFor({ state: "visible", timeout: 15000 });
  await expect(dialog.getByRole("button", { name: "Slack", exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: /Select channel/ }).click();
  await page.getByPlaceholder("Select channel").fill(SLACK_CHANNEL);
  await page.locator('[role="option"]').filter({ hasText: SLACK_CHANNEL }).first().click();
  console.log(`Selected Slack channel: ${SLACK_CHANNEL}`);

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow Slack Notification");

  await deleteCreatedWorkflow(page, locators, workflowName);
});
