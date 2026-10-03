// Not for OSS
import { Page, Locator } from "@playwright/test";
import { TroubleshootLocators } from "../TroubleshootLocators";
import {
  ACCOUNT_FILTER_ID,
  ACCOUNT_FILTER_LABEL,
  DOWNLOAD_BUTTON_ID,
  DOWNLOAD_BUTTON_LABEL,
  EMPTY_STATE_ID,
  LISTING_ID,
  NEXT_PAGE_LABEL,
  PAGINATION_SUMMARY_PATTERN,
  RESOLVER_FILTER_ID,
  RESOLVER_FILTER_LABEL,
  STATUS_FILTER_ID,
  STATUS_FILTER_LABEL,
  TABLE_ID,
  TYPE_FILTER_ID,
  TYPE_FILTER_LABEL,
} from "./eventResolutionsConstants";

// Rung choice, measured rather than assumed. Counted before a locator was written:
//   grep -c data-testid app/src/components/troubleshoot/EventResolutions.jsx   -> 0
//   grep -c data-testid app/src/components/common/ds/FilterDropdown.jsx        -> 0
//   grep -c data-testid app/src/components/common/buttons/DownloadButton.jsx   -> 0
//   grep -c data-testid app/src/components/common/tables/CustomTable.jsx       -> 2
// The module publishes no testid of its own, and CustomTable's two are the column-selector and
// resize handles, neither of which this suite touches — so rung 1 does not exist anywhere on
// this surface. Every locator below therefore takes the highest rung that does: role+name
// wherever the component gives the element a real accessible name (ds/FilterDropdown and
// ds/Button both render real buttons, MUI Pagination labels its own items), with the id the app
// publishes as the same-element `.or()` fallback. Locators that are deliberately id-only say so
// on the line above them.

// CustomTable emits one <tr> per data row here — EventResolutions passes no showExpandable, so
// there is no second collapse row per record. The second-cell guard costs nothing and keeps the
// counts right if this table ever gains one.
const DATA_ROW = "tr:has(td:nth-child(2))";

export class EventResolutionsLocators extends TroubleshootLocators {
  // Listing shell.
  readonly listing: Locator;
  readonly downloadButton: Locator;

  // Toolbar filters.
  readonly accountFilter: Locator;
  readonly statusFilter: Locator;
  readonly typeFilter: Locator;
  readonly resolverFilter: Locator;

  // Listing body.
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;

  // Pagination.
  readonly paginationSummary: Locator;
  readonly nextPageButton: Locator;

  constructor(page: Page) {
    super(page);

    // ds/ListingLayout puts the caller's id straight on a ds/Card, which forwards it to a plain
    // Box — no role, no accessible name, no testid. Deliberately id-only: this is the scope the
    // toolbar controls fall back inside, so a wider match would defeat the scoping itself.
    this.listing = page.locator(`#${LISTING_ID}`);

    // DownloadButton wraps ds/Button and sets aria-label="Download" on it
    // (DownloadButton.jsx:128), so role+name is a real rung 2. Scoped to the listing because
    // other panes of /troubleshoot stay mounted and carry download buttons of their own.
    this.downloadButton = this.listing
      .getByRole("button", { name: DOWNLOAD_BUTTON_LABEL })
      .or(this.listing.locator(`#${DOWNLOAD_BUTTON_ID}`))
      .first();

    this.accountFilter = this.filterTrigger(ACCOUNT_FILTER_ID, ACCOUNT_FILTER_LABEL);
    this.statusFilter = this.filterTrigger(STATUS_FILTER_ID, STATUS_FILTER_LABEL);
    this.typeFilter = this.filterTrigger(TYPE_FILTER_ID, TYPE_FILTER_LABEL);
    this.resolverFilter = this.filterTrigger(RESOLVER_FILTER_ID, RESOLVER_FILTER_LABEL);

    // Id-only by design: CustomTable renders no testid, and a <table>/<tbody> carries no
    // accessible name, so the ids it publishes are the only handle there is.
    this.table = page.locator(`#${TABLE_ID}`);
    this.tableBody = page.locator(`#${TABLE_ID}-body`);
    this.rows = page.locator(`#${TABLE_ID}-body ${DATA_ROW}`);

    // The empty branch replaces the table entirely rather than rendering inside it. Id-only:
    // EmptyData gives its <h2> no role and no testid.
    this.emptyState = page.locator(`#${EMPTY_STATE_ID}`);

    // CustomTablePagination renders the range line as bare Typography — no id, no role, no
    // testid — so scoped text (rung 4) is the highest rung that exists, and there is no lower
    // rung to fall back to that would not be a positional match on the pagination box. Scoped
    // to the listing card so it cannot match another pane's pagination while that pane is
    // still mounted.
    this.paginationSummary = this.listing.getByText(PAGINATION_SUMMARY_PATTERN).first();

    // MUI PaginationItem labels its own next control, which is a genuine accessible name.
    // Deliberately without an `.or()` fallback: the only alternative is a positional match on
    // the <ul>'s last child, and an index chain is exactly what the ladder rules out.
    this.nextPageButton = this.listing.getByRole("button", { name: NEXT_PAGE_LABEL }).first();
  }

  // Both rungs land on ds/FilterDropdown's own trigger button. Its accessible name starts with
  // the label and continues with the current selection, so the match is anchored rather than
  // exact. Scoped to the listing: `.or()` resolves in document order, and a page-wide id
  // fallback could reach a same-named filter on a sibling pane that is still mounted.
  private filterTrigger(id: string, label: string): Locator {
    return this.listing
      .getByRole("button", { name: new RegExp(`^${label}`) })
      .or(this.listing.locator(`#auto-complete-${id}`))
      .first();
  }

  // One column across every rendered row, as a single locator, so a whole-table claim ("every
  // Status cell now reads Failed") can be made with a retrying assertion instead of a
  // read-then-compare that races the refetch behind the filter.
  cellsInColumn(column: number): Locator {
    return this.page.locator(`#${TABLE_ID}-body ${DATA_ROW} td:nth-child(${column})`);
  }

  // One column heading. Matched by exact scoped text rather than by position: CustomTable's
  // <thead> holds TWO rows (CustomTable.jsx:1132 and :1166) — a sizing row of empty cells above
  // the labelled one — so a th index does not line up with a column index. Exact text also
  // keeps "Resolution" off the "Resolution Details" heading.
  columnHeading(name: string): Locator {
    return this.table.locator("thead").getByText(name, { exact: true }).first();
  }

  // ds/FilterDropdown portals its options into a MUI Popover, so they are not inside the
  // trigger's container and have to be reached from the page. `.last()` is the one just opened;
  // only one popover is open at a time.
  openFilterPanel(): Locator {
    return this.page.locator(".MuiPopover-paper:visible").last();
  }

  // One option row of an open filter panel. Matched by the CSS role attribute rather than by
  // getByRole: MUI marks the popover's container aria-hidden while it is open, so the
  // accessibility tree exposes no options and a role query matches zero — the form every other
  // spec in this suite uses against this same component. Anchored and escaped so one option
  // label cannot match another that extends it ("Ticket" vs a longer label starting with it).
  filterOption(panel: Locator, label: string): Locator {
    const exact = new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
    return panel.locator('[role="option"]').filter({ hasText: exact }).first();
  }

  // Every option row of an open filter panel, for reading back what a filter actually offers.
  filterOptions(panel: Locator): Locator {
    return panel.locator('[role="option"]');
  }

  // The collapsible group headers of a grouped panel. FilterDropdown gives each one role="button"
  // (FilterDropdown.jsx:583) and nests its per-group clear / select-all actions inside it, so
  // document order puts the header itself ahead of its own children. A panel that is still
  // loading renders neither these nor any option — only an aria-busy box or a "No results found"
  // line — which is what lets a caller tell "not populated yet" apart from "collapsed".
  filterGroupHeaders(panel: Locator): Locator {
    return panel.locator('[role="button"]');
  }

  // The option rows an open panel currently shows as selected. FilterDropdown sets
  // aria-selected on each row it renders (FilterDropdown.jsx:199-201), so this is how a
  // rehydrated selection is read back off the panel rather than off the trigger's label.
  selectedFilterOptions(panel: Locator): Locator {
    return panel.locator('[role="option"][aria-selected="true"]');
  }
}
