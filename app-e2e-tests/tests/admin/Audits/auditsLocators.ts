// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { FILTER, FILTER_LABEL, escapeForRegex } from "./auditsConstants";

// Nothing the Audits surface renders carries a data-testid: AuditsTable,
// FilterDropdown, ListingLayout and AnchorComponent return 0 for
// `grep -c data-testid`, and the two testids CustomTable does render sit on the
// column-selector and column-resize handles, neither of which this suite
// touches. Rung 1 of the locator ladder therefore does not exist here, so the
// ids the components are explicitly given are the primary, with a role-based
// fallback scoped to the same card wherever the accessible name is unambiguous.
export class AuditsLocators extends CommonLocators {
  readonly auditsTab: Locator;
  readonly usersTab: Locator;
  readonly listingCard: Locator;
  readonly table: Locator;
  readonly emptyState: Locator;
  readonly dataRows: Locator;
  readonly paginationSummary: Locator;
  readonly pageSizeTrigger: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent.jsx:507 renders id=`anchor-tab-${name}` and stamps
    // data-tab-selected on the same element, which is what the tests assert on.
    this.auditsTab = page
      .locator('[id="anchor-tab-Audits"]')
      .or(page.getByRole("link", { name: "Audits", exact: true }))
      .first();
    this.usersTab = page
      .locator('[id="anchor-tab-Users"]')
      .or(page.getByRole("link", { name: "Users", exact: true }))
      .first();

    // ListingLayout renders its `id` on the wrapping DS Card (ListingLayout.tsx:202),
    // so #audit is the whole listing: toolbar, table and footer. Deliberately
    // id-only: the Card is a plain styled div with no role and no accessible
    // name, and it is the container every other locator scopes to — a wider
    // fallback here would silently widen all of them.
    this.listingCard = page.locator("#audit");

    // CustomTable renders id={id} on the table (CustomTable.jsx:1082), whose only
    // accessible name is aria-label='table' (CustomTable.jsx:1081).
    this.table = page.locator("#audits-table").or(this.listingCard.getByRole("table")).first();

    // EmptyData renders <h2 id={`${id}-no-data`}> (EmptyData.jsx:27). Scoped
    // heading fallback rather than a bare getByText: "No Data Available" is the
    // default empty copy of every table in the app.
    this.emptyState = page
      .locator("#audits-table-no-data")
      .or(this.listingCard.getByRole("heading", { name: "No Data Available" }))
      .first();

    // Each audit record renders as two <tr>: the data row, then a sibling row
    // holding the Collapse panel (CustomTable.jsx:456-466). Only the data row
    // carries the expand control, so filtering on it is what separates the two.
    // Counting `tr` alone would double every row count in this suite. No .or()
    // fallback by design: the filter is the whole point, and any wider match
    // would pull the collapse rows back in and corrupt every count below.
    this.dataRows = page
      .locator("#audits-table-body tr")
      .filter({ has: page.getByRole("button", { name: /^(Expand|Collapse) row$/ }) });

    // CustomTablePagination.jsx:69-75 — "Showing 1-10 of 1,234 results".
    // The footer is only rendered when the table has rows (CustomTable.jsx:939).
    // .first() because getByText resolves every ancestor whose textContent also
    // matches, which would be a strict-mode violation on the assertions below.
    this.paginationSummary = this.listingCard.getByText(/Showing\s+[\d,]+-[\d,]+\s+of\s+[\d,]+\s+results/).first();

    // The rows-per-page ds/Select is given neither an id nor a label
    // (CustomTablePagination.jsx:154), so its generated React id is not stable.
    // aria-haspopup="listbox" is the trigger's own contract (Select.tsx:538) and
    // is unique inside the card — the six toolbar FilterDropdowns are plain
    // buttons with no aria-haspopup.
    this.pageSizeTrigger = this.listingCard.locator('button[aria-haspopup="listbox"]').first();
  }

  // FilterDropdown.jsx:1035-1042 renders its trigger as
  // <button id={`auto-complete-${toKebabCase(id)}`}>. The ids passed in
  // audits/index.jsx contain no spaces or underscores, so they kebab to themselves.
  filterTrigger(key: keyof typeof FILTER): Locator {
    return this.page
      .locator(`#auto-complete-${FILTER[key]}`)
      .or(this.listingCard.getByRole("button", { name: new RegExp(`^${escapeForRegex(FILTER_LABEL[key])}`) }))
      .first();
  }

  // The panel's search box. Present only above eight options, so callers must
  // treat its absence as an expected outcome rather than a failure. Scoped to
  // the popover root — a MUI class, but the panel has no id or role of its own
  // and an unscoped match would reach into any other open dropdown.
  filterSearchInput(): Locator {
    return this.page.locator(".MuiPopover-root").getByPlaceholder("Search...").first();
  }

  // Options are role='option' boxes inside the open MUI Popover
  // (FilterDropdown.jsx:195-198). Scoping by :visible rather than by the popover
  // paper's MUI class: only one panel is ever open, and every other dropdown's
  // options are unmounted. Matches the pattern the admin specs already use.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  visibleOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  // Rendered by both OptionsList and GroupedOptionsList when a search matches
  // nothing (FilterDropdown.jsx:355, 555).
  noOptionsMessage(): Locator {
    return this.page.getByText("No results found", { exact: true }).first();
  }

  // The clear (x) replaces the chevron once a value is selected
  // (FilterDropdown.jsx:1169-1183). With no leading option icon on these
  // filters, it is the trigger's only svg — hence a scoped structural match
  // rather than a role: the svg carries no accessible name of its own.
  clearFilterButton(key: keyof typeof FILTER): Locator {
    return this.filterTrigger(key).locator("svg").first();
  }

  // Every cell of one column, across the data rows only, in row order.
  // `col` is a zero-based index into the COLUMN map; nth-child is 1-based.
  columnCells(col: number): Locator {
    return this.dataRows.locator(`td:nth-child(${col + 1})`);
  }

  columnHeader(name: string): Locator {
    return this.table.getByRole("columnheader", { name, exact: true }).first();
  }

  // CustomTable.jsx:435-437 — one IconButton per data row, whose accessible name
  // and aria-expanded both flip with the row's state.
  expandToggle(rowIndex: number): Locator {
    return this.dataRows.nth(rowIndex).getByRole("button", { name: /^(Expand|Collapse) row$/ });
  }

  // ds/Select portals its listbox to the body here (disablePortal={false} in
  // CustomTablePagination.jsx:161), so the option is reachable at document level.
  pageSizeOption(size: string): Locator {
    return this.page
      .locator('[role="listbox"] [role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(size)}$`) })
      .first();
  }

  // Opens a toolbar filter and commits `label`, coping with both panel shapes:
  // short lists render no search box at all.
  async chooseFilter(key: keyof typeof FILTER, label: string): Promise<void> {
    const trigger = this.filterTrigger(key);
    await trigger.click();

    // Wait on the panel's options, not on its search box: the box only exists
    // above eight options, so waiting for it would burn a full timeout on every
    // Status and Action selection just to conclude it was never coming.
    await this.visibleOptions().first().waitFor({ state: "visible", timeout: 15000 });

    // Options and the search box render in the same pass, so by this point its
    // absence is a settled answer rather than a race, and needs no timeout.
    const search = this.filterSearchInput();
    if (await search.isVisible()) {
      await search.fill(label);
    }

    await this.filterOption(label).click();
    // The trigger shows its label plus the committed value, so this is the
    // signal that the selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label);
  }

  async clearFilterValue(key: keyof typeof FILTER): Promise<void> {
    await this.clearFilterButton(key).click();
  }
}
