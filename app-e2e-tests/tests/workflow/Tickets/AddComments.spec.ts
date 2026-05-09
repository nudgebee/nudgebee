import { test } from "@playwright/test";
import { WorkflowLocators } from "../workflowlocators";
import config from "../../../e2e-config.json";
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
        id: "tickets_add_comment",
        type: "tickets.add_comment",
        params: {
          comment: "Workflow testing ",
          project_key: "VanshikaR7/Testing_Purpose",
          ticket_id: "405",
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
  status: "DRAFT",
};

test("Automation workflow Add Comment", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Add Comment");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_tickets_add_comment.click();
  await selectCluster(page, locators, config.cluster_name);
  await selectIntegration(page, locators, process.env.GITHUB_NAME ?? "");
  // add_comment panel uses a different project key label than other ticket actions
  const projectKeyInput = page.getByRole("textbox", { name: "Project key (e.g. 'PROJ' for Jira, 'owner/repo' for GitHub/GitLab)" });
  await projectKeyInput.fill("VanshikaR7/Testing_Purpose");
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation-> Action-> Add Comment");
});
