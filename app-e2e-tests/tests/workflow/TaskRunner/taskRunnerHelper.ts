import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { TaskRunnerLocators } from "./taskRunnerLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

export const RUNNER_ACCOUNT = process.env.CLUSTER || "";

// Logs in and lands on Automations → Task Runner with the task list rendered.
export async function openTaskRunner(page: Page): Promise<TaskRunnerLocators> {
  const locators = new TaskRunnerLocators(page);
  const loginPage = new LoginPage(page);

  await loginPage.doFullLogin();
  console.log("Login complete");

  await locators.automationSidenavBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.automationSidenavBtn.click();
  await page.waitForURL(/\/(auto-pilot|automation)/, { timeout: 30000 });

  await locators.taskRunnerTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.taskRunnerTab.click();

  await locators.runnerBox.waitFor({ state: "visible", timeout: 30000 });
  await expect(locators.searchInput).toBeVisible({ timeout: 30000 });
  // Task rows are not a valid signal here — every category starts collapsed and
  // a collapsed category renders no rows at all.
  await expect(locators.categorySummaries.first().or(locators.noMatchingTasks).or(locators.loadErrorBanner)).toBeVisible({ timeout: 60000 });
  console.log("Task Runner tab loaded");

  return locators;
}

// Accounts sit inside collapsed provider groups, so the option only enters the
// DOM once the search box force-opens them.
export async function selectRunnerAccount(page: Page, locators: TaskRunnerLocators, accountName = RUNNER_ACCOUNT): Promise<void> {
  if (!accountName) {
    throw new Error("selectRunnerAccount needs an account name; set CLUSTER in the env file.");
  }

  // A tenant with a single writable account has it pre-selected already.
  await expect(locators.accountSelect).toBeVisible({ timeout: 15000 });
  const current = (await locators.accountSelect.innerText()).trim();
  if (current === accountName) {
    console.log(`Account "${accountName}" already selected`);
    return;
  }

  await locators.accountSelect.click();
  await expect(locators.accountSearchInput).toBeVisible({ timeout: 10000 });
  await locators.accountSearchInput.fill(accountName);

  const option = locators.dropdownOption(accountName);
  await expect(option).toBeVisible({ timeout: 10000 });
  await option.click();

  // Read back — a picker that committed a different account would let the whole
  // spec run green against the wrong account's cluster.
  await expect(locators.accountSelect).toContainText(accountName, { timeout: 10000 });
  console.log(`Selected Task Runner account: ${accountName}`);
}

// Filtering is client-side, so the settled signal is the input having committed
// its value and the listing having re-rendered into one of its two states.
export async function searchTasks(page: Page, locators: TaskRunnerLocators, query: string): Promise<void> {
  await locators.searchInput.fill(query);
  await expect(locators.searchInput).toHaveValue(query, { timeout: 10000 });
  await expect(locators.categorySummaries.first().or(locators.noMatchingTasks)).toBeVisible({ timeout: 15000 });
}

export async function expandCategory(page: Page, locators: TaskRunnerLocators, label: string): Promise<void> {
  const summary = locators.categorySummary(label);
  await summary.waitFor({ state: "visible", timeout: 15000 });
  if ((await summary.getAttribute("aria-expanded")) !== "true") {
    await summary.click();
    await expect(locators.taskRows.first()).toBeVisible({ timeout: 15000 });
  }
}

// Probe, not an assertion: a category that does not hold the row is the normal
// case here, so absence has to come back as false rather than throw.
async function rowAppears(row: Locator, timeoutMs: number): Promise<boolean> {
  return row
    .waitFor({ state: "visible", timeout: timeoutMs })
    .then(() => true)
    .catch(() => false);
}

// The accordion is single-selection, so categories are opened one at a time
// until the wanted row appears — opening the next closes the previous.
export async function openCategoryWithTask(page: Page, locators: TaskRunnerLocators, taskType: string): Promise<boolean> {
  const row = locators.taskRow(taskType);
  if (await rowAppears(row, 1000)) return true;

  const categories = await locators.categorySummaries.count();
  for (let i = 0; i < categories; i++) {
    const summary = locators.categorySummaries.nth(i);
    await summary.click();
    // Rows only enter the DOM once the accordion reports itself open.
    await expect(summary).toHaveAttribute("aria-expanded", "true", { timeout: 10000 });
    if (await rowAppears(row, 2000)) return true;
  }
  return false;
}

// Narrows the listing with `search`, walks the surviving categories, then picks
// the task by its id.
export async function selectTask(page: Page, locators: TaskRunnerLocators, taskType: string, search?: string): Promise<void> {
  if (search) {
    await searchTasks(page, locators, search);
  }

  if (!(await openCategoryWithTask(page, locators, taskType))) {
    const offered = await locators.categorySummaries.allInnerTexts();
    throw new Error(
      `Task "${taskType}" is not offered by this tenant${search ? ` for search "${search}"` : ""}. ` +
        `Categories searched: ${offered.map((t) => t.split("\n")[0]).join(", ") || "(none)"}`
    );
  }

  await locators.taskRow(taskType).click();
  await expect(locators.testActionHeading).toBeVisible({ timeout: 15000 });
  console.log(`Selected task: ${taskType}`);
}

// Command and script parameters render as CodeMirror contenteditable divs, not
// form controls, so writing and reading both branch on that.

// Probe, not an assertion: a plain input is the expected other branch.
async function isCodeEditor(control: Locator): Promise<boolean> {
  return control.evaluate((node) => node.classList.contains("cm-content")).catch(() => false);
}

export async function setTaskField(locators: TaskRunnerLocators, label: string, value: string): Promise<void> {
  const control = locators.fieldControl(label);
  await control.waitFor({ state: "visible", timeout: 15000 });

  if (await isCodeEditor(control)) {
    await control.click();
    await control.press("Control+a");
    await control.press("Delete");
    await control.pressSequentially(value, { delay: 10 });
    return;
  }

  await control.fill(value);
}

export async function readTaskField(locators: TaskRunnerLocators, label: string): Promise<string> {
  const control = locators.fieldControl(label);
  await control.waitFor({ state: "visible", timeout: 15000 });

  if (!(await isCodeEditor(control))) {
    return (await control.inputValue()).trim();
  }

  // These fields use the parameter description as the CodeMirror placeholder,
  // which renders inside the content div — so it must be dropped before reading.
  const text = await control.evaluate((node) => {
    const clone = node.cloneNode(true) as HTMLElement;
    clone.querySelectorAll(".cm-placeholder").forEach((placeholder) => placeholder.remove());
    const lines = Array.from(clone.querySelectorAll(".cm-line")).map((line) => line.textContent ?? "");
    return lines.length ? lines.join("\n") : (clone.textContent ?? "");
  });
  return text.trim();
}

// Options-backed fields render as a hybrid picker: a dropdown when the option
// list opens, a plain text input when the field also accepts free text.
export async function setDropdownField(page: Page, locators: TaskRunnerLocators, label: string, value: string): Promise<void> {
  const trigger = locators.fieldDropdownTrigger(label);
  await trigger.waitFor({ state: "visible", timeout: 15000 });
  await trigger.click();

  const option = locators.dropdownOption(value);
  // Probe, not an assertion: these fields legitimately render as free text when
  // no option list exists, and that path is handled below.
  const opened = await option
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);

  if (opened) {
    await option.click();
    return;
  }

  console.log(`No option list for "${label}" — typing "${value}" instead`);
  await setTaskField(locators, label, value);
}

// Returns every option the field offers and leaves the list closed, for pickers
// whose options are tenant data rather than a fixed enum.
export async function listDropdownOptions(page: Page, locators: TaskRunnerLocators, label: string): Promise<string[]> {
  const trigger = locators.fieldDropdownTrigger(label);
  await trigger.waitFor({ state: "visible", timeout: 15000 });
  await trigger.click();

  // Probe, not an assertion: an empty list is turned into a named error below
  // rather than a bare timeout.
  const appeared = await locators.dropdownOptions
    .first()
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);
  if (!appeared) {
    throw new Error(`"${label}" offered no options — the tenant has none configured for this task.`);
  }

  const labels = (await locators.dropdownOptions.allTextContents()).map((text) => text.trim());
  await page.keyboard.press("Escape");
  return labels;
}

export async function selectDropdownOptionAt(page: Page, locators: TaskRunnerLocators, label: string, index: number): Promise<string> {
  const trigger = locators.fieldDropdownTrigger(label);
  await trigger.waitFor({ state: "visible", timeout: 15000 });
  await trigger.click();

  const option = locators.dropdownOptions.nth(index);
  await option.waitFor({ state: "visible", timeout: 10000 });
  const selected = ((await option.textContent()) || "").trim();
  await option.click();
  console.log(`Selected "${label}" option ${index + 1}: ${selected}`);
  return selected;
}

// Clicks Run Task and waits for the result section. `expectNetworkCall: false`
// is for the cases the UI short-circuits before reaching the API.
export async function runTask(
  page: Page,
  locators: TaskRunnerLocators,
  testName: string,
  options: { expectNetworkCall?: boolean; timeoutMs?: number } = {}
): Promise<void> {
  const { expectNetworkCall = true, timeoutMs = 120000 } = options;

  await expect(locators.runTaskBtn).toBeVisible({ timeout: 15000 });
  await expect(locators.runTaskBtn).toBeEnabled({ timeout: 15000 });

  if (expectNetworkCall) {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.runTaskBtn.click();
      },
      {
        testName: `${testName} - triggerTask`,
        operationNames: ["triggerTask"],
        timeoutMs,
        checkDataErrors: true,
        workflowUrl: page.url(),
      }
    );
  } else {
    await locators.runTaskBtn.click();
  }

  await expect(locators.runResultHeading).toBeVisible({ timeout: timeoutMs });
}

// Reports what happened instead of failing, for callers that retry across
// options. Skips the GraphQL watcher so an expected failure fires no Slack alert.
export async function runTaskAndReadResult(locators: TaskRunnerLocators, timeoutMs = 120000): Promise<{ status: string; output: string }> {
  await expect(locators.runTaskBtn).toBeEnabled({ timeout: 15000 });
  await locators.runTaskBtn.click();

  // Probe, not an assertion: a short run can finish before this is sampled, so
  // losing the race is reported rather than failed on.
  const wentBusy = await expect(locators.runTaskBtn)
    .toBeDisabled({ timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  if (!wentBusy) {
    console.log("Run finished before the button was seen disabled");
  }

  await expect(locators.runTaskBtn).toBeEnabled({ timeout: timeoutMs });
  await expect(locators.runResultHeading).toBeVisible({ timeout: 15000 });

  return { status: await readRunStatus(locators), output: await readRunOutput(locators) };
}

// Swallowing a missing chip here would turn a panel that never rendered into an
// ordinary status mismatch, so the wait is an assertion and the read is direct.
async function readRunStatus(locators: TaskRunnerLocators): Promise<string> {
  await expect(locators.runResultStatusChip).toBeVisible({ timeout: 15000 });
  return ((await locators.runResultStatusChip.textContent()) ?? "").trim();
}

export async function expectRunSucceeded(locators: TaskRunnerLocators): Promise<void> {
  const status = await readRunStatus(locators);
  if (status !== "COMPLETED" && status !== "SUCCESS") {
    const panelText = await locators.runnerBox.innerText();
    const errorLines = panelText
      .split("\n")
      .filter((line) => /error|fail|missing|required|denied|not found/i.test(line))
      .join(" | ");
    throw new Error(`Run Task did not complete: status "${status || "(none)"}"${errorLines ? ` — ${errorLines}` : ""}`);
  }
  console.log(`Run Task result: ${status}`);
}

export async function expectRunFailed(locators: TaskRunnerLocators, errorPattern: RegExp): Promise<void> {
  await expect(locators.runResultStatusChip).toHaveText(/FAILED|ERROR/, { timeout: 15000 });
  await expect(locators.runnerBox).toContainText(errorPattern, { timeout: 15000 });
}

export async function readRunOutput(locators: TaskRunnerLocators): Promise<string> {
  await expect(locators.runResultSection).toBeVisible({ timeout: 15000 });
  return (await locators.runResultSection.innerText()).trim();
}

// The output renders as a JSON tree, so a leaf reads back as `"key": "value"`.
// Chained runs need the exact value, not a substring match on the panel.
export async function readRunOutputValue(locators: TaskRunnerLocators, key: string): Promise<string> {
  const text = await readRunOutput(locators);
  const match = new RegExp(`"${key}"\\s*:\\s*"([^"]*)"`).exec(text);
  if (!match) {
    throw new Error(`Run output has no "${key}" field. Result section read: ${text.slice(0, 400)}`);
  }
  return match[1];
}
