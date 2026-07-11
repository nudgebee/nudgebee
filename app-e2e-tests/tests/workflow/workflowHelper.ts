import { Page, expect, test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { WorkflowLocators } from "./workflowlocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

export function generateWorkflowName(baseName: string): string {
  const suffix = String(Math.floor(Math.random() * 99) + 1).padStart(2, "0");
  return `${baseName} ${suffix}`;
}

/**
 * Cleans up the automation the current test created by driving the UI: go back
 * to the listing, search for it by name, open ITS 3-dot menu (targeted by the
 * created workflow's id from the editor URL so no other row is touched), then
 * Delete → confirm. Call after the workflow has been created and run.
 */
export async function deleteCreatedWorkflow(
  page: Page,
  locators: WorkflowLocators,
  workflowName: string
): Promise<void> {
  // The editor URL is `/workflow/<id>?accountId=...`; capture the id so we click
  // exactly this workflow's menu and never any other automation's.
  let workflowId: string | undefined;
  try {
    workflowId = new URL(page.url()).pathname.match(/\/workflow\/([0-9a-fA-F-]{36})/)?.[1];
  } catch {
    workflowId = undefined;
  }

  // Back to the automation listing. Leaving the editor with a saved-but-not-
  // published draft pops an "Unpublished changes" guard — confirm "Leave page".
  await locators.backBtn.click();
  const leavePageBtn = page.getByRole("button", { name: "Leave page" });
  await leavePageBtn
    .waitFor({ state: "visible", timeout: 3000 })
    .then(() => leavePageBtn.click())
    .catch(() => {});
  await page.waitForURL(/\/auto-pilot/, { timeout: 15000 });

  // Surface the just-created row via the name search.
  await locators.nameSearchInput.waitFor({ state: "visible", timeout: 15000 });
  await locators.nameSearchInput.fill(workflowName);
  await locators.nameSearchInput.press("Enter");
  await page.waitForTimeout(1000);

  // Open this row's 3-dot menu. The trigger id carries the workflow id; fall
  // back to the only menu on the filtered listing if the id wasn't captured.
  const menuTrigger = workflowId
    ? page.locator(`#workflow-menu-${workflowId}`)
    : page.locator('[id^="workflow-menu-"]').first();
  await menuTrigger.waitFor({ state: "visible", timeout: 15000 });
  await menuTrigger.click();

  await page.getByRole("menuitem", { name: "Delete" }).click();

  await locators.deleteConfirmBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.deleteConfirmBtn.click();

  await expect(page.getByText(`Automation "${workflowName}" deleted successfully`)).toBeVisible({ timeout: 15000 });
  console.log(`Deleted created workflow "${workflowName}"`);
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

export async function selectTicketIntegration(
  locators: WorkflowLocators,
  integrationName: string,
  buttonName: RegExp = /Ticket integration|Incident management integration|Select integration id/i
): Promise<void> {
  const integrationBtn = locators.dialog.getByRole("button", { name: buttonName }).first();
  await integrationBtn.waitFor({ state: "visible", timeout: 15000 });
  await integrationBtn.click();

  // Primary: exact text match. Fallback: a role=option row containing the name
  // (handles extra icons/whitespace/adornments inside the option row).
  const exactOption = locators.dialog.getByText(integrationName, { exact: true });
  if (await exactOption.first().isVisible().catch(() => false)) {
    await exactOption.first().click();
  } else {
    await locators.dialog.locator('[role="option"]').filter({ hasText: integrationName }).first().click();
  }
  console.log(`Selected integration: ${integrationName}`);
}

export async function selectProjectKey(
  page: Page,
  locators: WorkflowLocators,
  projectKey: string
): Promise<void> {
  // Fallback: the project_key field can default to Expression mode (a template
  // text field) instead of the Select dropdown. If the dropdown trigger isn't
  // present, flip the field to Select mode via its toggle first.
  if (!(await locators.projectKeyDropdown.isVisible().catch(() => false))) {
    const selectTab = locators.dialog
      .locator(".MuiToggleButtonGroup-grouped")
      .filter({ hasText: "Select" })
      .last();
    if (await selectTab.isVisible().catch(() => false)) {
      await selectTab.scrollIntoViewIfNeeded();
      await selectTab.click();
      await page.waitForTimeout(500);
    }
  }

  await locators.projectKeyDropdown.waitFor({ state: "visible", timeout: 15000 });
  await locators.projectKeyDropdown.click();
  await page.waitForTimeout(700);
  await page.keyboard.type(projectKey);
  await page.waitForTimeout(300);

  // Primary: option containing the full key. Fallback: match the repo segment
  // after the last "/" (some providers label options by repo name only).
  const fullOption = page.locator('[role="option"]').filter({ hasText: projectKey }).first();
  if (await fullOption.isVisible().catch(() => false)) {
    await fullOption.click();
  } else {
    const repoSegment = projectKey.split("/").pop() ?? projectKey;
    await page.locator('[role="option"]').filter({ hasText: repoSegment }).first().click();
  }
  console.log(`Selected Project Key: ${projectKey}`);
}

export async function closeActionPanel(
  page: Page,
  locators: WorkflowLocators
): Promise<void> {
  const saveActionBtn = page.locator("#action-sidebar-save-btn");
  if (await saveActionBtn.isVisible().catch(() => false)) {
    await saveActionBtn.click();
    await saveActionBtn.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  }

  await locators.actionPanelCloseBtn.click();

  const saveChangesBtn = page.getByRole("button", { name: "Save changes" });
  if (await saveChangesBtn.isVisible().catch(() => false)) {
    await saveChangesBtn.click();
  }

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
  const saveActionBtn = page.locator("#action-sidebar-save-btn");
  if (await saveActionBtn.isVisible().catch(() => false)) {
    await saveActionBtn.click();
    await saveActionBtn.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(300);
    console.log("Saved action config before dry run");
  }

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

  const resultChips = (await page.locator("div.MuiDialog-container .MuiChip-label").allTextContents()).map((t) => t.trim());
  console.log(`Dry Run result chips: ${resultChips.join(", ")}`);
  const dialogText = (await locators.dialog.innerText().catch(() => "")) || "";
  if (resultChips.some((t) => /fail/i.test(t)) || /validation failed|missing required parameter/i.test(dialogText)) {
    const errorBlock = dialogText.split("\n").filter((l) => /error|fail|missing|required/i.test(l)).join(" | ");
    throw new Error(`Dry Run failed: ${errorBlock || resultChips.join(", ")}`);
  }
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

  await expect(dialog.getByRole("button", { name: "Slack", exact: true })).toBeVisible();
  await expect(dialog.getByRole("textbox", { name: "Select channel" })).toHaveValue(slackChannel);

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