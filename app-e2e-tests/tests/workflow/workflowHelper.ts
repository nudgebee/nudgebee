import { Locator, Page, expect, test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { WorkflowLocators } from "./workflowlocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

export function generateWorkflowName(baseName: string): string {
  const suffix = String(Math.floor(Math.random() * 99) + 1).padStart(2, "0");
  return `${baseName} ${suffix}`;
}

export async function deleteCreatedWorkflow(
  page: Page,
  locators: WorkflowLocators,
  workflowName: string
): Promise<void> {
  let workflowId: string | undefined;
  try {
    workflowId = new URL(page.url()).pathname.match(/\/automation\/([0-9a-fA-F-]{36})/)?.[1];
  } catch {
    workflowId = undefined;
  }

  await locators.backBtn.click();
  const leavePageBtn = page.getByRole("button", { name: "Leave page" });
  await leavePageBtn
    .waitFor({ state: "visible", timeout: 3000 })
    .then(() => leavePageBtn.click())
    .catch(() => {});

  await locators.nameSearchInput
    .waitFor({ state: "visible", timeout: 15000 })
    .catch(() => page.waitForURL(/\/(auto-pilot|automation)/, { timeout: 15000 }));

  await locators.nameSearchInput.fill(workflowName);
  await locators.nameSearchInput.press("Enter");
  await page.waitForTimeout(1000);

  const menuTrigger = workflowId
    ? page.locator(`#workflow-menu-${workflowId}`)
    : page.locator('[id^="workflow-menu-"]').first();
  await menuTrigger.waitFor({ state: "visible", timeout: 15000 });
  await menuTrigger.click();

  const deleteMenuItem = page.locator('[role="menuitem"]:visible', { hasText: "Delete" }).first();
  await deleteMenuItem.waitFor({ state: "attached", timeout: 15000 });
  await deleteMenuItem.dispatchEvent("click");

  await locators.deleteConfirmBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.deleteConfirmBtn.click();

  await expect(page.getByText(`Automation "${workflowName}" deleted successfully`)).toBeVisible({ timeout: 15000 });
  console.log(`Deleted created workflow "${workflowName}"`);
}


// The Create Automation modal needs an account before any card is clickable, so skipping this makes the card click a silent no-op.
export async function selectAutomationAccount(
  page: Page,
  locators: WorkflowLocators,
  accountName: string
): Promise<void> {
  // Callers pass process.env.CLUSTER!, which is undefined when the env file omits it — fail with a readable message.
  if (!accountName) {
    throw new Error("selectAutomationAccount needs an account name; set CLUSTER in the env file.");
  }

  // Fall back to the placeholder text if the select keeps its id but the app renames it.
  const trigger = locators.createAccountSelect;
  const openTarget = (await trigger.isVisible().catch(() => false))
    ? trigger
    : page.locator("div.MuiDialog-container").getByText("Select an account").first();
  await openTarget.waitFor({ state: "visible", timeout: 15000 });
  await openTarget.click();

  const listbox = page.locator('[role="listbox"]');
  await listbox.waitFor({ state: "visible", timeout: 10000 });

  // The menu renders inside the aria-hidden dialog, so getByRole finds nothing here — CSS role selectors still work.
  const options = listbox.locator('[role="option"]');

  // The select is grouped: accounts live under collapsed provider headers (K8S/AWS/…), so no option
  // exists in the DOM until a search auto-expands the matching group. Search first, wait after.
  // Search only narrows the list; the exact match below does the picking so k8s-dev can't select k8s-dev-oss.
  const search = listbox.getByPlaceholder(/Search/i);
  const searchable = await search.isVisible().catch(() => false);
  if (searchable) {
    await search.fill(accountName);
  }

  // Wait for the filtered list to settle on the match instead of sleeping a fixed interval that CI load can outrun.
  const escaped = accountName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const exactMatch = () => options.filter({ hasText: new RegExp(`^${escaped}$`) });
  const appears = (locator: Locator) =>
    locator.first().waitFor({ state: "visible", timeout: 5000 }).then(() => true).catch(() => false);

  let option = exactMatch();
  let found = await appears(option);

  // Fall back to expanding every group by hand, then to a loose match before giving up.
  if (!found) {
    if (searchable) {
      await search.fill("");
    }
    const groupHeaders = listbox.locator('[role="button"]');
    const headerCount = await groupHeaders.count();
    for (let i = 0; i < headerCount; i++) {
      await groupHeaders.nth(i).click().catch(() => {});
    }
    option = exactMatch();
    found = await appears(option);
    if (!found) {
      option = options.filter({ hasText: accountName });
      found = await appears(option);
    }
  }

  if (!found) {
    const available = await options.allTextContents();
    throw new Error(
      `Account "${accountName}" not found in the Create Automation account list. Available: ${available.join(", ") || "(none)"}`
    );
  }
  await option.first().click();
  await listbox.waitFor({ state: "hidden", timeout: 10000 }).catch(() => {});
  console.log(`Selected automation account: ${accountName}`);
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

  await locators.createAutomationBtn
    .waitFor({ state: "visible", timeout: 30000 })
    .catch(() => page.waitForURL(/\/(auto-pilot|automation)/, { timeout: 15000 }));
  await locators.createAutomationBtn.click();
  await locators.createNewAutomationModal.waitFor({ state: "visible", timeout: 15000 });
  await selectAutomationAccount(page, locators, process.env.CLUSTER!);
  await locators.makeAnAutomationCard.waitFor({ state: "visible", timeout: 10000 });
  await locators.makeAnAutomationCard.click();

  await page.waitForURL(/\/automation\/new/, { timeout: 30000 });
  await page.getByText("How should your Automation begin?").waitFor({ state: "visible", timeout: 30000 });

  // Pick Manual Trigger in the "How should your Automation begin?" window.
  const triggerPicker = page.getByText("How should your Automation begin?");
  await locators.manualTriggerOption.waitFor({ state: "visible", timeout: 15000 });
  await locators.manualTriggerOption.click();

  // The picker only renders while the canvas is empty, so it hiding proves the trigger node was really added.
  let triggerSelected = await triggerPicker
    .waitFor({ state: "hidden", timeout: 10000 })
    .then(() => true)
    .catch(() => false);

  // Fall back to re-clicking, then to the card id, since a click that never registers otherwise passes silently.
  if (!triggerSelected) {
    await locators.manualTriggerOption.click();
    triggerSelected = await triggerPicker
      .waitFor({ state: "hidden", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
  }
  if (!triggerSelected) {
    await page.locator("#wf-builder-trigger-option-manual-card").click();
    await triggerPicker.waitFor({ state: "hidden", timeout: 10000 });
  }
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
  // insertText instead of a clipboard paste: the clipboard is one OS-wide resource, so parallel workers overwrite each other.
  await locators.codeMirrorEditor.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.insertText(jsonContent);

  // CodeMirror virtualizes long documents, so assert on the name only — it sits on the first line of the JSON.
  const pastedName = (workflowJson as { name?: string }).name;
  const contentLanded = async () =>
    !pastedName || (await locators.codeMirrorEditor.textContent().catch(() => ""))?.includes(pastedName) === true;

  // Fall back to retyping, since Apply on a stale editor closes the panel and leaves the canvas empty.
  if (!(await contentLanded())) {
    await locators.codeMirrorEditor.click();
    await page.keyboard.press("ControlOrMeta+A");
    await page.keyboard.insertText(jsonContent);
  }
  if (pastedName) {
    await expect(locators.codeMirrorEditor).toContainText(pastedName, { timeout: 15000 });
  }

  // Fall back to reopening the panel if Apply never appears, which happens when the editor renders only partially.
  const applyVisible = await locators.applyJsonBtn
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  if (!applyVisible) {
    await locators.jsonPanelToggleBtn.click();
    await locators.codeMirrorEditor.waitFor({ state: "visible", timeout: 15000 });
    await locators.codeMirrorEditor.click();
    await page.keyboard.press("ControlOrMeta+A");
    await page.keyboard.insertText(jsonContent);
    await locators.applyJsonBtn.waitFor({ state: "visible", timeout: 15000 });
    console.log("Reopened JSON panel after Apply failed to render");
  }
  await locators.applyJsonBtn.click();
  console.log("Applied workflow JSON");

  const jsonHeading = page.getByRole("heading", { name: "Automation JSON Editor" });
  await jsonHeading.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});

  const jsonPanelOpen = await jsonHeading.isVisible().catch(() => false);
  if (jsonPanelOpen) {
    await locators.jsonPanelToggleBtn.click();
    await jsonHeading.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  }

  // Fall back to reopening the panel and applying again if the first Apply produced no nodes.
  const firstNode = page.locator(".react-flow__node").first();
  const nodesRendered = await firstNode
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  if (!nodesRendered) {
    await locators.jsonPanelToggleBtn.click();
    await locators.codeMirrorEditor.waitFor({ state: "visible", timeout: 15000 });
    await locators.codeMirrorEditor.click();
    await page.keyboard.press("ControlOrMeta+A");
    await page.keyboard.insertText(jsonContent);
    await locators.applyJsonBtn.click();
    await firstNode.waitFor({ state: "visible", timeout: 15000 });
    console.log("Re-applied workflow JSON after empty canvas");
  }
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

  await page.waitForURL(/\/automation\/[0-9a-fA-F-]{36}/, { timeout: 30000 });
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

export async function waitForExecutionToComplete(
  page: Page,
  locators: WorkflowLocators,
  timeoutMs = 240000
): Promise<void> {
  await expect(locators.runBtn).toContainText("Run current", { timeout: timeoutMs });
  console.log("Workflow execution reached a terminal state");

  const outcomeToast = page
    .getByText(/Automation execution (completed successfully|failed|was terminated|timed out|completed with errors)/i)
    .first();
  await outcomeToast.waitFor({ state: "visible", timeout: 10000 }).catch(() => {});
  const outcomeText = ((await outcomeToast.textContent().catch(() => "")) ?? "").trim();
  if (/failed|terminated|timed out|completed with errors/i.test(outcomeText)) {
    throw new Error(`Workflow execution did not complete successfully: ${outcomeText}`);
  }
  console.log("Workflow execution completed successfully");
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