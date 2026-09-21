// Not for OSS
import { test, expect } from "@playwright/test";
import { FINDINGS_HEADERS, ROLLUP_HEADERS } from "./configurationLocators";
import { expandCheck, expandCheckWithFindings, hasChecks, noMatchResource, openConfigurationTab, waitForFindings, waitForRollup } from "./configurationHelper";
import { expectSelectedTab, parkCursor, waitForRecommendations } from "../optimizeModuleHelper";

// Configuration — the tenant-level Optimize tab at /optimise#configuration
// (app/src/pages/optimise/index.jsx, filterOptions index 2). It is OptimizeNewPage.tsx
// with lockedCategory='Configuration', which changes three things this suite is about:
// the body rolls findings up by check instead of listing them per resource, the Savings
// filter and the category cards are gone, and Sort/Download are withheld while the
// rollup is showing.
//
// Everything here is read-only. Following tests/Optimize/OptimizeModule.spec.ts, this
// suite never opens a write modal — not even to cancel out of it, since a stray click
// inside Dismiss is one button away from a permanent change to a shared tenant. The
// overflow menu is opened once, and dismissed with Escape rather than a pointer, so the
// cursor never crosses an item. That leaves the module with no form-validation surface
// this suite can reach; written up in the PR's Follow-ups.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Optimize Configuration", () => {
  test(
    "Optimize Configuration sanity - open the Configuration tab, verify the check rollup renders its Severity, Check, Accounts and Findings columns and the toolbar offers search with the Account, Rules, Last seen and Status filters",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      await test.step("The body is the check rollup, not the per-resource list", async () => {
        // showConfigRollup is true on arrival because no rule filter and no search are
        // set, so the flat table is not merely hidden — it is never mounted.
        await expect(locators.configRulesTable.or(locators.configRulesEmpty).first()).toBeVisible({ timeout: 60000 });
        await expect(locators.recommendationsTable).toHaveCount(0);
      });

      await test.step("Every column the rollup declares is rendered", async () => {
        if (await hasChecks(locators)) {
          for (const header of ROLLUP_HEADERS) {
            await expect(locators.configRulesTable.locator("th", { hasText: header }).first()).toBeVisible();
          }
        } else {
          // A tenant whose accounts have not been scanned is a legitimate pass — the
          // rollup states that in its own copy rather than rendering a bare table.
          await expect(locators.configRulesEmpty).toBeVisible();
        }
      });

      await test.step("The toolbar offers search and the four filters this tab keeps", async () => {
        await expect(locators.recommendationsSearch).toBeVisible();
        await expect(locators.recommendationsAccountFilter).toBeVisible();
        await expect(locators.recommendationsRulesFilter).toBeVisible();
        await expect(locators.recommendationsLastSeenFilter).toBeVisible();
        await expect(locators.recommendationsStatusFilter).toBeVisible();
      });
    }
  );

  test(
    "Optimize Configuration sanity - open the Configuration tab, verify the Savings filter and the category summary cards are withheld because configuration findings carry no savings",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      await test.step("The Savings filter is absent, unlike on the Cost tab", async () => {
        // Rendered under `!isConfigurationOnly` in OptimizeNewPage.tsx. Every
        // configuration finding is $0, so a savings bucket would empty the list with
        // nothing visible to clear — its absence is the fix, and the contract.
        await expect(locators.recommendationsSavingsFilter).toHaveCount(0);
      });

      await test.step("The category cards are absent, because a locked tab is already one category", async () => {
        await expect(locators.summaryCardAll).toHaveCount(0);
      });

      await test.step("The severity and safety chip rows are still offered", async () => {
        // The chip row sits outside the lockedCategory branch, so narrowing by band is
        // the filtering this tab keeps in place of the cards.
        await expect(locators.severityBar).toBeVisible();
        await expect(page.getByTestId("severity-chip-critical")).toBeVisible();
        await expect(page.getByTestId("safety-chip-safe")).toBeVisible();
      });
    }
  );

  test(
    "Optimize Configuration sanity - open the Configuration tab, verify the rollup withholds the Sort by and Download controls because it has no per-resource rows to order or export",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      await test.step("Neither control is on the toolbar while the rollup is showing", async () => {
        // Both are rendered under `!showConfigRollup`. The presets sort the per-resource
        // list and the export is built from its rows, so on the rollup they would order
        // and export something the reader cannot see.
        await expect(locators.recommendationsSortTrigger).toHaveCount(0);
        await expect(locators.recommendationsDownload).toHaveCount(0);
      });
    }
  );

  test(
    "Optimize Configuration - search the check rollup for a resource name, verify the rollup gives way to the per-resource list and its Sort by and Download controls return",
    { tag: ["@dev", "@test", "@smoke", "@functional", "@search"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      const term = noMatchResource();
      await locators.searchAndApply(locators.recommendationsSearch, term);

      await test.step("The rollup is replaced by the flat recommendations table", async () => {
        // A search names specific rows, and the rollup groups by check with no row for
        // "the resources matching this text" — so showConfigRollup goes false and the
        // body swaps components. This is the switch the whole tab is built around.
        await expect(locators.recommendationsTable).toBeVisible({ timeout: 60000 });
        await expect(locators.configRulesTable).toHaveCount(0);
      });

      await test.step("Sort by and Download come back with the per-resource list", async () => {
        await expect(locators.recommendationsSortTrigger).toBeVisible({ timeout: 60000 });
        await expect(locators.recommendationsDownload).toBeVisible();
      });

      await test.step("The tab has not moved off Configuration", async () => {
        await expectSelectedTab(locators.ConfigurationTab);
      });
    }
  );

  test(
    "Optimize Configuration - search for a resource name that cannot exist, verify the per-resource list empties and reports that no recommendations match these filters",
    { tag: ["@dev", "@test", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      const term = noMatchResource();
      await locators.searchAndApply(locators.recommendationsSearch, term);
      await waitForRecommendations(locators);

      await test.step("The list comes back with no rows", async () => {
        // Retrying assertion rather than a snapshot count: the search refetches, and
        // reading the table once would race the in-flight request and see the old rows.
        await expect(locators.recommendationsRows).toHaveCount(0, { timeout: 60000 });
      });

      await test.step("The listing names the rejection instead of going blank", async () => {
        // CustomTable picks this string over the "well-optimised" one precisely because
        // a filter is active, so the copy is the proof the search reached the query.
        await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });
        await expect(locators.recommendationsNoDataText).toHaveCount(0);
      });

      await test.step("The search reached the URL, so the filter is real and shareable", async () => {
        await expect(page).toHaveURL(new RegExp(`search=${term}`));
      });
    }
  );

  test(
    "Optimize Configuration - apply a no-match search, click Clear all, verify the search leaves the field and the URL and the check rollup takes the per-resource list's place again",
    { tag: ["@dev", "@test", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      const term = noMatchResource();

      await test.step("Searching swaps the rollup out for an empty per-resource list", async () => {
        await locators.searchAndApply(locators.recommendationsSearch, term);
        await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });
        await expect(locators.configRulesTable).toHaveCount(0);
      });

      await test.step("Clear all empties the field and drops the term from the URL", async () => {
        await expect(locators.recommendationsClearFilters).toBeVisible();
        await locators.recommendationsClearFilters.click();
        await expect(locators.recommendationsSearch).toHaveValue("", { timeout: 60000 });
        await expect(page).not.toHaveURL(/[?&]search=/);
      });

      await test.step("The rollup returns, and the tab keeps its locked category", async () => {
        // handleClearAll keeps the category on a locked tab — clearing it would silently
        // turn Configuration into the all-categories list with no card to lock back. So
        // the rollup coming back is the proof the category survived the clear.
        await expect(locators.configRulesTable.or(locators.configRulesEmpty).first()).toBeVisible({ timeout: 60000 });
        await expect(locators.recommendationsTable).toHaveCount(0);
        await expectSelectedTab(locators.ConfigurationTab);
      });
    }
  );

  test(
    "Optimize Configuration - apply a no-match search then reload the page, verify the tab, the search term and its filtered empty result all survive the reload",
    { tag: ["@dev", "@test", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      const term = noMatchResource();
      await locators.searchAndApply(locators.recommendationsSearch, term);
      await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });

      await page.reload();

      // This is the tab's only persistence: OptimizeNewPage writes every filter into
      // router.query and re-seeds its state from router.query on mount, so a reload is
      // what proves the filter was stored rather than held in React state.
      await test.step("The reloaded page comes back on Configuration with the term still in the field", async () => {
        await expectSelectedTab(locators.ConfigurationTab);
        await waitForRecommendations(locators);
        await expect(locators.recommendationsSearch).toHaveValue(term, { timeout: 60000 });
      });

      await test.step("The stored term is applied to the query, not just echoed in the field", async () => {
        await expect(locators.recommendationsRows).toHaveCount(0, { timeout: 60000 });
        await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Optimize Configuration - expand the first check in the rollup, verify its findings drawer lists the failing resources under the Resource, Account and Last Seen columns",
    { tag: ["@dev", "@test", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);

      test.skip(!(await hasChecks(locators)), "The dev tenant reports no configuration checks, so there is no row to expand.");

      const row = locators.configRulesRows.first();
      await expandCheck(locators, row);

      await test.step("The check's drilldown mounts its own findings table", async () => {
        await waitForFindings(locators, row);
      });

      await test.step("Every column the findings table declares is rendered", async () => {
        if ((await locators.findingsRowsIn(row).count()) > 0) {
          for (const header of FINDINGS_HEADERS) {
            await expect(locators.findingsTableIn(row).locator("th", { hasText: header }).first()).toBeVisible();
          }
        } else {
          // The rollup counts every severity band while the drawer re-queries under the
          // active band filter, so a drawer with nothing in the selected bands is a real
          // state of the app and says so in its own copy.
          await expect(locators.findingsEmptyIn(row)).toBeVisible();
        }
      });

      await test.step("Collapsing the check puts the drawer away again", async () => {
        await locators.expandToggle(row).click();
        await expect(locators.expandToggle(row)).toHaveAttribute("aria-expanded", "false", { timeout: 30000 });
      });
    }
  );

  test(
    "Optimize Configuration - expand a check, open one failing resource from its findings drawer, verify the recommendation detail panel opens and closing it returns to the still-expanded check",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);

      test.skip(!(await hasChecks(locators)), "The dev tenant reports no configuration checks, so there is no row to expand.");

      const row = await expandCheckWithFindings(locators);
      test.skip(!row, "No configuration check in the first three rows reports findings in the active severity bands.");
      // test.skip above ends the test; this return only narrows the type for TypeScript.
      if (!row) return;

      await test.step("Clicking a finding's resource cell opens its detail panel", async () => {
        // The trailing cell holds RowActions, which stops propagation — the Resource
        // cell is the one that carries the row click through to the panel.
        await locators.findingsRowsIn(row).first().locator("td").nth(1).click();
        await expect(locators.detailPanel).toBeVisible({ timeout: 60000 });
      });

      await test.step("Closing the panel returns to the rollup with the check still open", async () => {
        await locators.detailPanelClose.click();
        await expect(locators.detailPanel).toHaveCount(0, { timeout: 30000 });
        await expect(locators.expandToggle(row)).toHaveAttribute("aria-expanded", "true");
      });
    }
  );

  test(
    "Optimize Configuration - clear the default Critical severity chip, verify it reports itself unpressed, leaves the URL severity list and the rollup re-settles on its remaining checks",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);
      await waitForRollup(locators);

      const criticalChip = page.getByTestId("severity-chip-critical");
      const highChip = page.getByTestId("severity-chip-high");

      await test.step("The tab opens with Critical and High already applied", async () => {
        // OptimizeNewPage seeds filters.severity with DEFAULT_SEVERITY on first load, so
        // both chips are pressed before anything is touched.
        await expect(criticalChip).toHaveAttribute("aria-pressed", "true", { timeout: 60000 });
        await expect(highChip).toHaveAttribute("aria-pressed", "true");
      });

      await test.step("Clicking Critical clears that band and leaves High applied", async () => {
        await criticalChip.click();
        await parkCursor(page);
        await expect(criticalChip).toHaveAttribute("aria-pressed", "false", { timeout: 60000 });
        await expect(highChip).toHaveAttribute("aria-pressed", "true");
      });

      await test.step("The cleared band leaves the URL, so the rollup re-queried rather than re-rendering", async () => {
        // updateUrl writes the surviving bands only, so Critical dropping out of the
        // query string is the app's own statement that the narrower filter reached it.
        await expect(page).toHaveURL(/severity=High/);
        await expect(page).not.toHaveURL(/severity=Critical/);
      });

      await test.step("The rollup settles again on rows or on its empty state, never on a skeleton", async () => {
        await waitForRollup(locators);
        await expect(locators.configRulesTable.or(locators.configRulesEmpty).first()).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Optimize Configuration - open the overflow menu on one failing resource, verify it offers the Create ticket and Dismiss entries, then press Escape and verify the menu closes with nothing actioned",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openConfigurationTab(page);

      test.skip(!(await hasChecks(locators)), "The dev tenant reports no configuration checks, so there is no row to expand.");

      const row = await expandCheckWithFindings(locators);
      test.skip(!row, "No configuration check in the first three rows reports findings in the active severity bands.");
      // test.skip above ends the test; this return only narrows the type for TypeScript.
      if (!row) return;

      const findingRow = locators.findingsRowsIn(row).first();
      // DropdownMenu portals its surface to the body, so the menu is not inside the row
      // it belongs to. Scoped to the open menu rather than matched page-wide: the tab
      // strip and the sidenav flyout render their own popovers.
      const openMenu = page.locator('[role="menu"]:visible');
      const menuItems = openMenu.locator('[role="menuitem"]');

      await test.step("The overflow trigger opens the row's action menu", async () => {
        await locators.rowActionsMenu(findingRow).click();
        await expect(menuItems.first()).toBeVisible({ timeout: 30000 });
      });

      await test.step("The menu names the ticket and dismiss actions RowActions declares", async () => {
        // Dismiss is offered only for a row the user can write to and whose status is
        // Open or Dismissed, so it is asserted as present-or-absent by count rather than
        // required — the ticket entry is unconditional and is the one that must be here.
        await expect(menuItems.filter({ hasText: /Create ticket|Ticket:/ }).first()).toBeVisible();
        expect(await menuItems.filter({ hasText: /Dismiss \/ snooze|Reactivate/ }).count()).toBeLessThanOrEqual(1);
      });

      await test.step("Escape closes the menu without actioning anything", async () => {
        // Dismissed with the keyboard on purpose: this suite never opens a write modal,
        // and a pointer leaving the trigger would cross the items on its way out.
        await page.keyboard.press("Escape");
        await expect(openMenu).toHaveCount(0, { timeout: 30000 });
        await expect(locators.detailPanel).toHaveCount(0);
      });
    }
  );
});
