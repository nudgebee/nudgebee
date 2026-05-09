import { Page, expect, test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { WorkflowLocators } from "./workflowlocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

// Appends a random 2-digit suffix to avoid duplicate workflow name conflicts
export function generateWorkflowName(baseName: string): string {
  const suffix = String(Math.floor(Math.random() * 99) + 1).padStart(2, "0");
  return `${baseName} ${suffix}`;
}

// Logs in, navigates to /auto-pilot, and opens the workflow builder with Manual trigger selected
export async function loginAndNavigateToNewWorkflow(
  page: Page,
  locators: WorkflowLocators
): Promise<void> {
  const loginPage = new LoginPage(page);
  await loginPage.doFullLogin();
  console.log("Login complete");

  await locators.autoPilotSidenavBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.autoPilotSidenavBtn.click();
  await page.waitForURL(/\/auto-pilot/, { timeout: 15000 });

  await locators.createAutomationBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.createAutomationBtn.click();
  await locators.createNewAutomationModal.waitFor({ state: "visible", timeout: 15000 });
  await locators.makeAnAutomationCard.waitFor({ state: "visible", timeout: 10000 });
  await locators.makeAnAutomationCard.click();

  await page.waitForURL(/.*\/workflow\/new.*/, { timeout: 30000 });
  await page.getByText("How should your Automation begin?").waitFor({ state: "visible", timeout: 30000 });

  await locators.manualTriggerOption.waitFor({ state: "visible", timeout: 15000 });
  await locators.manualTriggerOption.click();
  console.log("Selected Manual Trigger");
}

// Pastes workflow JSON into the CodeMirror editor via clipboard and applies it
export async function pasteAndApplyWorkflowJson(
  page: Page,
  locators: WorkflowLocators,
  workflowJson: object
): Promise<void> {
  await locators.jsonPanelToggleBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.jsonPanelToggleBtn.click();
  await locators.codeMirrorEditor.waitFor({ state: "visible", timeout: 15000 });

  const jsonContent = JSON.stringify(workflowJson, null, 2);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.evaluate(async (text) => {
    await navigator.clipboard.writeText(text);
  }, jsonContent);
  await locators.codeMirrorEditor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Control+V");

  await locators.applyJsonBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.applyJsonBtn.click();
  console.log("Applied workflow JSON");
  await page.waitForTimeout(2000);
}

// Saves the workflow, asserts success toast, and waits for redirect to /workflow/{id}
export async function saveNewWorkflow(
  page: Page,
  locators: WorkflowLocators,
  workflowName: string
): Promise<void> {
  await locators.saveBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.saveBtn.click();

  await expect(locators.getSuccessMessage(workflowName)).toBeVisible({ timeout: 15000 });
  console.log(`Workflow '${workflowName}' created successfully`);

  await page.waitForURL(/.*\/workflow\/(?!new).*/, { timeout: 30000 });
  await locators.saveBtn.waitFor({ state: "visible", timeout: 30000 });

  // Attach the workflow URL so SlackReporter can include it in failure alert links
  try {
    await test.info().attach("workflowUrl", {
      body: Buffer.from(page.url()),
      contentType: "text/plain",
    });
  } catch {
    // testInfo not available outside of a test context — safe to ignore
  }
}

// Sets workflow status to ACTIVE, saves, then opens the run modal
export async function setWorkflowActiveAndSave(
  page: Page,
  locators: WorkflowLocators
): Promise<void> {
  await locators.statusDropdown.waitFor({ state: "visible", timeout: 20000 });
  await locators.statusDropdown.click();
  await locators.activeStatusOption.waitFor({ state: "visible", timeout: 10000 });
  await locators.activeStatusOption.click();
  await locators.saveBtn.click();
  console.log("Workflow set to ACTIVE and saved");
  await page.waitForTimeout(2000);
}

// Selects a cluster from the Account Id autocomplete in the action panel
export async function selectCluster(
  page: Page,
  locators: WorkflowLocators,
  clusterName: string
): Promise<void> {
  await locators.account_id_input.click();
  await locators.account_id_input.pressSequentially(clusterName);
  await page.getByRole("option", { name: clusterName }).click();
  console.log(`Selected cluster: ${clusterName}`);
}

// Selects an integration from the Integration Id autocomplete in the action panel
export async function selectIntegration(
  page: Page,
  locators: WorkflowLocators,
  integrationName: string
): Promise<void> {
  await locators.integration_id_input.click();
  await locators.integration_id_input.pressSequentially(integrationName);
  await page.getByRole("option", { name: integrationName }).click();
  console.log(`Selected integration: ${integrationName}`);
}

// Closes the action panel dialog and waits for animation to settle
export async function closeActionPanel(
  page: Page,
  locators: WorkflowLocators
): Promise<void> {
  await locators.actionPanelCloseBtn.click();
  await page.waitForTimeout(500);
}

// Full workflow run for tests that need no action panel configuration
export async function runSimpleWorkflow(
  page: Page,
  locators: WorkflowLocators,
  workflowJson: object,
  workflowName: string,
  testName: string
): Promise<void> {
  await loginAndNavigateToNewWorkflow(page, locators);
  await pasteAndApplyWorkflowJson(page, locators, workflowJson);
  await saveNewWorkflow(page, locators, workflowName);
  await setWorkflowActiveAndSave(page, locators);
  await runWorkflowWithGraphQLValidation(page, locators, testName);
}

// Clicks Dry Run, captures the REST API response body, and waits for the result chip
export async function dryRunAction(page: Page, locators: WorkflowLocators): Promise<string> {
  await locators.dryRunBtn.waitFor({ state: "visible", timeout: 10000 });

  // Intercept POST responses fired during dry run (excludes GraphQL and static assets)
  const capturedResponses: { url: string; status: number; body: string }[] = [];
  const responseHandler = async (response: import("@playwright/test").Response) => {
    const url = response.url();
    const method = response.request().method();
    if (method === "POST" && !url.includes("/graphql") && !url.match(/\.(js|css|png|jpg|woff|svg)(\?|$)/)) {
      try {
        const body = await response.text();
        capturedResponses.push({ url, status: response.status(), body: body.substring(0, 2000) });
      } catch {
        // body already consumed — skip
      }
    }
  };
  page.on("response", responseHandler);

  await locators.dryRunBtn.click();
  console.log("Clicked Dry Run button");

  await locators.actionTestResultChip.waitFor({ state: "visible", timeout: 30000 });
  const result = (await locators.actionTestResultChip.first().textContent()) ?? "";

  page.off("response", responseHandler);

  for (const r of capturedResponses) {
    console.log(`Dry Run REST response [${r.status}] ${r.url}\n${r.body}`);
  }
  console.log(`Dry Run result: ${result}`);
  return result;
}

// Clicks Run Task in the action panel and returns the result status text (e.g. "PASSED", "FAILED")
export async function runTaskAction(locators: WorkflowLocators): Promise<string> {
  await locators.runTaskBtn.waitFor({ state: "visible", timeout: 10000 });
  await locators.runTaskBtn.click();
  await locators.actionTestResultChip.waitFor({ state: "visible", timeout: 30000 });
  const result = (await locators.actionTestResultChip.first().textContent()) ?? "";
  console.log(`Run Task result: ${result}`);
  return result;
}

// Validates that the triggerWorkflow GraphQL mutation fires and returns HTTP 200
export async function runWorkflowWithGraphQLValidation(
  page: Page,
  locators: WorkflowLocators,
  testName: string
): Promise<void> {
  await locators.runBtn.waitFor({ state: "visible", timeout: 20000 });
  await locators.runBtn.click();
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.triggerAutomationBtn.click();
    },
    {
      testName: `${testName} - triggerWorkflow`,
      operationNames: ["triggerWorkflow"],
      timeoutMs: 30000,
      postCaptureWaitMs: 8000,
      checkDataErrors: true,
      workflowUrl: page.url(),
    }
  );
  console.log("GraphQL validation passed: triggerWorkflow fired and returned 200");
}
