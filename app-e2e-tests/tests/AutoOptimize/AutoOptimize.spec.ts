// Not for OSS
import { test, expect } from "@playwright/test";
import {
  APPROVALS_HEADERS,
  APPROVALS_TABLE,
  CATEGORY_COLUMN,
  CREATE_MENU_ITEMS,
  DEFAULT_STATUS,
  DISABLED_STATUS,
  OPTIMIZATIONS_TABLE,
  PVC_CATEGORY,
  STATUS_COLUMN,
} from "./autoOptimizeLocators";
import {
  expectSelectedSubTab,
  expectSelectedTab,
  noMatchTerm,
  openAutoOptimize,
  parkCursor,
  settleApprovals,
  settleOptimizations,
} from "./autoOptimizeHelper";

// Auto Optimize (AutoPilot) — tab 4 of /optimise, rendered by
// app/src/components/autopilot/tables/AutoOptimizeTabs.tsx. Distinct from the rest of
// /optimise, which tests/Optimize covers: this tab is the scheduled-rightsizing module.
//
// Read-only by construction. Every write this module offers acts on a shared dev tenant
// and cannot be undone from the UI: creating a configuration registers a live scheduled
// job that will rightsize real workloads, Enable/Disable flips an existing tenant's
// schedule, and Review approves or rejects someone else's pending change. So the suite
// never submits a form and never confirms a toggle — the Create menu is opened and
// dismissed, which is as far as a write path can safely be exercised here. Written up
// in the PR under Follow-ups.
const SPEC_TIMEOUT_MS = 180000;

// AutoOptimizeListingTable drops a search term of two characters or fewer on the floor:
// `query['name'] = nameToUse.length > 2 ? nameToUse : null`. That floor is what the
// short-term test asserts against.
const BELOW_SEARCH_FLOOR = "zz";

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Auto Optimize", () => {
  test(
    "Auto Optimize sanity - open /optimise#auto-optimize, verify the Auto Optimize tab is selected and its Optimizations and Approvals sub-tabs render with Optimizations open",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      await test.step("The module opens on its own tab of the Optimize strip", async () => {
        await expect(locators.AutoOptimizeTab).toContainText("Auto Optimize");
        await expectSelectedTab(locators.AutoOptimizeTab);
      });

      await test.step("Both sub-tabs the module declares are on the row", async () => {
        // tabOptions for the Auto Optimize entry in app/src/pages/optimise/index.jsx.
        await expect(locators.optimizationsSubTab).toBeVisible();
        await expect(locators.optimizationsSubTab).toContainText("Optimizations");
        await expect(locators.approvalsSubTab).toBeVisible();
        await expect(locators.approvalsSubTab).toContainText("Approvals");
      });

      await test.step("A hash with no sub-fragment opens Optimizations, and only Optimizations", async () => {
        await expectSelectedSubTab(locators.optimizationsSubTab);
        await expect(locators.approvalsSubTab).toHaveAttribute("aria-selected", "false");
        // AutoOptimizeTabs renders one sub-tab body at a time, so the Approvals listing
        // being absent from the DOM is what proves the row is not merely styled.
        await expect(locators.optimizationsListing).toBeVisible();
        await expect(locators.approvalsListing).toHaveCount(0);
      });
    }
  );

  test(
    "Auto Optimize Optimizations - open the Optimizations sub-tab, verify the Account filter resolves to the cluster the run logged in on and the Status, Category, search and download controls render",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const { locators, cluster } = await openAutoOptimize(page);

      await test.step("The account filter adopts the session's cluster rather than sitting unset", async () => {
        // AutoOptimizeTabs.resolvedAccountId prefers the globally selected cluster when
        // it is a K8s account, which is the one doFullLogin just picked. Read back from
        // the login's return value so the assertion is env-driven, not hardcoded.
        expect(cluster, "doFullLogin did not settle on a cluster — CLUSTER / CLUSTER_NAME may be unset").not.toBe("");
        await expect(locators.optimizationsAccountFilter).toBeVisible();
        await expect(locators.optimizationsAccountFilter).toContainText(cluster, { timeout: 60000 });
      });

      await test.step("The toolbar exposes both remaining filters, the search box and download", async () => {
        await expect(locators.optimizationsStatusFilter).toBeVisible();
        // The listing seeds selectedStatus with 'Active', so the trigger carries a value
        // from the first render — an empty "Status" would mean the seed was lost.
        await expect(locators.optimizationsStatusFilter).toContainText(DEFAULT_STATUS);
        await expect(locators.optimizationsCategoryFilter).toBeVisible();
        await expect(locators.optimizationsCategoryFilter).toContainText("Category");
        await expect(locators.optimizationsSearch).toBeVisible();
        await expect(locators.optimizationsDownload).toBeVisible();
      });

      await test.step("The table settles on rows or on its empty heading, never on a skeleton", async () => {
        const rows = await locators.rowCount(OPTIMIZATIONS_TABLE, locators.optimizationsEmpty);
        if (rows > 0) {
          // Column contract from LISTING_HEADER in AutoOptimizeListingTable.jsx.
          for (const header of ["Name", "Status", "Category", "Resource", "Created By"]) {
            await expect(locators.optimizationsTable.locator("th", { hasText: header }).first()).toBeVisible();
          }
        } else {
          await expect(locators.optimizationsEmpty).toBeVisible();
        }
      });
    }
  );

  test(
    "Auto Optimize Optimizations - open the Status filter and pick Disabled, verify the trigger reports Disabled and no listed configuration is still Active",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      await test.step("The tab opens on its seeded Active filter", async () => {
        await expect(locators.optimizationsStatusFilter).toContainText(DEFAULT_STATUS);
      });

      await locators.pickFilterOption(locators.optimizationsStatusFilter, DISABLED_STATUS);

      await test.step("The pick is committed to the trigger and the seeded value is gone", async () => {
        await expect(locators.optimizationsStatusFilter).toContainText(DISABLED_STATUS, { timeout: 30000 });
        await expect(locators.optimizationsStatusFilter).not.toContainText(DEFAULT_STATUS);
      });

      await test.step("The listing refetched against the new status, not just relabelled its trigger", async () => {
        await settleOptimizations(locators);
        // The status cell is a <Label> holding the raw status string, so an Active row
        // surviving a Disabled filter means the query never carried the new value.
        const activeCells = locators
          .cellsInColumn(OPTIMIZATIONS_TABLE, STATUS_COLUMN)
          .filter({ hasText: new RegExp(`^${DEFAULT_STATUS}$`) });
        await expect(activeCells).toHaveCount(0, { timeout: 60000 });
      });
    }
  );

  test(
    "Auto Optimize Optimizations - open the Category filter and pick PVC RightSizing, verify the trigger reports it and every listed configuration is a PVC RightSizing",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      await locators.pickFilterOption(locators.optimizationsCategoryFilter, PVC_CATEGORY);

      await test.step("The pick is committed to the trigger", async () => {
        await expect(locators.optimizationsCategoryFilter).toContainText(PVC_CATEGORY, { timeout: 30000 });
      });

      await test.step("Every row the listing came back with belongs to that category", async () => {
        await settleOptimizations(locators);
        // Counting the cells that do NOT read the picked category states the whole-table
        // claim as one retrying assertion, so a stale row still on screen from the
        // in-flight refetch resolves instead of failing. A tenant holding no PVC
        // configurations makes this vacuous — settleOptimizations above is what
        // distinguishes "settled empty" from "never loaded" in that case.
        const offCategory = locators.cellsInColumn(OPTIMIZATIONS_TABLE, CATEGORY_COLUMN).filter({ hasNotText: PVC_CATEGORY });
        await expect(offCategory).toHaveCount(0, { timeout: 60000 });
      });
    }
  );

  test(
    "Auto Optimize Optimizations - search for a configuration name that cannot exist, verify the table empties and the listing shows its No Data Available state",
    { tag: ["@dev", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      await locators.searchAndApply(locators.optimizationsSearch, noMatchTerm());

      await test.step("The table comes back with no rows", async () => {
        // A retrying assertion rather than a snapshot count: the search refetches, and
        // reading the table once would race the in-flight request and see the old rows.
        await expect(locators.optimizationsRows).toHaveCount(0, { timeout: 60000 });
      });

      await test.step("The listing renders its empty panel instead of going blank", async () => {
        await expect(locators.optimizationsEmpty).toBeVisible({ timeout: 60000 });
        await expect(locators.optimizationsEmpty).toHaveText("No Data Available");
      });
    }
  );

  test(
    "Auto Optimize Optimizations - search a two-character name, verify the listing ignores it and keeps its unfiltered rows, then extend the term past the three-character floor and verify the table empties",
    { tag: ["@dev", "@regression", "@negative", "@validation", "@search"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      const baseline = await locators.rowCount(OPTIMIZATIONS_TABLE, locators.optimizationsEmpty);

      await test.step("A term below the floor reaches the field but not the query", async () => {
        await locators.searchAndApply(locators.optimizationsSearch, BELOW_SEARCH_FLOOR);
        // Both halves are needed: the field holding the term proves it was typed and
        // committed, and the unchanged row count proves listAutoPilot nulled it out
        // rather than filtering on it.
        await expect(locators.optimizationsSearch).toHaveValue(BELOW_SEARCH_FLOOR);
        await expect(locators.optimizationsRows).toHaveCount(baseline, { timeout: 60000 });
      });

      const longTerm = `${BELOW_SEARCH_FLOOR}${noMatchTerm()}`;

      await test.step("The same term extended past the floor is applied, and matches nothing", async () => {
        await locators.searchAndApply(locators.optimizationsSearch, longTerm);
        await expect(locators.optimizationsSearch).toHaveValue(longTerm);
        await expect(locators.optimizationsRows).toHaveCount(0, { timeout: 60000 });
        await expect(locators.optimizationsEmpty).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Auto Optimize Optimizations - search a name that cannot exist then clear the search box, verify the empty panel goes and the original rows come back",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      const baseline = await locators.rowCount(OPTIMIZATIONS_TABLE, locators.optimizationsEmpty);

      await test.step("The search empties the listing", async () => {
        await locators.searchAndApply(locators.optimizationsSearch, noMatchTerm());
        await expect(locators.optimizationsRows).toHaveCount(0, { timeout: 60000 });
        await expect(locators.optimizationsEmpty).toBeVisible({ timeout: 60000 });
      });

      await test.step("Clearing the field restores exactly what was listed before it", async () => {
        await locators.clearSearch(locators.optimizationsSearch);
        await expect(locators.optimizationsSearch).toHaveValue("");
        await expect(locators.optimizationsRows).toHaveCount(baseline, { timeout: 60000 });
        if (baseline > 0) {
          await expect(locators.optimizationsEmpty).toHaveCount(0);
        }
      });
    }
  );

  test(
    "Auto Optimize - click the Approvals sub-tab then click back to Optimizations, verify each click moves the URL fragment and swaps which listing is mounted",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      await test.step("Clicking Approvals opens it and writes its fragment", async () => {
        await locators.approvalsSubTab.click();
        await parkCursor(page);
        await expectSelectedSubTab(locators.approvalsSubTab);
        await expect(page).toHaveURL(/#auto-optimize\/approvals\b/);
        await settleApprovals(locators);
        // The Optimizations listing is unmounted, not hidden — one sub-tab body renders
        // at a time, so this is what proves the click swapped the view.
        await expect(locators.optimizationsListing).toHaveCount(0);
      });

      await test.step("Clicking Optimizations brings the first listing back with its own toolbar", async () => {
        await locators.optimizationsSubTab.click();
        await parkCursor(page);
        await expectSelectedSubTab(locators.optimizationsSubTab);
        await expect(page).toHaveURL(/#auto-optimize\/optimizations\b/);
        await settleOptimizations(locators);
        await expect(locators.optimizationsStatusFilter).toBeVisible();
        await expect(locators.approvalsListing).toHaveCount(0);
      });
    }
  );

  test(
    "Auto Optimize Approvals - open /optimise#auto-optimize/approvals directly, verify the Approvals sub-tab is selected and its table renders the approval columns or its No Data Available state",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page, "approvals");

      await test.step("The deep link lands on Approvals rather than the default sub-tab", async () => {
        await expectSelectedSubTab(locators.approvalsSubTab);
        await expect(locators.optimizationsSubTab).toHaveAttribute("aria-selected", "false");
        await expect(locators.optimizationsListing).toHaveCount(0);
      });

      await test.step("The approvals toolbar renders its own account filter", async () => {
        await expect(locators.approvalsAccountFilter).toBeVisible();
      });

      await test.step("The table settles on approval rows or on its empty heading", async () => {
        const rows = await locators.rowCount(APPROVALS_TABLE, locators.approvalsEmpty);
        if (rows > 0) {
          for (const header of APPROVALS_HEADERS) {
            await expect(locators.approvalsTable.locator("th", { hasText: header }).first()).toBeVisible();
          }
        } else {
          // A tenant with nothing awaiting approval is a legitimate pass, not a missing
          // locator — the id'd heading is how that state is told apart from a skeleton.
          await expect(locators.approvalsEmpty).toBeVisible();
        }
      });
    }
  );

  test(
    "Auto Optimize - open the Create Auto Optimize menu, verify it offers all four rightsizing types, press Escape and verify the menu closes with no configuration form opened",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openAutoOptimize(page);

      await test.step("The create control is on the tab strip", async () => {
        // Rendered only when hasWriteAccess passes (app/src/pages/optimise/index.jsx), so
        // a read-only run user would fail here with a locator error rather than a reason.
        await expect(locators.createButton, "Create Auto Optimize is gated on write access — check the run user's role").toBeVisible({
          timeout: 60000,
        });
      });

      await test.step("The menu lists every rightsizing type the module can create", async () => {
        await locators.createButton.click();
        await expect(locators.createMenuItems).toHaveCount(CREATE_MENU_ITEMS.length, { timeout: 30000 });
        for (const item of CREATE_MENU_ITEMS) {
          // Each id is asserted to carry its own label, so an entry silently re-pointed
          // at a different rightsizing type would fail rather than just counting four.
          await expect(locators.createMenuItem(item.id)).toBeVisible();
          await expect(locators.createMenuItem(item.id)).toContainText(item.label);
        }
      });

      await test.step("Escape dismisses the menu without starting a configuration", async () => {
        await page.keyboard.press("Escape");
        // ds/DropdownMenu leaves keepMounted at false and /optimise does not override it,
        // so a dismissed menu unmounts its entries rather than merely hiding them.
        await expect(locators.createMenuItems).toHaveCount(0, { timeout: 30000 });
        // Each menu entry opens a Modal (ds/Modal wraps MUI Dialog, whose paper carries
        // role="dialog"). None open means the dismissal left no half-built configuration
        // behind on the shared tenant.
        await expect(page.getByRole("dialog")).toHaveCount(0);
        await expect(locators.optimizationsListing).toBeVisible();
      });
    }
  );
});
