// Not for OSS
import { test, expect } from "@playwright/test";
import { LISTINGS, LISTING_ORDER } from "./appsAndInfraTablesLocators";
import { clearSearch, expectHeaders, noMatchTerm, NO_ROWS_HINT, openListing, searchAndApply, selectFilterOption, setup } from "./appsAndInfraTablesHelper";

// Apps & Infra — tabOptions[3] of the cluster detail page (fragment '#kubernetes'),
// nine listings of what the cluster actually runs. The sibling specs in this folder
// assert the GraphQL each sub-tab fires; nothing asserted the pages themselves, which
// is what this spec adds.
//
// Nothing here creates or deletes a Kubernetes object. The section is a read-only view
// of live cluster state — its only write is Applications' "Bulk assign owner", which
// overrides the owner of pre-existing workloads on a shared cluster, so it is covered
// up to its validation and its cancel and never through a submit. Written up in the
// PR's Follow-ups.
const SPEC_TIMEOUT_MS = 300000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Apps & Infra", () => {
  test(
    "Apps & Infra sanity - open the Apps & Infra section, verify the sub-tab strip lists all nine listings and opens on Nodes",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      await test.step("Every listing the section declares is on the strip", async () => {
        // tabOptions[3].tabOptions in app/src/pages/kubernetes/details/[KubernetesDetails].jsx.
        // These sub-tabs carry no `tabName`, so AnchorComponent's grouped-tab slicing
        // widens to the full range and all nine render at once rather than a group.
        for (const listing of LISTING_ORDER) {
          await expect(locators.subTab(listing)).toBeVisible();
        }
      });

      await test.step("The section opens on Nodes, and only Nodes", async () => {
        await expect(locators.subTab(LISTINGS.nodes)).toHaveAttribute("aria-selected", "true");
        for (const listing of LISTING_ORDER.filter((l) => l.id !== LISTINGS.nodes.id)) {
          await expect(locators.subTab(listing)).toHaveAttribute("aria-selected", "false");
        }
      });
    }
  );

  test(
    "Apps & Infra Nodes - open the Nodes listing, verify its nine column headers render and its State filter and Node Name search are offered",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openListing(locators, LISTINGS.nodes);

      await expectHeaders(locators, LISTINGS.nodes);

      await test.step("The toolbar offers the filter and the search the listing declares", async () => {
        await expect(locators.stateFilter).toBeVisible();
        await expect(locators.nodeSearch).toBeVisible();
      });
    }
  );

  test(
    "Apps & Infra Nodes - search a node name no node can match, verify the No Data Available panel replaces the rows, clear the search, verify the node rows return",
    { tag: ["@dev", "@regression", "@search", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openListing(locators, LISTINGS.nodes);

      const before = await locators.rows(LISTINGS.nodes).count();
      expect(before, NO_ROWS_HINT).toBeGreaterThan(0);

      await searchAndApply(locators.nodeSearch, noMatchTerm());

      // Retrying assertions, not a snapshot count: the search refetches, and reading the
      // table once would race the in-flight request and still see the pre-search rows.
      await expect(locators.rows(LISTINGS.nodes)).toHaveCount(0);
      await expect(locators.emptyState(LISTINGS.nodes)).toBeVisible();
      await expect(locators.emptyState(LISTINGS.nodes)).toHaveText("No Data Available");

      await clearSearch(locators.nodeSearch);
      await expect(locators.rows(LISTINGS.nodes)).toHaveCount(before);
    }
  );

  test(
    "Apps & Infra Nodes - filter State to Deleted, verify no Active node survives the filter, switch back to Active, verify every listed node reads Active again",
    { tag: ["@dev", "@regression", "@search", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openListing(locators, LISTINGS.nodes);

      // NODE_HEADERS puts Status eighth, and CustomTable appends the expand control
      // after the data cells rather than before them, so it does not shift the column.
      // The cell holds two things: the state Label ("Active" / "Deleted") and, after it,
      // a Text of the node's readiness conditions — so a whole-cell read returns
      // "ActiveReady", or "ActiveNotReady" / "...SchedulingDisabled" on other nodes.
      // Only the leading state token is under test here, so that is what is extracted.
      const statuses = () => locators.settledColumnValues(LISTINGS.nodes, 8, (cell) => cell.match(/^(Active|Deleted)/)?.[1] ?? cell);

      await test.step("The listing opens on Active, and every node it lists says Active", async () => {
        await expect(locators.rows(LISTINGS.nodes), NO_ROWS_HINT).not.toHaveCount(0);
        await expect.poll(statuses, { timeout: 60000 }).toBe("Active");
      });

      await test.step("Switching State to Deleted leaves no Active node listed", async () => {
        await selectFilterOption(page, locators, locators.stateFilter, "Deleted");
        // Either this cluster holds deleted nodes or the listing empties — both are
        // correct outcomes, and settledColumnValues only reports "" once the empty
        // panel is actually rendered. What must never survive the filter is an Active
        // row, which is what anything else in the column would show as a diff.
        await expect.poll(statuses, { timeout: 60000 }).toMatch(/^(Deleted)?$/);
      });

      await test.step("Switching back to Active restores the Active nodes", async () => {
        await selectFilterOption(page, locators, locators.stateFilter, "Active");
        await expect.poll(statuses, { timeout: 60000 }).toBe("Active");
        await expect(locators.rows(LISTINGS.nodes)).not.toHaveCount(0);
      });
    }
  );

  test(
    "Apps & Infra Applications - open the Applications listing, expand its first application, verify the row reports itself expanded, collapse it, verify the row reports itself collapsed again",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openListing(locators, LISTINGS.applications);

      expect(await locators.rows(LISTINGS.applications).count(), NO_ROWS_HINT).toBeGreaterThan(0);

      const toggle = locators.expandToggle(LISTINGS.applications);
      // CustomTable drives both the icon's aria-label and its aria-expanded off the same
      // collapse state, so these are the row's own account of whether it is open.
      await expect(toggle).toHaveAttribute("aria-expanded", "false");

      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-expanded", "true");
      await expect(toggle).toHaveAccessibleName("Collapse row");

      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-expanded", "false");
      await expect(toggle).toHaveAccessibleName("Expand row");
    }
  );

  test(
    "Apps & Infra Applications - open Bulk assign owner, verify Assign stays disabled with no namespace, workload or owner picked, cancel, verify the modal closes and the listing still lists its applications",
    { tag: ["@dev", "@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openListing(locators, LISTINGS.applications);

      const before = await locators.rows(LISTINGS.applications).count();
      expect(before, NO_ROWS_HINT).toBeGreaterThan(0);

      await expect(locators.bulkAssignOwnerBtn, "Bulk assign owner is gated on hasWriteAccess() — this user appears to be read-only.").toBeVisible({
        timeout: 30000,
      });
      await locators.bulkAssignOwnerBtn.click();
      await expect(locators.bulkAssignDialog).toBeVisible({ timeout: 30000 });

      // BulkAssignOwnerModal resets namespace, workloads and owner every time it opens
      // and gates Assign on all three, so a freshly opened modal can never submit.
      await expect(locators.bulkAssignSaveBtn).toBeDisabled();

      await locators.bulkAssignCancelBtn.click();
      await expect(locators.bulkAssignDialog).toBeHidden();

      // Cancel must leave the listing exactly as it was — nothing assigned, nothing refetched away.
      await expect(locators.rows(LISTINGS.applications)).toHaveCount(before);
    }
  );

  test(
    "Apps & Infra - move through the Namespace, Services, PVC and PV listings, verify each one mounts its own table and renders its own column headers",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      for (const listing of [LISTINGS.namespaces, LISTINGS.services, LISTINGS.pvc, LISTINGS.pv]) {
        await test.step(`${listing.label} mounts ${listing.tableId} and its own columns`, async () => {
          await openListing(locators, listing);
          await expect(locators.table(listing)).toBeVisible();
          await expectHeaders(locators, listing);
        });
      }
    }
  );

  test(
    "Apps & Infra - open the Databases and Queues listings, verify each mounts its own table with the Type, Name, Namespace and Status columns and offers a Status filter",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      for (const listing of [LISTINGS.dbms, LISTINGS.queue]) {
        await test.step(`${listing.label} mounts ${listing.tableId} and its Status filter`, async () => {
          await openListing(locators, listing);
          await expect(locators.table(listing)).toBeVisible();
          await expectHeaders(locators, listing);
          await expect(locators.statusFilter).toBeVisible();
        });
      }
    }
  );

  test(
    "Apps & Infra Pods - open the Pods listing, verify its pagination summary counts the rows the page is showing, switch to Nodes and back, verify Pods is selected again and still lists rows",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openListing(locators, LISTINGS.pods);

      const rows = await locators.rows(LISTINGS.pods).count();
      expect(rows, NO_ROWS_HINT).toBeGreaterThan(0);

      await test.step("The summary's range agrees with the rows actually on screen", async () => {
        // CustomTablePagination prints "Showing <start>-<end> of <total> results"; Pods is
        // server-paginated, so end - start + 1 is what this page returned.
        // The summary locator also matches the "No results found" wording the component
        // shows while totalRows is still 0, so wait for the range form before parsing —
        // a single read can otherwise catch that loading text and fail the match.
        await expect(locators.paginationSummary()).toHaveText(/Showing\s[\d,]+-[\d,]+\sof\s[\d,]+\sresults/);
        const summary = ((await locators.paginationSummary().textContent()) ?? "").replace(/,/g, "");
        const range = summary.match(/Showing\s(\d+)-(\d+)\sof\s(\d+)\sresults/);
        expect(range, `Pods pagination summary did not report a range: "${summary}"`).not.toBeNull();
        const [, start, end] = range as RegExpMatchArray;
        expect(Number(end) - Number(start) + 1).toBe(rows);
      });

      await test.step("Leaving Pods and coming back returns to the same listing", async () => {
        await openListing(locators, LISTINGS.nodes);
        await expect(locators.subTab(LISTINGS.pods)).toHaveAttribute("aria-selected", "false");

        await openListing(locators, LISTINGS.pods);
        await expect(locators.table(LISTINGS.pods)).toBeVisible();
        await expect(locators.rows(LISTINGS.pods)).toHaveCount(rows);
      });
    }
  );
});
