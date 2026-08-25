import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { TaskRunnerLocators } from "./taskRunnerLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { openTaskRunner, selectRunnerAccount, searchTasks, expandCategory, selectTask, setTaskField, readTaskField, RUNNER_ACCOUNT } from "./taskRunnerHelper";

const KUBECTL_NAMESPACE = process.env.KUBECTL_NAMESPACE!;

test("Task Runner sanity - login, open tab, validate ListTaskDefinitions API, verify categories, exclude triggers, match category count with task rows", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(120000);

  const locators = new TaskRunnerLocators(page);
  const loginPage = new LoginPage(page);

  await loginPage.doFullLogin();
  await locators.automationSidenavBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.automationSidenavBtn.click();
  await page.waitForURL(/\/(auto-pilot|automation)/, { timeout: 30000 });
  await locators.taskRunnerTab.waitFor({ state: "visible", timeout: 30000 });

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.taskRunnerTab.click();
    },
    {
      testName: "Task Runner sanity - ListTaskDefinitions",
      operationNames: ["ListTaskDefinitions"],
      timeoutMs: 45000,
      checkDataErrors: true,
    }
  );

  await expect(locators.runnerBox).toBeVisible({ timeout: 30000 });
  await expect(locators.runnerTitle).toBeVisible();
  await expect(locators.runnerIntro).toBeVisible();
  await expect(locators.accountSelect).toBeVisible();
  await expect(locators.searchInput).toBeVisible();
  await expect(locators.loadErrorBanner).toHaveCount(0);

  await expect(locators.categorySummaries.first()).toBeVisible({ timeout: 30000 });
  const categoryCount = await locators.categorySummaries.count();
  expect(categoryCount).toBeGreaterThan(0);
  console.log(`Task Runner rendered ${categoryCount} categories`);

  // Triggers can't be run standalone, so the runner must not list them.
  await expect(locators.categorySummary("Triggers")).toHaveCount(0);

  // The count on each header must match the rows the category reveals.
  const counts = await locators.categoryTaskCounts();
  expect(counts[0]).toBeGreaterThan(0);
  await locators.categorySummaries.first().click();
  await expect(locators.taskRows.first()).toBeVisible({ timeout: 15000 });
  await expect(locators.taskRows).toHaveCount(counts[0], { timeout: 15000 });
  console.log(`First category count ${counts[0]} matches its task rows`);
});

test("Task Runner sanity - login, deep link to #task-runner, verify tab renders without clicking the tab", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(120000);

  const locators = new TaskRunnerLocators(page);
  const loginPage = new LoginPage(page);

  await loginPage.doFullLogin();
  await page.goto(`${process.env.BASE_URL}/automation#task-runner`);

  await expect(locators.runnerBox).toBeVisible({ timeout: 30000 });
  await expect(locators.searchInput).toBeVisible({ timeout: 30000 });
  await expect(locators.taskRunnerTab).toHaveAttribute("id", "anchor-tab-task-runner");
  console.log("Deep link #task-runner landed on the Task Runner tab");
});

test("Task Runner sanity - search action by alias, search by category, expand category, no-match empty state, clear search and restore listing", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@sanity", "@search"] }, async ({ page }) => {
  test.setTimeout(120000);

  const locators = await openTaskRunner(page);

  // Only one category can be open at a time, so the listing's size is measured
  // from the per-category counts in the headers rather than by expanding all.
  const sum = (counts: number[]) => counts.reduce((total, n) => total + n, 0);
  const allCategories = await locators.categorySummaries.count();
  const allTasks = sum(await locators.categoryTaskCounts());
  expect(allTasks).toBeGreaterThan(0);

  // Alias search: "kubectl" is an alias of k8s.cli, not part of its label.
  await searchTasks(page, locators, "kubectl");
  const aliasTasks = sum(await locators.categoryTaskCounts());
  expect(aliasTasks).toBeLessThan(allTasks);
  await expandCategory(page, locators, "Kubernetes");
  await expect(locators.taskRow("k8s.cli")).toBeVisible({ timeout: 15000 });
  console.log(`Alias search "kubectl" narrowed ${allTasks} tasks to ${aliasTasks}`);

  // A category-name match keeps that category's tasks in the listing.
  await searchTasks(page, locators, "kubernetes");
  await expandCategory(page, locators, "Kubernetes");
  await expect(locators.taskRow("k8s.cli")).toBeVisible({ timeout: 15000 });

  await searchTasks(page, locators, "zzz-no-such-task");
  await expect(locators.noMatchingTasks).toBeVisible({ timeout: 15000 });
  await expect(locators.categorySummaries).toHaveCount(0);

  await searchTasks(page, locators, "");
  await expect(locators.categorySummaries).toHaveCount(allCategories, { timeout: 15000 });
  expect(sum(await locators.categoryTaskCounts())).toEqual(allTasks);
  console.log("Clearing the search restored the full listing");
});

test("Task Runner sanity - select account, select task, verify inline panel and hidden Dry Run, fill command, switch task, verify form reset", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(120000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);

  await expect(locators.noTaskSelected).toBeVisible();

  await selectTask(page, locators, "k8s.cli", "kubectl");
  await expect(locators.noTaskSelected).toHaveCount(0);
  await expect(locators.testActionHeading).toBeVisible();
  await expect(locators.testActionEmptyState).toBeVisible();
  await expect(locators.runTaskBtn).toBeVisible();
  await expect(locators.runTaskBtn).toBeEnabled();
  await expect(locators.runTaskHelperText).toBeVisible();
  await expect(locators.fieldControl("Command")).toBeVisible();

  // Dry Run simulates the surrounding workflow, which does not exist here — the
  // inline panel must not offer it.
  await expect(locators.dryRunBtn).toHaveCount(0);
  console.log("Inline panel shows Run Task and hides Dry Run");

  // The Command parameter is a CodeMirror editor, so this also proves typing
  // into a code field lands where a plain input would.
  const command = `kubectl get pods -n ${KUBECTL_NAMESPACE}`;
  await setTaskField(locators, "Command", command);
  expect(await readTaskField(locators, "Command")).toEqual(command);

  // Switching tasks starts from a clean form: TaskRunner resets taskData and
  // remounts the panel on every selection.
  await selectTask(page, locators, "core.print", "print");
  await expect(locators.fieldControl("Message")).toBeVisible({ timeout: 15000 });
  expect(await readTaskField(locators, "Message")).toEqual("");

  await selectTask(page, locators, "k8s.cli", "kubectl");
  expect(await readTaskField(locators, "Command")).toEqual("");
  console.log("Task switch reset the parameter form");
});

test("Task Runner sanity - open account dropdown, search and select account, verify commit, select task, verify account persists", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(120000);

  const locators = await openTaskRunner(page);
  await expect(locators.accountSelect).toBeVisible();

  await selectRunnerAccount(page, locators);
  await expect(locators.accountSelect).toContainText(RUNNER_ACCOUNT);

  // The choice must survive picking a task — the panel remounts on selection
  // and receives accountId as a prop.
  await selectTask(page, locators, "k8s.cli", "kubectl");
  await expect(locators.accountSelect).toContainText(RUNNER_ACCOUNT);
  console.log(`Account "${RUNNER_ACCOUNT}" held across task selection`);
});
