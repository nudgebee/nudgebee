// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Every handle below is read off the components that render this module, not guessed:
//   app/src/components/tickets/TicketListTable.jsx — ListingLayout id='list-ticket',
//     CustomTable id='all-tickets', FilterDropdown id='ticket-filter-<facet>',
//     SearchInput id='ticket-filter-title', data-testid='clear-all-filters'.
//   app/src/components/common/ds/FilterDropdown.jsx re-wraps the caller's id as
//     `auto-complete-${toKebabCase(id)}` on the trigger button, so 'ticket-filter-status'
//     is reachable as '#auto-complete-ticket-filter-status'.
//   app/src/components/common/ds/SearchInput.jsx forwards its id to ds/Input, which puts
//     it on the <input> itself and uses `label` as the placeholder.
//   app/src/components/common/tables/CustomTable.jsx puts `${id}-body` on the <tbody> and
//     renders EmptyData's <h2 id={`${id}-no-data`}> in its place when there are no rows.
//   app/src/components/common/navigation/AnchorComponent.jsx renders
//     `anchor-tab-${opt.id || opt.name}`; the tickets page supplies no id, so the tab ids
//     carry the visible names verbatim — spaces included, hence the [id="..."] form.
export const TICKETS_TABLE = "all-tickets";

// CustomTable emits a second <tr> per data row to hold the collapsed drill-down. It sits
// in the DOM whether or not the row is open and carries a single colSpan cell, so every
// row locator requires a second cell — otherwise every count is silently doubled.
const DATA_ROW = "tr:has(td:nth-child(2))";

// Column position in TICKET_HEADERS (TicketListTable.jsx). Status is the only facet
// readable from a row: Priority renders a SeverityIcon with no text and Tool a bare
// provider glyph, so neither can be asserted from the table body.
const STATUS_CELL = 5;

// Every regex built from a value read off the page has to go through this. Ticket data is
// not guaranteed to be regex-safe: dev currently serves a priority literally named
// "&{%!d(string=3)}", and unescaped that parses as a capture group, so the pattern matches
// a string the page never contains and the assertion times out on text that is plainly
// there. Cost a CI cycle on run 32302158587.
export function escapeRegex(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Anchored, case-insensitive text match for one option row. Case matters here: ds/Label
// renders a ticket's status through CSS `text-transform: capitalize` while the filter
// option shows the API's raw value, so "open" in the panel is "Open" in the row.
function exactText(text: string): RegExp {
  return new RegExp(`^\\s*${escapeRegex(text)}\\s*$`, "i");
}

export class TicketsLocators extends CommonLocators {
  readonly listingRoot: Locator;
  readonly ticketsTable: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly noDataHeading: Locator;
  readonly statusCells: Locator;

  readonly accountFilter: Locator;
  readonly priorityFilter: Locator;
  readonly toolFilter: Locator;
  readonly statusFilter: Locator;
  readonly assigneeFilter: Locator;
  readonly titleSearch: Locator;
  readonly clearAllFilters: Locator;
  readonly downloadBtn: Locator;

  readonly allTicketsTab: Locator;
  readonly assignedToMeTab: Locator;

  readonly filterOptions: Locator;

  readonly ticketDetailsTab: Locator;
  readonly detailsDescriptionHeading: Locator;
  readonly detailsAdditionalHeading: Locator;

  constructor(page: Page) {
    super(page);

    // ListingLayout renders no testid and its root Box has no accessible name, so the id
    // it was given is the only contract there is.
    this.listingRoot = page.locator("#list-ticket");
    // No fallbacks on the five below on purpose. CustomTable renders two testids
    // (column-selector-btn, column-resize-handle-<i>) and neither is on the table, the
    // body or the empty panel, and none of them carries an accessible name — so the ids it
    // derives from the caller's `id` are the only contract. A wider match would be worse
    // than no match: `table`, `tbody tr` and `h2` all resolve against whatever else the
    // page renders, and a row count taken off the wrong table reads as a passing filter.
    this.ticketsTable = page.locator(`#${TICKETS_TABLE}`);
    this.tableBody = page.locator(`#${TICKETS_TABLE}-body`);
    this.rows = page.locator(`#${TICKETS_TABLE}-body ${DATA_ROW}`);
    this.noDataHeading = page.locator(`#${TICKETS_TABLE}-no-data`);
    this.statusCells = page.locator(`#${TICKETS_TABLE}-body ${DATA_ROW} td:nth-child(${STATUS_CELL})`);

    // Each FilterDropdown trigger is a <button> whose accessible name opens with its
    // label and then gains the selected value, so the fallback anchors on the label only.
    // Scoped to the listing root: the same labels appear on other pages' toolbars.
    this.accountFilter = page
      .locator("#auto-complete-ticket-filter-account")
      .or(this.listingRoot.getByRole("button", { name: /^Account/ }))
      .first();
    this.priorityFilter = page
      .locator("#auto-complete-ticket-filter-priority")
      .or(this.listingRoot.getByRole("button", { name: /^Priority/ }))
      .first();
    this.toolFilter = page
      .locator("#auto-complete-ticket-filter-tool")
      .or(this.listingRoot.getByRole("button", { name: /^Tool/ }))
      .first();
    this.statusFilter = page
      .locator("#auto-complete-ticket-filter-status")
      .or(this.listingRoot.getByRole("button", { name: /^Status/ }))
      .first();
    this.assigneeFilter = page
      .locator("#auto-complete-ticket-filter-assignee")
      .or(this.listingRoot.getByRole("button", { name: /^Assignee/ }))
      .first();

    this.titleSearch = page.locator("#ticket-filter-title").or(this.listingRoot.getByPlaceholder("Title")).first();
    this.clearAllFilters = page
      .getByTestId("clear-all-filters")
      .or(this.listingRoot.getByRole("button", { name: "Clear all filters" }))
      .first();
    // TicketListTable passes DownloadButton no id, so its aria-label is the handle.
    this.downloadBtn = this.listingRoot.getByRole("button", { name: "Download" }).first();

    // The tab strip and the sidebar flyout both offer a link named "All Tickets", so the
    // id is the primary here rather than the role — the role match is ambiguous. The
    // fallback is scoped by data-tab-selected, which only AnchorComponent's tabs carry.
    this.allTicketsTab = page
      .locator('[id="anchor-tab-All Tickets"]')
      .or(page.locator("[data-tab-selected]").filter({ hasText: "All Tickets" }))
      .first();
    this.assignedToMeTab = page
      .locator('[id="anchor-tab-Assigned to me"]')
      .or(page.locator("[data-tab-selected]").filter({ hasText: "Assigned to me" }))
      .first();

    // Deliberately the attribute selector, NOT getByRole("option"). FilterDropdown mounts
    // its Popover with disablePortal (FilterDropdown.jsx:784) and MUI's ModalManager then
    // marks the containing subtree aria-hidden while the panel is open, so the
    // accessibility tree holds no options at all and getByRole matches zero — proved by
    // run 32301342577, where both dropdown tests timed out on an option that was plainly
    // on screen. The role attribute is still in the DOM. Same trap, and the same fix, as
    // tests/admin/roles/rolesLocators.ts:186. Unscoped is safe: a FilterDropdown Popover
    // unmounts on close, so at most one panel is mounted at a time.
    this.filterOptions = page.locator('[role="option"]:visible');

    this.ticketDetailsTab = page.getByRole("tab", { name: "Ticket Details" }).first();
    // The drill-down's two section headings are plain Typography with no id or role, so
    // they are matched on text and scoped to the table body — the only place in the module
    // that renders them, and only while a row is expanded.
    this.detailsDescriptionHeading = this.tableBody.getByText("Description", { exact: true }).first();
    this.detailsAdditionalHeading = this.tableBody.getByText("Additional Details", { exact: true }).first();
  }

  // Column headers are TableCells whose text is the header name; scoped to the tickets
  // table so the same word on another table on screen cannot satisfy the assertion.
  columnHeader(name: string): Locator {
    return this.ticketsTable.locator("thead th").filter({ hasText: exactText(name) }).first();
  }

  // The expand chevron is an IconButton whose aria-label is the app's own statement of
  // what it does, and it flips to "Collapse row" once open — there is no wider match that
  // would still mean "expand this row".
  expandRowBtn(row: Locator): Locator {
    return row.getByRole("button", { name: "Expand row" }).first();
  }

  collapseRowBtn(row: Locator): Locator {
    return row.getByRole("button", { name: "Collapse row" }).first();
  }

  // Summary tiles are TitleWithValue nodes with role=button and an explicit
  // aria-label='Filter by <title>' (TicketListInfograph.jsx).
  summaryTile(title: string): Locator {
    return this.page.getByRole("button", { name: `Filter by ${title}` }).first();
  }

  optionByLabel(label: string): Locator {
    return this.filterOptions.filter({ hasText: exactText(label) }).first();
  }

  // Waits out the fetch. While `loading` is true CustomTable swaps in a skeleton <tbody>
  // that carries no id, and with no rows it drops the body entirely and renders the empty
  // panel instead — so either node being attached means the response has landed.
  async waitForTable(timeout = 60000): Promise<void> {
    await this.tableBody.or(this.noDataHeading).first().waitFor({ state: "attached", timeout });
  }

  // Only for a baseline taken before any interaction. After an action that refetches,
  // assert the expected end state with a retrying expect(rows).toHaveCount(n) instead —
  // a plain count races the in-flight request and reads the stale table.
  async rowCount(): Promise<number> {
    await this.waitForTable();
    return this.rows.count();
  }

  // SearchInput commits on Enter (onEnterPress) — typing alone filters nothing.
  async searchByTitle(term: string): Promise<void> {
    await this.titleSearch.click();
    await this.titleSearch.fill(term);
    await this.titleSearch.press("Enter");
  }

  // The X only renders while the field holds a value, so clearing goes through the field
  // itself and re-commits, matching SearchInput's onChange('') fast path.
  async clearTitleSearch(): Promise<void> {
    await this.titleSearch.click();
    await this.titleSearch.fill("");
    await this.titleSearch.press("Enter");
  }

  async openFilter(trigger: Locator): Promise<void> {
    await trigger.click();
    await expect(this.filterOptions.first()).toBeVisible();
  }

  // Reads the first option's label out of an already-open panel, so a test can filter by
  // a value this environment actually offers instead of one hardcoded here.
  async firstOptionLabel(trigger: Locator): Promise<string> {
    await this.openFilter(trigger);
    return ((await this.filterOptions.first().textContent()) ?? "").trim();
  }

  // Single-select FilterDropdowns close themselves on pick, so the options going away is
  // the signal that the selection was committed rather than the click merely landing.
  // Asserted on the option count rather than on a panel container: the count cannot pass
  // vacuously the way a container locator that matched nothing in the first place would.
  async pickFilterOption(trigger: Locator, label: string): Promise<void> {
    await this.openFilter(trigger);
    await this.optionByLabel(label).click();
    await expect(this.filterOptions).toHaveCount(0);
  }
}
