// Not for OSS
import { test, expect } from "@playwright/test";
import { distinctStatuses, expectSelectedTab, openTickets, requireTickets } from "./ticketsHelper";
import { escapeRegex } from "./ticketsLocators";

// Tickets (/tickets) — the listing module behind the Tickets rail button, its two tabs,
// its six toolbar facets and the per-row drill-down.
//
// Everything here reads or filters. The module's only write is the Jira comment box in the
// drill-down, which posts to a real ticket in a shared project and cannot be taken back —
// so it is left alone; see "Follow-ups" in the PR.

test.beforeEach(async () => {
  test.setTimeout(180000);
});

// A term no ticket title can match, so the no-results assertion is about the filter
// working rather than about what the environment happens to hold.
const NO_MATCH_TERM = `zz-no-such-ticket-${Date.now()}`;

// One of the six priority tiles TicketListInfograph always renders — the array is a fixed
// list in the component, padded to full length whether or not the API returns counts for
// each severity, so the tile is on screen regardless of this environment's data.
const FIXED_PRIORITY_TILE = "Medium";

test(
  "Tickets sanity - open the Tickets module, verify the toolbar offers the Account, Priority, Tool, Status and Assignee filters, the Title search and the Download action",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const tickets = await openTickets(page);

    await test.step("The listing card and its five filter facets are on screen", async () => {
      await expect(tickets.listingRoot).toBeVisible();
      for (const facet of [
        tickets.accountFilter,
        tickets.priorityFilter,
        tickets.toolFilter,
        tickets.statusFilter,
        tickets.assigneeFilter,
      ]) {
        await expect(facet).toBeVisible();
      }
    });

    await test.step("The title search and the export action are on screen", async () => {
      await expect(tickets.titleSearch).toBeVisible();
      await expect(tickets.downloadBtn).toBeVisible();
    });

    await test.step("The table declares every ticket column", async () => {
      for (const column of ["Ticket ID", "Tool", "Title", "Priority", "Status", "Account", "Created By", "Assignee", "Created At"]) {
        await expect(tickets.columnHeader(column)).toBeVisible();
      }
    });
  }
);

test(
  "Tickets sanity - open the Tickets module, verify the tab strip offers All Tickets and Assigned to me and lands on All Tickets",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const tickets = await openTickets(page);

    await expect(tickets.allTicketsTab).toBeVisible();
    await expect(tickets.assignedToMeTab).toBeVisible();
    await expect(tickets.allTicketsTab).toHaveText(/All Tickets/);
    await expect(tickets.assignedToMeTab).toHaveText(/Assigned to me/);

    // The selected tab, not the URL: a bare /tickets settles without a fragment.
    await expectSelectedTab(tickets.allTicketsTab);
  }
);

test(
  "Tickets - search the listing for a title no ticket can hold, clear the search, verify the No Data Available panel replaces the rows and the original rows come back",
  { tag: ["@dev", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    const tickets = await openTickets(page);
    await requireTickets(tickets);

    const baseline = await tickets.rowCount();

    await test.step("A term that matches no title empties the table", async () => {
      await tickets.searchByTitle(NO_MATCH_TERM);
      // Retrying assertions, not a snapshot count: the search refetches, and reading the
      // table once would race the in-flight request and see the pre-search rows.
      await expect(tickets.rows).toHaveCount(0);
      await expect(tickets.noDataHeading).toBeVisible();
      await expect(tickets.noDataHeading).toHaveText("No Data Available");
    });

    await test.step("Clearing the search restores the original listing", async () => {
      await tickets.clearTitleSearch();
      await expect(tickets.titleSearch).toHaveValue("");
      await expect(tickets.rows).toHaveCount(baseline);
    });
  }
);

test(
  "Tickets - read the status off the first ticket, select that status in the Status filter, verify every listed ticket now carries only that status",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    const tickets = await openTickets(page);
    await requireTickets(tickets);

    // Taken from the data rather than hardcoded: which statuses exist is a property of the
    // environment, and filtering by one a ticket actually has is what makes the assertion
    // below about the filter instead of about the tenant's ticket mix.
    await expect(tickets.statusCells.first()).toBeVisible();
    const status = ((await tickets.statusCells.first().textContent()) ?? "").trim();
    expect(status, "The first ticket rendered no status to filter by").not.toBe("");

    await tickets.pickFilterOption(tickets.statusFilter, status);

    await test.step("The trigger reports the selection", async () => {
      await expect(tickets.statusFilter).toContainText(new RegExp(escapeRegex(status), "i"));
    });

    await test.step("The listing keeps only tickets in that status", async () => {
      // expect.poll IS the wait here: the pick kicks off a refetch, so the first read
      // would still hold the unfiltered rows.
      await expect
        .poll(async () => distinctStatuses(tickets), { timeout: 60000 })
        .toEqual([status.toLowerCase()]);
    });
  }
);

test(
  "Tickets - select a value in the Priority filter, click Clear All, verify the trigger drops the value and the full listing returns",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const tickets = await openTickets(page);
    await requireTickets(tickets);

    const baseline = await tickets.rowCount();
    await expect(tickets.clearAllFilters).toBeHidden();

    const priority = await tickets.firstOptionLabel(tickets.priorityFilter);
    expect(priority, "The Priority filter offered no options").not.toBe("");

    await test.step("Picking a priority arms the filter and reveals Clear All", async () => {
      await tickets.optionByLabel(priority).click();
      await expect(tickets.filterOptions).toHaveCount(0);
      await expect(tickets.priorityFilter).toContainText(new RegExp(escapeRegex(priority), "i"));
      await expect(tickets.clearAllFilters).toBeVisible();
    });

    await test.step("Clear All drops the selection and restores every row", async () => {
      await tickets.clearAllFilters.click();
      await expect(tickets.priorityFilter).not.toContainText(new RegExp(escapeRegex(priority), "i"));
      await expect(tickets.clearAllFilters).toBeHidden();
      await expect(tickets.rows).toHaveCount(baseline);
    });
  }
);

test(
  `Tickets - click the ${FIXED_PRIORITY_TILE} tile in the Priority summary, click it again, verify the Priority filter adopts ${FIXED_PRIORITY_TILE} and then releases it`,
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const tickets = await openTickets(page);

    const tile = tickets.summaryTile(FIXED_PRIORITY_TILE);
    await expect(tile).toBeVisible();
    await expect(tickets.priorityFilter).not.toContainText(FIXED_PRIORITY_TILE);

    await test.step("The tile drives the toolbar filter, not just the chart", async () => {
      await tile.click();
      await expect(tickets.priorityFilter).toContainText(FIXED_PRIORITY_TILE);
      await expect(tickets.clearAllFilters).toBeVisible();
      await tickets.waitForTable();
    });

    await test.step("Clicking the same tile again releases the filter", async () => {
      await tile.click();
      await expect(tickets.priorityFilter).not.toContainText(FIXED_PRIORITY_TILE);
      await expect(tickets.clearAllFilters).toBeHidden();
    });
  }
);

test(
  "Tickets - expand the first ticket row, verify the Ticket Details drilldown shows its Description and Additional Details, then collapse it",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const tickets = await openTickets(page);
    await requireTickets(tickets);

    const firstRow = tickets.rows.first();

    await test.step("The drilldown opens with the Ticket Details panel", async () => {
      await tickets.expandRowBtn(firstRow).click();
      // The chevron's aria-label flips with its state, so the collapse control carrying
      // aria-expanded=true is the app's own statement that the row is open.
      await expect(tickets.collapseRowBtn(firstRow)).toHaveAttribute("aria-expanded", "true");
      await expect(tickets.ticketDetailsTab).toBeVisible();
      await expect(tickets.detailsDescriptionHeading).toBeVisible();
      await expect(tickets.detailsAdditionalHeading).toBeVisible();
    });

    await test.step("Collapsing puts the row back", async () => {
      await tickets.collapseRowBtn(firstRow).click();
      await expect(tickets.expandRowBtn(firstRow)).toHaveAttribute("aria-expanded", "false");
    });
  }
);

test(
  "Tickets - switch to the Assigned to me tab and back, verify the Assignee filter is withdrawn there and restored on All Tickets",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const tickets = await openTickets(page);

    await expect(tickets.assigneeFilter).toBeVisible();

    await test.step("Assigned to me drops the Assignee facet, which it scopes for you", async () => {
      await tickets.assignedToMeTab.click();
      await expectSelectedTab(tickets.assignedToMeTab);
      await tickets.waitForTable();
      // enableAssigneeFilter is false on this tab (app/src/pages/tickets/index.jsx), so the
      // facet is not rendered at all — the query already pins the signed-in user.
      await expect(tickets.assigneeFilter).toBeHidden();
      await expect(tickets.statusFilter).toBeVisible();
    });

    await test.step("All Tickets brings the Assignee facet back", async () => {
      await page.mouse.move(640, 500);
      await tickets.allTicketsTab.click();
      await expectSelectedTab(tickets.allTicketsTab);
      await tickets.waitForTable();
      await expect(tickets.assigneeFilter).toBeVisible();
    });
  }
);
