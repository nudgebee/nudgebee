// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openExecutions,
  gotoExecutions,
  gotoAutomationHash,
  expectExecutionsTabSelected,
  expectDashboardSettled,
  toggleFilterOption,
  columnTexts,
  totalRows,
  COMPLETED_STATUS,
  FAILED_STATUS,
  EXPECTED_COLUMNS,
  NO_EXECUTIONS_HINT,
} from "./executionsHelper";

// Executions tab of /automation — app/src/components/workflow/execution-dashboard/.
// Sibling of tests/workflow/Automations (tab 1) and tests/workflow/TaskRunner (tab 3),
// which cover the other two tabs of the same page.
//
// The whole module is read-only triage: it has no form, no modal and no action that
// writes anything server-side, so these tests create, modify and delete nothing on
// the shared dev tenant. The only state any of them writes is the browser's own
// localStorage filter memory, and every test clears that before the app boots
// (resetPersistedFilters), which is what makes them order-free and safe to re-run.
//
// The dev tenant's execution history is live, so no test asserts a particular count.
// Where a panel has two legitimate shapes — rows vs the no-executions panel, a
// ranked failures panel vs none — the test asserts the shape that rendered rather
// than requiring one.

test(
  "Executions sanity - open Automations from the side nav, switch to the Executions tab, verify the failure summary, the four filters and the refresh control render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);

    await test.step("The Executions tab is the one selected", async () => {
      await expectExecutionsTabSelected(locators);
      await expect(locators.automationsTab).toHaveAttribute("data-tab-selected", "false");
    });

    await test.step("The summary card reports failures out of a window total", async () => {
      await expect(locators.summaryCard).toContainText("Failed executions");
      // Counts carry a "≈" prefix because Temporal documents its count API as
      // approximate, so the sub-line is matched with that prefix optional.
      await expect(locators.summaryCard).toContainText(/of\s+≈?\s*[\d,]+\s+total/);
      await expect(locators.shareSucceeded).toContainText(/[\d,]+\s+·\s+[\d.]+%/);
      await expect(locators.shareTimedOut).toContainText(/[\d,]+\s+·\s+[\d.]+%/);
    });

    await test.step("The toolbar exposes all four filters and the refresh control", async () => {
      await expect(locators.accountFilter).toBeVisible();
      await expect(locators.automationFilter).toBeVisible();
      await expect(locators.userFilter).toBeVisible();
      await expect(locators.statusFilter).toBeVisible();
      await expect(locators.refreshBtn).toBeVisible();
    });

    await test.step("The caption states which filter is page-level and how rows are ordered", async () => {
      await expect(locators.scopeCaption).toContainText("Account applies to the whole page");
      await expect(locators.scopeCaption).toContainText("Sorted newest first");
    });
  }
);

test(
  "Executions sanity - open the Executions tab, verify the table renders its seven columns and settles into execution rows or the no-executions panel",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);

    await test.step("The header carries exactly the seven columns the tab declares", async () => {
      expect(await locators.headerNames()).toEqual(EXPECTED_COLUMNS);
    });

    await test.step("The body has resolved into one of its two real shapes", async () => {
      const rowCount = await locators.rows.count();
      if (rowCount === 0) {
        // A tenant whose window holds no run renders the empty panel and no
        // <tbody> at all, which is the app's own zero-row branch rather than a
        // failure to load.
        await expect(locators.emptyState).toBeVisible();
      } else {
        await expect(locators.tableBody).toBeVisible();
        // Every row carries a status, so a body that rendered structure but no
        // content would fail here rather than pass as "rows exist".
        const statuses = await columnTexts(locators, "Status");
        expect(statuses.length).toBe(rowCount);
        expect(statuses.every((value) => value.length > 0)).toBe(true);
      }
    });
  }
);

test(
  `Executions - open the Status filter, pick ${COMPLETED_STATUS.label}, verify statuses=${COMPLETED_STATUS.value} enters the URL and every Status cell reads ${COMPLETED_STATUS.value}`,
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);
    await toggleFilterOption(page, locators, "Status", COMPLETED_STATUS.label);

    await test.step("The choice is recorded in the URL and on the trigger", async () => {
      await expect(page).toHaveURL(new RegExp(`statuses=${COMPLETED_STATUS.value}`), { timeout: 30000 });
      await expect(locators.statusFilter).toContainText(COMPLETED_STATUS.label);
    });

    await test.step("The table holds nothing but completed runs", async () => {
      // The distinct Status values joined: "" at zero rows, the status alone when
      // the filter held, anything else when it did not.
      await expect
        .poll(async () => Array.from(new Set(await columnTexts(locators, "Status"))).join(","), { timeout: 30000 })
        .toMatch(new RegExp(`^(${COMPLETED_STATUS.value})?$`));

      if ((await locators.rows.count()) === 0) {
        await expect(locators.emptyState).toBeVisible();
      }
    });
  }
);

test(
  `Executions - filter by ${COMPLETED_STATUS.label}, toggle the same option off, verify statuses leaves the URL and the total returns to the unfiltered count`,
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);
    const baseline = await totalRows(locators);

    await toggleFilterOption(page, locators, "Status", COMPLETED_STATUS.label);
    const filtered = await totalRows(locators);

    await test.step("Filtering narrows the reported total rather than widening it", async () => {
      expect(filtered).toBeLessThanOrEqual(baseline);
    });

    await toggleFilterOption(page, locators, "Status", COMPLETED_STATUS.label);

    await test.step("Clearing the choice removes it from the URL and from the trigger", async () => {
      await expect(page).not.toHaveURL(/statuses=/, { timeout: 30000 });
      await expect(locators.statusFilter).not.toContainText(COMPLETED_STATUS.label);
    });

    await test.step("The unfiltered total is back", async () => {
      // Greater-or-equal rather than equal: this is a live tenant, and a run that
      // started during the test only ever adds to the count.
      await expect.poll(async () => totalRows(locators), { timeout: 30000 }).toBeGreaterThanOrEqual(baseline);
    });
  }
);

test(
  `Executions - open /automation?statuses=${FAILED_STATUS.value}#executions directly, verify the Status filter is seeded from the URL and the table is scoped to failed runs`,
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await gotoExecutions(page, `statuses=${FAILED_STATUS.value}`);

    await test.step("The deep link selected the Executions tab and seeded its Status filter", async () => {
      await expectExecutionsTabSelected(locators);
      await expect(locators.statusFilter).toContainText(FAILED_STATUS.label, { timeout: 30000 });
    });

    await test.step("The table is scoped to the status the link asked for", async () => {
      await expect
        .poll(async () => Array.from(new Set(await columnTexts(locators, "Status"))).join(","), { timeout: 30000 })
        .toMatch(new RegExp(`^(${FAILED_STATUS.value})?$`));

      if ((await locators.rows.count()) === 0) {
        await expect(locators.emptyState).toBeVisible();
      }
    });
  }
);

test(
  `Executions - pick the ${COMPLETED_STATUS.label} status, switch to the Automations tab and back, verify the Status selection is still applied`,
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);
    await toggleFilterOption(page, locators, "Status", COMPLETED_STATUS.label);
    await expect(locators.statusFilter).toContainText(COMPLETED_STATUS.label);

    await test.step("Leaving for the Automations tab unmounts the dashboard", async () => {
      await locators.automationsTab.click();
      await page.mouse.move(640, 500);
      await expect(locators.automationsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      await expect(locators.dashboardRoot).toBeHidden({ timeout: 30000 });
    });

    await test.step("Coming back restores the tab with the status still selected", async () => {
      await locators.executionsTab.click();
      await page.mouse.move(640, 500);
      await expectExecutionsTabSelected(locators);
      await expectDashboardSettled(locators);
      await expect(locators.statusFilter).toContainText(COMPLETED_STATUS.label, { timeout: 30000 });
    });
  }
);

test(
  "Executions - click an execution row, verify the detail drawer names the run, its id and its status, and closing it leaves the table intact",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);

    const rowCount = await locators.rows.count();
    if (rowCount === 0) {
      throw new Error(NO_EXECUTIONS_HINT);
    }

    const rowStatuses = await columnTexts(locators, "Status");
    const firstStatus = rowStatuses[0];

    await test.step("Clicking the row opens the execution detail drawer", async () => {
      // The first cell, not the row: the Automation cell holds a link that stops
      // propagation and opens the builder in a new tab instead.
      await locators.rows.first().locator("td").first().click();
      await expect(locators.drawerTitle).toBeVisible({ timeout: 30000 });
    });

    await test.step("The drawer carries the run's identity and the same status as its row", async () => {
      await expect(locators.drawer).toContainText("Execution ID");
      await expect(locators.drawer).toContainText("Started");
      await expect(locators.drawer).toContainText("Duration");
      await expect(locators.drawer).toContainText("Trigger");
      await expect(locators.drawer).toContainText("User");
      await expect(locators.drawer).toContainText(firstStatus);
      // The id is the only thing the table no longer shows, so the drawer is the
      // only place it can be read — a uuid, not an empty field.
      await expect(locators.drawer).toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i);
    });

    await test.step("Closing the drawer leaves the table exactly as it was", async () => {
      await locators.drawerCloseBtn.click();
      await expect(locators.drawerTitle).toBeHidden({ timeout: 30000 });
      await expect(locators.rows).toHaveCount(rowCount, { timeout: 30000 });
    });
  }
);

test(
  "Executions - click a row in the failures panel, verify the automation enters the URL and the table narrows to that one automation",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);

    // Probe, not an assertion: MostFailedAutomations renders nothing at all when
    // the aggregate ranked no failure, so its absence is a legitimate branch on a
    // tenant whose window holds none. Short timeout because the answer is already
    // decided — the settle above waits on the loaded summary card, and the panel
    // is fed by that same resolved aggregate.
    const panelPresent = await locators.mostFailedPanel
      .waitFor({ state: "visible", timeout: 5000 })
      .then(() => true)
      .catch(() => false);

    if (!panelPresent) {
      await test.step("With no failure ranked, the summary stands alone and no panel is drawn", async () => {
        await expect(locators.mostFailedPanel).toHaveCount(0);
        await expect(locators.summaryCard).toBeVisible();
      });
      return;
    }

    await test.step("The panel ranks automations by how many of the window's failures they own", async () => {
      await expect(locators.mostFailedPanel).toContainText("Where the failures are");
      await expect(locators.mostFailedRows.first()).toBeVisible({ timeout: 30000 });
    });

    await test.step("Clicking a ranked automation narrows the table to it", async () => {
      await locators.mostFailedRows.first().click();
      await expect(page).toHaveURL(/automations=/, { timeout: 30000 });
      await expectDashboardSettled(locators);

      // One automation selected means one distinct name down the Automation
      // column — zero only if that automation has no run left in the window.
      await expect.poll(async () => new Set(await columnTexts(locators, "Automation")).size, { timeout: 30000 }).toBeLessThanOrEqual(1);
    });
  }
);

test(
  "Executions sanity - open /automation on an unknown hash fragment, verify the page falls back to the Automations tab and the executions dashboard is not mounted",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await gotoAutomationHash(page, "not-a-real-tab");

    await test.step("An unrecognised fragment selects no tab, so the first one stands", async () => {
      await expect(locators.automationsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      await expect(locators.executionsTab).toHaveAttribute("data-tab-selected", "false");
    });

    await test.step("The executions dashboard is not rendered behind the fallback", async () => {
      await expect(locators.dashboardRoot).toHaveCount(0);
      await expect(locators.summaryCard).toHaveCount(0);
    });
  }
);

test(
  "Executions - refresh the dashboard, verify it re-settles and reports no fewer executions than before",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openExecutions(page);
    const before = await totalRows(locators);

    await test.step("Refreshing re-runs both the table and the summary requests", async () => {
      await locators.refreshBtn.click();
      await expectDashboardSettled(locators);
    });

    await test.step("The dashboard comes back with at least what it had", async () => {
      // Read-only page: a refresh can only pick up runs that started meanwhile, so
      // the total may grow but must never shrink.
      await expect.poll(async () => totalRows(locators), { timeout: 30000 }).toBeGreaterThanOrEqual(before);
      await expect(locators.summaryCard).toContainText("Failed executions");
    });
  }
);
