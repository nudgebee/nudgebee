// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { TroubleshootEventsLocators } from "../troubleshootEventsLocators";
import { INBOX_LISTING_CARD, INBOX_TABLE, INBOX_LIST_OPERATION } from "./triageInboxConstants";

// Troubleshoot > All Events > Triage Inbox, rendered by
// app/src/components/k8s/details/groupedevents/KubernetesGroupedEventsTable.tsx with
// groupEventType='fingerprint', hideScopeFilters and isTroubleshootPage.
//
// The component renders four data-testids and they are all inside a row — the NEW /
// GROUPED chips and the Investigate action. Every toolbar control is a DS primitive
// that forwards only an id, so rung 3 there is the highest rung available, not a
// shortcut. Checked with:
//   grep -c 'data-testid' app/src/components/k8s/details/groupedevents/KubernetesGroupedEventsTable.tsx  -> 4

// Direct children only. The drill-down panel mounts a nested KubernetesEventsTable
// inside the second <tr> of an expanded row, and that table's own rows are
// DESCENDANTS of this tbody — a descendant match would silently inflate every row
// count the moment a row is expanded. The second-cell guard then drops the collapse
// <tr>, which holds a single colSpan cell.
const DATA_ROW = "> tr:has(td:nth-child(2))";

export class TriageInboxLocators extends TroubleshootEventsLocators {
  readonly listingCard: Locator;
  readonly downloadBtn: Locator;
  readonly eventTypeFilter: Locator;
  readonly sourceFilter: Locator;
  readonly severityFilter: Locator;
  readonly statusFilter: Locator;
  readonly triagePriorityFilter: Locator;
  readonly issueTypeFilter: Locator;
  readonly sortByFilter: Locator;
  readonly triageStatusFilter: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;
  readonly optionRows: Locator;

  constructor(page: Page) {
    super(page);

    // ListingLayout forwards `id` to its root Box. Deliberately no .or(): this is the
    // scope for the empty-state lookup below, and /troubleshoot mounts a different
    // ListingLayout card on every sibling sub-tab — a wider match would let one of
    // those stand in for a listing that never rendered.
    this.listingCard = page.locator(`[id="${INBOX_LISTING_CARD}"]`);

    // DownloadButton sets aria-label='Download' on the button and forwards its id, so
    // role+name leads and the id is the fallback. Both scoped to the card.
    this.downloadBtn = this.listingCard
      .getByRole("button", { name: "Download" })
      .or(this.listingCard.locator("#triage-inbox-download"))
      .first();

    // ds/FilterDropdown renders its trigger as <div id={`auto-complete-${kebab(id)}`}>,
    // and the ids here come from `filter-${label.toLowerCase().replace(/\s+/g,'-')}`.
    // The id leads for all eight: the trigger's visible text is the label PLUS the
    // current selection, so a text-based primary stops matching the moment a filter is
    // applied — which is exactly when these tests need it. The fallback matches the
    // label prefix inside the card, valid only while nothing is selected.
    this.eventTypeFilter = this.filterTrigger("filter-event-type", /^Event Type/);
    this.sourceFilter = this.filterTrigger("filter-source", /^Source/);
    this.severityFilter = this.filterTrigger("filter-severity", /^Severity/);
    this.statusFilter = this.filterTrigger("filter-status", /^Status/);
    this.triagePriorityFilter = this.filterTrigger("filter-triage-priority", /^Triage Priority/);
    this.issueTypeFilter = this.filterTrigger("filter-issue-type", /^Issue Type/);
    this.sortByFilter = this.filterTrigger("filter-sort-by", /^Sort By/);
    this.triageStatusFilter = this.filterTrigger("filter-triage-status", /^Triage Status/);

    // No fallback on the next three: CustomTable stamps aria-label="table" on EVERY
    // table in the app and renders no testid, so the caller id is the only handle that
    // names this one. The id carries a space, hence the attribute form — "#Grouped
    // Events" parses as an id plus a descendant and matches nothing.
    this.table = page.locator(`[id="${INBOX_TABLE}"]`);
    this.tableBody = page.locator(`[id="${INBOX_TABLE}-body"]`);
    this.rows = this.tableBody.locator(DATA_ROW);

    // CustomTable swaps the table for <EmptyData>, which stamps `${id}-no-data` on its
    // <h2> — so the heading role leads and the id is the fallback, both scoped to the
    // card. The inbox has no search box, so this state is reached only by a filter
    // combination that genuinely matches nothing; it is asserted as a tolerated branch
    // of "the listing resolved", never forced.
    this.emptyState = this.listingCard
      .getByRole("heading", { name: "No Data Available" })
      .or(this.listingCard.locator(`[id="${INBOX_TABLE}-no-data"]`))
      .first();

    // Every open filter panel's option rows. See filterOption() for why this is the
    // role attribute rather than getByRole. No .or(): the attribute is the contract.
    this.optionRows = page.locator('[role="option"]');
  }

  private filterTrigger(id: string, labelPrefix: RegExp): Locator {
    return this.listingCard.locator(`#auto-complete-${id}`).or(this.listingCard.getByText(labelPrefix)).first();
  }

  // Either the loaded table body or the empty panel — never both. While a request is
  // in flight CustomTable swaps in a skeleton <tbody> that carries no id, so the id'd
  // body being attached is what proves the response has landed.
  async waitForListing(timeout = 60000): Promise<void> {
    await this.listingCard.waitFor({ state: "visible", timeout });
    await this.tableBody.or(this.emptyState).first().waitFor({ state: "attached", timeout });
  }

  // Runs `action` and resolves once the inbox's own list call comes back. Every filter
  // and sort change refetches server-side while the previous <tbody> stays attached,
  // so waiting on the DOM alone reads the stale table.
  async waitForListRequest(action: () => Promise<void>, timeout = 60000): Promise<void> {
    const settled = this.page.waitForResponse(
      (response) => response.url().includes("api/graphql") && (response.request().postData() ?? "").includes(INBOX_LIST_OPERATION),
      { timeout }
    );
    await action();
    await settled;
    await this.waitForListing(timeout);
  }

  // Column order is data-driven — CustomTable renders only the headers left visible by
  // its column selector — so cells are addressed by resolving the header's position at
  // runtime rather than by a hardcoded nth that silently reads the wrong column.
  async columnIndex(header: string): Promise<number> {
    const index = (await this.headerNames()).indexOf(header);
    if (index === -1) {
      throw new Error(`Column "${header}" is not rendered in the Triage Inbox table`);
    }
    return index;
  }

  async headerNames(): Promise<string[]> {
    const headerCells = this.table.locator("thead th");
    await expect(headerCells.first()).toBeVisible({ timeout: 30000 });
    return (await headerCells.allTextContents()).map((value) => value.trim());
  }

  // Direct-child cells only, for the same nested-table reason as DATA_ROW.
  async columnValues(header: string): Promise<string[]> {
    const index = await this.columnIndex(header);
    const cells = this.rows.locator(`> td:nth-child(${index + 1})`);
    return (await cells.allTextContents()).map((value) => value.trim());
  }

  async cellValue(row: Locator, header: string): Promise<string> {
    const index = await this.columnIndex(header);
    return ((await row.locator(`> td:nth-child(${index + 1})`).textContent()) ?? "").trim();
  }

  // ds/FilterDropdown's options are Boxes with role="option" inside a MUI Popover, and
  // the Popover aria-hidden's the surrounding subtree — so getByRole("option") resolves
  // to ZERO here, as it does for this app's MUI menu items. CommonLocators.getOption()
  // is getByRole-based and fails for exactly that reason, so it is not reused. Matched
  // by the role attribute instead, anchored on the full text because a bare substring
  // would let "All Issues" also match nothing and "Events" match a longer sibling.
  filterOption(label: string): Locator {
    const exact = new RegExp(`^\\s*${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);
    return this.optionRows
      .filter({ hasText: exact })
      .or(this.optionRows.filter({ has: this.page.getByText(label, { exact: true }) }))
      .first();
  }

  // Opens a filter panel and picks one option. Every filter these tests drive is
  // single-select, and FilterDropdown closes its own Popover on a single-select pick
  // (setAnchorEl(null) at FilterDropdown.jsx:964) — so the panel closing is the signal
  // the choice registered, and no Escape is needed. A multi-select filter would need
  // the Escape-inside-the-panel dance instead; none is driven here.
  async selectFilterOption(trigger: Locator, optionName: string): Promise<void> {
    await trigger.click();
    // Assert the panel opened before hunting for one row in it, so a trigger that did
    // not open reports that rather than masquerading as a missing option.
    await expect(this.optionRows.first(), "The filter panel did not open").toBeVisible({ timeout: 30000 });
    const option = this.filterOption(optionName);
    await option.waitFor({ state: "visible", timeout: 30000 });
    await option.click();
    await expect(this.optionRows, "The filter panel did not close after a single-select pick").toHaveCount(0, { timeout: 30000 });
  }

  // The row's severity marker. ds/SeverityIcon renders role="img" with the raw severity
  // as its accessible name when it has no visible label.
  rowSeverityIcon(row: Locator, severity: string): Locator {
    return row.getByRole("img", { name: severity, exact: true }).or(row.locator(`[aria-label="${severity}"]`)).first();
  }

  // The NEW chip, rendered only when the row's is_new_issue is true — the same field
  // the Issue Type filter selects on, which is what makes it a real outcome check.
  rowNewIssueChip(row: Locator): Locator {
    return row.getByTestId("new-issue-chip");
  }

  // The row's primary action. It is an <a> whose label depends on whether the event has
  // already been analysed, so both testids are accepted; ds/Button renders the label as
  // its own text, so role+name is a sound fallback.
  rowInvestigateLink(row: Locator): Locator {
    return row
      .getByTestId("investigate-btn")
      .or(row.getByTestId("view-analysis-btn"))
      .or(row.getByRole("link", { name: /^(Investigate|View Analysis)$/ }))
      .first();
  }

  // The per-row classify trigger. ds/Button renders composition='icon-only' with the
  // aria-label as its only text, so role+name is the primary rung.
  rowClassifyTrigger(row: Locator): Locator {
    return row.getByRole("button", { name: "Classify" }).or(row.locator('[aria-label="Classify"]')).first();
  }

  // ds/DropdownMenu items are Boxes with role="menuitem" whose label sits in nested
  // spans, so getByRole(name) computes an empty accessible name for them — the trap the
  // repo's TaskRunner locators document. Matched by text instead, scoped to the open
  // overlay so it cannot resolve onto page content behind the menu.
  menuItem(label: string): Locator {
    return this.page
      .locator('[role="menuitem"]')
      .filter({ hasText: label })
      .or(this.page.locator('[role="menu"]').getByText(label, { exact: true }))
      .first();
  }
}
