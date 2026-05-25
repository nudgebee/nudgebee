import { Page, expect, test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { WorkflowLocators } from "./workflowlocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

export function generateWorkflowName(baseName: string): string {
  const suffix = String(Math.floor(Math.random() * 99) + 1).padStart(2, "0");
  return `${baseName} ${suffix}`;
}

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

  const jsonHeading = page.getByRole("heading", { name: "Automation JSON Editor" });
  await jsonHeading.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});

  const jsonPanelOpen = await jsonHeading.isVisible().catch(() => false);
  if (jsonPanelOpen) {
    await locators.jsonPanelToggleBtn.click();
    await jsonHeading.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  }

  await page.locator(".react-flow__node").first().waitFor({ state: "visible", timeout: 15000 });
}

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

  try {
    await test.info().attach("workflowUrl", {
      body: Buffer.from(page.url()),
      contentType: "text/plain",
    });
  } catch {
  }
}

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

export async function selectCluster(
  page: Page,
  locators: WorkflowLocators,
  clusterName: string
): Promise<void> {
  await locators.account_id_input.waitFor({ state: "visible", timeout: 10000 });
  await locators.account_id_input.click();
  await locators.account_id_input.fill(clusterName);
  await page.getByRole("option", { name: clusterName }).click();
  console.log(`Selected cluster: ${clusterName}`);
}

export async function selectIntegration(
  page: Page,
  locators: WorkflowLocators,
  integrationName: string
): Promise<void> {
  await locators.integrationIdDropdown.waitFor({ state: "visible", timeout: 10000 });
  await locators.integrationIdDropdown.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(integrationName);
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: integrationName }).first().click();
  console.log(`Selected integration: ${integrationName}`);
}

export async function closeActionPanel(
  page: Page,
  locators: WorkflowLocators
): Promise<void> {
  await locators.actionPanelCloseBtn.click();
  await page.waitForTimeout(500);
}

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

export async function dryRunAction(page: Page, locators: WorkflowLocators): Promise<void> {
  await locators.dryRunBtn.waitFor({ state: "visible", timeout: 10000 });

  const existingChipTexts = await page
    .locator("div.MuiDialog-container .MuiChip-label")
    .allTextContents();
  const existingSet = existingChipTexts.map((t) => t.trim());

  await locators.dryRunBtn.click();
  console.log("Clicked Dry Run button");

  await page
    .waitForFunction(
      (existing) => {
        const chips = Array.from(document.querySelectorAll("div.MuiDialog-container .MuiChip-label"));
        return chips.some((el) => {
          const text = el.textContent?.trim() ?? "";
          return text.length > 0 && !existing.includes(text);
        });
      },
      existingSet,
      { timeout: 30000 }
    )
    .catch(() => {});
}

export async function runTaskAction(page: Page, locators: WorkflowLocators): Promise<void> {
  await locators.runTaskBtn.waitFor({ state: "visible", timeout: 10000 });

  const existingChipTexts = await page
    .locator("div.MuiDialog-container .MuiChip-label")
    .allTextContents();
  const existingSet = existingChipTexts.map((t) => t.trim());

  await locators.runTaskBtn.click();
  console.log("Clicked Run Task button");

  await page
    .waitForFunction(
      (existing) => {
        const chips = Array.from(document.querySelectorAll("div.MuiDialog-container .MuiChip-label"));
        return chips.some((el) => {
          const text = el.textContent?.trim() ?? "";
          return text.length > 0 && !existing.includes(text);
        });
      },
      existingSet,
      { timeout: 30000 }
    )
    .catch(() => {});
}

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

export async function configureMcpIntegrationAction(
  page: Page,
  integrationName: string,
  toolName: string = "ask_question"
): Promise<void> {
  const dialog = page.locator("div.MuiDialog-container");
  await dialog.waitFor({ state: "visible", timeout: 15000 });

  await dialog.getByText(/Select an MCP integration/i).first().click();
  await page.waitForTimeout(500);

  const escapedIntegration = integrationName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  await page.locator('[role="option"]').filter({ hasText: new RegExp(escapedIntegration, "i") }).first().click();
  await page.waitForTimeout(500);

  const toolNameInput = dialog.getByPlaceholder(/Select or type tool name/i);
  await toolNameInput.click();
  await page.waitForTimeout(300);
  const toolOption = page.locator('[role="option"]').filter({ hasText: new RegExp(toolName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "i") });
  const toolOptionVisible = await toolOption.first().isVisible().catch(() => false);
  if (toolOptionVisible) {
    await toolOption.first().click();
  } else {
    await toolNameInput.fill(toolName);
    await toolNameInput.press("Enter");
  }

  console.log(`Configured MCP action: integration=${integrationName}, tool=${toolName}`);
}

export async function configureNotificationsImSlack(
  page: Page,
  slackChannel: string
): Promise<void> {
  const dialog = page.locator("div.MuiDialog-container");
  await dialog.waitFor({ state: "visible", timeout: 15000 });

  const providerAutocomplete = dialog.locator(".MuiAutocomplete-root").first();
  await providerAutocomplete.locator("input").click();
  await page.locator('[role="option"]').filter({ hasText: /^Slack$/ }).click();
  await page.waitForTimeout(500);

  await dialog.getByRole("button").filter({ hasText: "Select" }).first().click();
  await page.waitForTimeout(500);

  const channelInput = dialog.locator(".MuiAutocomplete-root").last().locator("input");
  await channelInput.fill(slackChannel);
  await page.waitForTimeout(500);
  const channelOption = page.locator('[role="option"]').filter({ hasText: slackChannel });
  const optionVisible = await channelOption.first().isVisible().catch(() => false);
  if (optionVisible) {
    await channelOption.first().click();
  }

  console.log(`Configured notifications.im with Slack channel: ${slackChannel}`);
}

export async function selectGitHubIntegration(
  page: Page,
  locators: WorkflowLocators,
  integrationName: string = process.env.GITHUB_NAME ?? "GitHub-test"
): Promise<void> {
  await locators.integrationIdDropdown.waitFor({ state: "visible", timeout: 10000 });
  await locators.integrationIdDropdown.click();
  await page.waitForTimeout(300);
  await page.keyboard.type(integrationName);
  await page.waitForTimeout(300);
  await page.locator('[role="option"]').filter({ hasText: integrationName }).first().click();
  console.log(`Selected integration: ${integrationName}`);
}