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
        id: "tickets_assign",
        type: "tickets.assign",
        params: {
          assignee: process.env.GITHUB_USERNAME ?? "",
          project_key: process.env.GITHUB_PROJECT_KEY ?? "",
          ticket_id: process.env.GITHUB_TICKET_ID ?? "",
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

test("Automation workflow Github Ticket Assign", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Ticket Assign");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_assign.click();
  await selectCluster(page, locators, process.env.CLUSTER ?? "");
  await page.getByRole("textbox", { name: "Ticket ID" }).fill(process.env.GITHUB_TICKET_ID ?? "");
  await selectIntegration(page, locators, process.env.GITHUB_NAME ?? "");

  await page.getByRole("textbox", { name: "Assignee" }).fill(process.env.GITHUB_USERNAME ?? "");

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
  console.log(`Selected project: ${process.env.GITHUB_PROJECT_KEY}`);
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Github Ticket Assign");
});
