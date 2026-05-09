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
        id: "notifications_email",
        type: "notifications.email",
        params: {
          body: "Welcome to Nudgebee, really glad you're here\nHi there,\n\nYour Iteration-156 account is all set. Welcome to the hive — you're officially one of us now.\n\nNudgebee is built for SRE, CloudOps, and FinOps folks — the people who get paged at 2am, spend an hour grepping logs, fix the thing, and then have to write an RCA by morning. We built four AI agents to take the worst parts of that job off your plate: incident response, cloud cost optimisation, day-2 operations, and Kubernetes. They sit on top of what you already have — no big migration, no ripping anything out.\n\nWe promise the onboarding won't sting. Here's the fastest way to get something running on day one — you don't have to do all of these now, just start wherever makes sense:",
          recipients: ["test.user1@nudgebee.com"],
          subject: "tell me utsav",
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

test("Automation workflow Email", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Email Notification testing");
  const workflowJson = { name: workflowName, ...WORKFLOW_JSON_TEMPLATE };

  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await locators.action_notifications_email.click();
  await dryRunAction(page, locators);
  await closeActionPanel(page, locators);

  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, "Automation workflow Email");
});
