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
  dryRunAction,
} from "../workflowHelper";

const WORKFLOW_JSON_TEMPLATE = {
  definition: {
    version: "v1",
    timeout: "300s",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "tickets_resolve",
        type: "tickets.resolve",
        params: {
          resolution: "All good",
          ticket_id: process.env.PAGER_DUTY_TICKET_ID ?? "",
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

test("Automation workflow Resolve ticket", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Resolve ticket");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_resolve.click();

  await page.locator("div.MuiDialog-container").waitFor({ state: "visible", timeout: 15000 });

  const pagerDutyName = process.env.PAGER_DUTY_NAME ?? "";
  const integrationBtn = page.locator("div.MuiDialog-container")
    .getByRole("button", { name: /Incident management integration/i });
  await integrationBtn.waitFor({ state: "visible", timeout: 15000 });
  await integrationBtn.click();
  await page.locator("div.MuiDialog-container").getByText(pagerDutyName, { exact: true }).click();
  console.log(`Selected PagerDuty integration: ${pagerDutyName}`);

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Resolve ticket");
});
