import { test, expect } from "@playwright/test";
import { WorkflowLocators } from "./workflowlocators";
import {
  generateWorkflowName,
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
} from "./workflowHelper";

test("Workflow Server-Side Validation and Interception", async ({ page }) => {
  test.setTimeout(120000);

  const locators = new WorkflowLocators(page);
  const workflowName = generateWorkflowName("Server Validation testing");

  const invalidWorkflowJson = {
    name: workflowName,
    definition: {
      version: "v1",
      timeout: "",
      inputs: [],
      output: {},
      tasks: [],
      triggers: [
        {
          type: "schedule",
          params: {
            cron: "bad cron format",
          },
        },
      ],
    },
    tags: {},
    status: "PAUSED",
  };

  // 1. Navigate to the new workflow page and paste the invalid workflow JSON
  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, invalidWorkflowJson);

  // 2. Click the Validate button
  const validateBtn = page.locator("#workflow-validate-btn");
  await validateBtn.waitFor({ state: "visible", timeout: 15000 });
  await validateBtn.click();

  // 3. Verify validation error toast message appears
  await expect(page.locator("text=Validation failed")).toBeVisible({ timeout: 15000 });

  // 4. Verify the trigger node shows the "Server Error" badge
  const serverErrorBadge = page.locator('[data-testid="trigger-node-server-error-badge"]');
  await expect(serverErrorBadge).toBeVisible({ timeout: 15000 });
  await expect(serverErrorBadge).toHaveText("Server Error");

  // 5. Try to save the workflow and verify the save is blocked
  await locators.saveBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.saveBtn.click();

  // Verify that it still displays a validation failure and does not save
  await expect(page.locator("text=Validation failed")).toBeVisible({ timeout: 10000 });
  await expect(page.locator("text=created successfully")).not.toBeVisible();
});
