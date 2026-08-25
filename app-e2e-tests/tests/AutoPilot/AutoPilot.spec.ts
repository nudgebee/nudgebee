// Not for OSS
import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { openFirstAutoPilotTask, openTaskTab, settleTasksTab } from "./autoPilotHelper";
import {
  AutoPilotLocators,
  TASKS_TABLE,
  TASKS_HEADERS,
  STATUS_COLUMN,
  TASK_STATUS_OPTIONS,
  ALL_STATUS,
  COMPLETE_STATUS,
  DRILLDOWN_HEADERS,
  DETAILS_STATS,
} from "./autoPilotLocators";

// Optimise > Auto Optimize > a configuration's name — app/src/pages/auto-pilot/task/
// [TaskDetails].jsx, rendering AutoOptimizeTasks (Tasks tab) and AutoOptimizeSummary
// (Details tab).
//
// Every case here is read-only. The page's two write surfaces both act on a configuration
// that already exists in the shared dev tenant — Edit opens the rightsizing config modal,
// and Disable/Enable flips the configuration's status for every other consumer of that
// tenant. Neither can be exercised without mutating a fixture this run does not own, so
// the write path is covered only as far as its cancel branch. See "Follow-ups" in the PR.

// An auto pilot id no tenant can hold, for the empty-listing case.
const UNKNOWN_TASK_ID = "00000000-0000-0000-0000-000000000000";

test(
  "AutoPilot sanity - open Auto Optimize, click the first configuration's name, verify the task page header names that configuration and the route carries its id",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    // The title is built as 'Auto Optimize Task > ' + the record's own name, so the row
    // that was clicked being named here is what proves the link opened that record.
    await expect(task.locators.pageTitle).toContainText(`Auto Optimize Task > ${task.taskName}`);
    await expect(page).toHaveURL(new RegExp(`/auto-pilot/task/${task.taskId}`));
    // The listing builds the link with the configuration's own account, and every query
    // the page fires is scoped by it — an empty one would silently widen them.
    expect(task.accountId).not.toEqual("");
  }
);

test(
  "AutoPilot sanity - open a task, verify the tab strip offers Tasks and Details and lands on Tasks with its listing mounted",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await expect(task.locators.tasksTab).toBeVisible();
    await expect(task.locators.detailsTab).toBeVisible();
    // Tasks is the default: the page's hash effect falls to tab 0 when the fragment names
    // no tab, so a cold arrival from the listing link must land here.
    await expect(task.locators.tasksTab).toHaveAttribute("aria-selected", "true");
    await expect(task.locators.detailsTab).toHaveAttribute("aria-selected", "false");
    await expect(task.locators.tasksListing).toBeVisible();
  }
);

test(
  "AutoPilot Tasks - open a task, verify the executions table renders all six columns and settles on either its rows or its No Data Available panel",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    for (const header of TASKS_HEADERS) {
      await expect(task.locators.tasksHeaderCell(header)).toBeVisible();
    }
    await expect(task.locators.statusFilter).toBeVisible();

    // Whether this configuration has actually run is the tenant's business, not the
    // page's — but exactly one of the two shapes must be on screen, never neither. The
    // retrying assertion is the wait; a plain count() would not retry and could read 0
    // against a table whose rows are still a beat behind their tbody.
    await expect(task.locators.tasksRows.first().or(task.locators.tasksEmpty).first()).toBeVisible({ timeout: 60000 });
  }
);

test(
  "AutoPilot Tasks - open the Status filter, verify it offers All, Complete, Scheduled, Failed, Skipped and Dryrun",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await task.locators.statusFilter.click();

    // Anchored per option, so an entry that is a substring of another cannot stand in for
    // the one being asserted — the six the page declares must each be there in their own
    // right.
    for (const option of TASK_STATUS_OPTIONS) {
      await expect(task.locators.filterOption(option)).toBeVisible();
    }
  }
);

test(
  "AutoPilot Tasks - pick Complete in the Status filter, verify the trigger reports Complete and no listed execution carries any other status",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await task.locators.pickFilterOption(task.locators.statusFilter, COMPLETE_STATUS);

    await expect(task.locators.statusFilter).toContainText(COMPLETE_STATUS);
    await expect(task.locators.tasksRows.first().or(task.locators.tasksEmpty).first()).toBeVisible({ timeout: 60000 });

    // The whole-table claim, made with a retrying count assertion rather than by reading
    // the cells once — the pick refetches, so a plain read races the in-flight request.
    // A configuration that has no Complete run yields the no-data panel instead, which is
    // the filter working just as much as a narrowed list is.
    const offStatus = task.locators
      .cellsInColumn(TASKS_TABLE, STATUS_COLUMN)
      .filter({ hasNotText: new RegExp(`^\\s*${COMPLETE_STATUS}\\s*$`) });
    await expect(offStatus).toHaveCount(0, { timeout: 60000 });
  }
);

test(
  "AutoPilot Tasks - narrow the Status filter to Complete then pick All, verify the trigger reports All and the unfiltered executions come back",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    const before = await task.locators.rowCount(TASKS_TABLE, task.locators.tasksEmpty);

    await task.locators.pickFilterOption(task.locators.statusFilter, COMPLETE_STATUS);
    await expect(task.locators.statusFilter).toContainText(COMPLETE_STATUS);
    await expect(task.locators.tasksRows.first().or(task.locators.tasksEmpty).first()).toBeVisible({ timeout: 60000 });

    await task.locators.pickFilterOption(task.locators.statusFilter, ALL_STATUS);

    // "All" carries the empty value, so the reset is proved by the row count returning to
    // the baseline rather than by the trigger's own text alone.
    await expect(task.locators.tasksRows).toHaveCount(before, { timeout: 60000 });
  }
);

test(
  "AutoPilot Tasks - click the chevron on the first execution, verify the row expands and its drilldown lists the Name, Old Value and New Value columns",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await expect(task.locators.tasksRows.first()).toBeVisible({ timeout: 60000 });
    const firstRow = task.locators.tasksRows.first();
    await task.locators.expandToggleIn(firstRow).click();

    // aria-expanded flips with the toggle, so it is the condition a sleep here would have
    // been standing in for.
    await expect(task.locators.collapseToggleIn(firstRow)).toHaveAttribute("aria-expanded", "true", { timeout: 30000 });

    const panel = task.locators.drilldownPanelFor(firstRow);
    for (const header of DRILLDOWN_HEADERS) {
      await expect(task.locators.drilldownHeaderCell(panel, header)).toBeVisible({ timeout: 30000 });
    }
  }
);

test(
  "AutoPilot Tasks - expand the first execution then click the chevron again, verify the row collapses and its drilldown columns leave the page",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await expect(task.locators.tasksRows.first()).toBeVisible({ timeout: 60000 });
    const firstRow = task.locators.tasksRows.first();

    await task.locators.expandToggleIn(firstRow).click();
    await expect(task.locators.collapseToggleIn(firstRow)).toHaveAttribute("aria-expanded", "true", { timeout: 30000 });

    await task.locators.collapseToggleIn(firstRow).click();

    // Collapsing must restore the closed chevron, not merely hide the panel behind it.
    await expect(task.locators.expandToggleIn(firstRow)).toHaveAttribute("aria-expanded", "false", { timeout: 30000 });
    // MUI Collapse keeps the panel mounted and animates its height to zero, so the
    // drilldown is asserted gone by visibility rather than by count.
    const panel = task.locators.drilldownPanelFor(firstRow);
    await expect(task.locators.drilldownHeaderCell(panel, DRILLDOWN_HEADERS[0])).toBeHidden({ timeout: 30000 });
  }
);

test(
  "AutoPilot - open a task and click the Details tab, verify the URL fragment moves to details and the summary replaces the executions table with its Namespace and Workload filters and stat cards",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await openTaskTab(page, "details", task.locators);

    // Tabs.jsx builds each tab as a Next link carrying its own fragment and preserving
    // accountId, so the click is what writes both.
    await expect(page).toHaveURL(/#details/);
    await expect(page).toHaveURL(new RegExp(`accountId=${task.accountId}`));

    await expect(task.locators.namespaceFilter).toBeVisible({ timeout: 60000 });
    await expect(task.locators.workloadFilter).toBeVisible();
    for (const label of DETAILS_STATS) {
      await expect(task.locators.detailsStat(label)).toBeVisible({ timeout: 30000 });
    }
    // The page renders exactly one tab body, so the executions table must be gone rather
    // than merely scrolled out of view.
    await expect(task.locators.tasksTable).toHaveCount(0);
  }
);

test(
  "AutoPilot - open the Details tab then click back to Tasks, verify the fragment returns to tasks and the executions table is mounted again",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const task = await openFirstAutoPilotTask(page);

    await openTaskTab(page, "details", task.locators);
    await expect(task.locators.namespaceFilter).toBeVisible({ timeout: 60000 });

    await openTaskTab(page, "tasks", task.locators);

    await expect(page).toHaveURL(/#tasks/);
    await settleTasksTab(task.locators);
    await expect(task.locators.pageTitle).toContainText(`Auto Optimize Task > ${task.taskName}`);
    // The Details body must have unmounted in turn, or "both tabs render" would pass on a
    // page that never switched.
    await expect(task.locators.detailsStat(DETAILS_STATS[0])).toBeHidden();
  }
);

test(
  "AutoPilot Tasks - open a task details url whose auto pilot id does not exist, verify the executions table renders its No Data Available panel and lists no rows",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    await new LoginPage(page).doFullLogin();
    const locators = new AutoPilotLocators(page);

    await page.goto(`/auto-pilot/task/${UNKNOWN_TASK_ID}#tasks`);

    // The listing card renders from the route alone, so it is the proof the page mounted
    // — without it, "no rows" would also be true of a page that never loaded.
    await expect(locators.tasksListing).toBeVisible({ timeout: 60000 });
    await expect(locators.tasksEmpty).toBeVisible({ timeout: 60000 });
    await expect(locators.tasksRows).toHaveCount(0);
    // An unknown id must stay on its route rather than bouncing the user elsewhere.
    await expect(page).toHaveURL(new RegExp(`/auto-pilot/task/${UNKNOWN_TASK_ID}`));
  }
);
