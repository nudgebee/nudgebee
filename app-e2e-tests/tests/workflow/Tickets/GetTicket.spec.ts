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
    timeout: "5m",
    inputs: [],
    output: {},
    tasks: [
      {
        id: "tickets_get",
        type: "tickets.get",
        params: {
          project_key: process.env.GITHUB_PROJECT_KEY ?? "",
          ticket_id: process.env.GITHUB_TICKET_ID ?? "",
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

  await page.locator("div.MuiDialog-container").waitFor({ state: "visible", timeout: 15000 });

  const githubName = process.env.GITHUB_NAME ?? "";
  const integrationBtn = page.locator("div.MuiDialog-container")
    .getByRole("button", { name: /Ticket integration/i });
  await integrationBtn.waitFor({ state: "visible", timeout: 15000 });
  await integrationBtn.click();
  await page.locator("div.MuiDialog-container").getByText(githubName, { exact: true }).click();
  console.log(`Selected GitHub integration: ${githubName}`);

  const projectKeySelectTab = page.locator("div.MuiDialog-container")
    .locator(".MuiToggleButtonGroup-grouped")
    .filter({ hasText: "Select" })
    .last();
  await projectKeySelectTab.scrollIntoViewIfNeeded();
  await projectKeySelectTab.click();
  await page.waitForTimeout(500);

  const projectKeyDropdown = page.locator("div.MuiDialog-container").getByText("Select project", { exact: false }).last();
  await projectKeyDropdown.waitFor({ state: "visible", timeout: 10000 });
  await projectKeyDropdown.click();

  const projectSearchInput = page.getByPlaceholder("Select project");
  await projectSearchInput.waitFor({ state: "visible", timeout: 10000 });
  await projectSearchInput.fill(process.env.GITHUB_PROJECT_KEY ?? "");
  await page.locator('[role="option"]').filter({ hasText: process.env.GITHUB_PROJECT_KEY ?? "" }).first().click();
  console.log(`Selected Project Key: ${process.env.GITHUB_PROJECT_KEY}`);

  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Get ticket Github");
});
