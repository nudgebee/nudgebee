// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeLocators } from "../Optimize/OptimizeLocators";

// Auto Optimize — the AutoPilot module mounted as tab 4 of /optimise
// (app/src/pages/optimise/index.jsx -> components/autopilot/tables/AutoOptimizeTabs.tsx).
// Two sub-tabs, each its own listing:
//   Optimizations -> tables/AutoOptimizeListingTable.jsx  (CustomTable id 'auto-pilot')
//   Approvals     -> tables/AutoPilotApprovalsTable.tsx   (CustomTable id 'approvals')
//
// Rung choice, measured rather than assumed. Every file behind this module renders
// zero data-testid attributes:
//   grep -c 'data-testid' on every file under app/src/components/autopilot -> 0 (14 files)
//   grep -c 'data-testid' app/src/pages/optimise/index.jsx                 -> 0
// So rung 1 does not exist here and the ids the components do render are the highest
// available rung. Where the element also carries a real accessible name that can be
// scoped without ambiguity, that name is the `.or()` fallback; where it does not, the
// locator is deliberately id-only and says so on the line above it.

// CustomTable puts the id on the <table> and `${id}-body` on the <tbody>, and swaps in
// an id-less skeleton <tbody> while loading. Neither listing passes showExpandable, so
// there is one <tr> per record — the second-cell guard is kept anyway so a later switch
// to an expandable table cannot silently double every count.
const DATA_ROW = "tr:has(td:nth-child(2))";

export const OPTIMIZATIONS_TABLE = "auto-pilot";
export const APPROVALS_TABLE = "approvals";

// LISTING_HEADER in AutoOptimizeListingTable.jsx. Used as 1-based nth-child indices.
export const STATUS_COLUMN = 2;
export const CATEGORY_COLUMN = 3;

// STATUS_OPTIONS / CATEGORY_OPTIONS in AutoOptimizeListingTable.jsx.
export const DEFAULT_STATUS = "Active";
export const DISABLED_STATUS = "Disabled";
// Of the four categories this is one of the three whose filter label matches the text
// the table prints for it. `continuous_rightsize` is filtered as "Vertical RightSizing"
// but rendered as "Continuous Vertical RightSizing" — raised under Follow-ups.
export const PVC_CATEGORY = "PVC RightSizing";

// Approvals column contract from the headers array in AutoPilotApprovalsTable.tsx.
export const APPROVALS_HEADERS = ["Name", "Description", "Status", "Comment", "Created at"];

// The four entries of the Create Auto Optimize menu (app/src/pages/optimise/index.jsx).
// ds/DropdownMenu forwards each item's `id` to its OverlayItem, so the id is the handle
// and the visible label is the fallback.
export const CREATE_MENU_ITEMS = [
  { id: "continuous_rightsize", label: "Continuous Vertical Right Sizing" },
  { id: "horizontal_rightsize", label: "Horizontal Right Sizing" },
  { id: "vertical_rightsize", label: "Scheduled Vertical Right Sizing" },
  { id: "pvc_rightsize", label: "PVC Right Sizing" },
];

export class AutoOptimizeLocators extends OptimizeLocators {
  // Sub-tab row.
  readonly optimizationsSubTab: Locator;
  readonly approvalsSubTab: Locator;

  // Create Auto Optimize.
  readonly createButton: Locator;
  readonly createMenuItems: Locator;

  // Optimizations sub-tab.
  readonly optimizationsListing: Locator;
  readonly optimizationsAccountFilter: Locator;
  readonly optimizationsStatusFilter: Locator;
  readonly optimizationsCategoryFilter: Locator;
  readonly optimizationsSearch: Locator;
  readonly optimizationsDownload: Locator;
  readonly optimizationsTable: Locator;
  readonly optimizationsRows: Locator;
  readonly optimizationsEmpty: Locator;

  // Approvals sub-tab.
  readonly approvalsListing: Locator;
  readonly approvalsAccountFilter: Locator;
  readonly approvalsTable: Locator;
  readonly approvalsEmpty: Locator;

  constructor(page: Page) {
    super(page);

    // navigation/Tabs.jsx puts the tabOption's own `id` on the MUI <Tab> via a11yProps,
    // and MUI renders it role="tab". No other role="tab" exists inside this module, so
    // the role fallback cannot resolve to something else on the page.
    this.optimizationsSubTab = page.locator("#Optimizations").or(page.getByRole("tab", { name: "Optimizations" })).first();
    this.approvalsSubTab = page.locator("#approvals").or(page.getByRole("tab", { name: "Approvals" })).first();

    this.createButton = page.locator("#create-auto-optimize").or(page.getByRole("button", { name: "Create Auto Optimize" })).first();

    // The menu is identified by its own items, NOT by a `[role="menu"]` wrapper. Every
    // row of the Optimizations table mounts a ThreeDotsMenu that stays in the DOM while
    // closed, and the header account menu adds another — `[role="menu"]` resolved to 8
    // elements in CI (run 32434364345), all sharing the same MUI classes, so nothing but
    // the contents tells them apart. These four ids are unique page-wide.
    //
    // Id-only by design: ds/DropdownMenu gives OverlayItem no testid, and a role+name
    // fallback would reach into the row menus, whose items sit under the same role.
    this.createMenuItems = page.locator(CREATE_MENU_ITEMS.map((item) => `#${item.id}`).join(", "));

    // ds/ListingLayout puts the caller's id on a plain <Box> — no role, no accessible
    // name, no testid. Deliberately id-only: this locator is the scope every control
    // below falls back inside, so a wider match would defeat the scoping itself.
    this.optimizationsListing = page.locator("#box-layout-auto-pilot");

    // ds/FilterDropdown renders its trigger as a <button id={`auto-complete-${kebab(id)}`}>
    // whose leading text is the label, so getByRole is a genuine fallback — scoped to the
    // listing because the Approvals sub-tab renders an identically labelled Account filter.
    this.optimizationsAccountFilter = this.filterTrigger("auto-pilot-filter-account", "Account", this.optimizationsListing);
    this.optimizationsStatusFilter = this.filterTrigger("auto-pilot-filter-status", "Status", this.optimizationsListing);
    this.optimizationsCategoryFilter = this.filterTrigger("auto-pilot-filter-category", "Category", this.optimizationsListing);

    // ds/SearchInput forwards the id to the <input> itself and passes `label` through as
    // the placeholder, so the id (rung 3) outranks the placeholder (rung 4).
    this.optimizationsSearch = page
      .locator("#auto-pilot-name-search")
      .or(this.optimizationsListing.getByPlaceholder("Search by name or id"))
      .first();

    // DownloadButton is given no id by this listing, but it does carry aria-label="Download".
    this.optimizationsDownload = this.optimizationsListing.getByRole("button", { name: "Download" }).first();

    // The table locators below are id-only by design: CustomTable renders no testid, and
    // a <table> carries no accessible name to fall back to. The ids are its own published
    // contract (`id`, `${id}-body`, `${id}-no-data`), so there is no safer wider match.
    this.optimizationsTable = page.locator(`#${OPTIMIZATIONS_TABLE}`);
    this.optimizationsRows = this.rowsFor(OPTIMIZATIONS_TABLE);
    // Neither listing passes showEmptyStateText or showUpdatedEmptyData, so both take
    // CustomTable's default empty branch, which ids its heading `${tableId}-no-data`.
    this.optimizationsEmpty = page.locator(`#${OPTIMIZATIONS_TABLE}-no-data`);

    // Id-only for the same reason as the Optimizations listing above.
    this.approvalsListing = page.locator("#autopilot-approvals-listing-box");
    this.approvalsAccountFilter = this.filterTrigger("auto-pilot-approvals-filter-account", "Account", this.approvalsListing);
    this.approvalsTable = page.locator(`#${APPROVALS_TABLE}`);
    this.approvalsEmpty = page.locator(`#${APPROVALS_TABLE}-no-data`);
  }

  // Both rungs are scoped to the tab's own listing. `.or()` resolves in document order,
  // so a page-wide id fallback could return the other sub-tab's identically labelled
  // Account filter and drive the wrong control.
  private filterTrigger(id: string, label: string, scope: Locator): Locator {
    return scope
      .getByRole("button", { name: new RegExp(`^${label}`) })
      .or(scope.locator(`#auto-complete-${id}`))
      .first();
  }

  // One entry of the Create Auto Optimize menu, by the id ds/DropdownMenu forwards from
  // the item to its OverlayItem (app/src/pages/optimise/index.jsx:194-208).
  createMenuItem(id: string): Locator {
    return this.page.locator(`#${id}`);
  }

  rowsFor(tableId: string): Locator {
    return this.page.locator(`#${tableId}-body ${DATA_ROW}`);
  }

  // One column of every rendered row, as a single locator, so a whole-table claim
  // ("nothing here is still Active") can be made with a retrying count assertion.
  cellsInColumn(tableId: string, column: number): Locator {
    return this.page.locator(`#${tableId}-body ${DATA_ROW} td:nth-child(${column})`);
  }

  // Waits out the fetch. While `loading` is true CustomTable swaps in a skeleton <tbody>
  // that carries no id, so the id'd body being attached means the response has landed
  // and the rows on screen are the current ones. The empty heading is the other settled
  // outcome — which of the two shows depends on what the shared dev tenant holds.
  async waitForTable(tableId: string, emptyState: Locator, timeout = 60000): Promise<void> {
    await this.page.locator(`#${tableId}-body`).or(emptyState).first().waitFor({ state: "attached", timeout });
  }

  // Only for a baseline taken before any interaction. After an action that refetches,
  // assert the end state with a retrying expect(...).toHaveCount(n) instead — a plain
  // count races the in-flight request and reads the stale table.
  async rowCount(tableId: string, emptyState: Locator): Promise<number> {
    await this.waitForTable(tableId, emptyState);
    return this.rowsFor(tableId).count();
  }

  // FilterDropdown is single-select here: picking an option fires onSelect and closes
  // the popover, so the option leaving the DOM is the signal the pick was committed.
  //
  // Anchored and escaped, because one category label is a strict prefix of another
  // ("Vertical RightSizing" vs "Scheduled Vertical RightSizing") — a substring match
  // would pick whichever the popover happened to render first. Page-scoped rather than
  // scoped to the trigger: MUI portals the Popover to <body>, so the options are not
  // inside it. Only one popover is open at a time.
  async pickFilterOption(trigger: Locator, optionLabel: string): Promise<void> {
    await trigger.click();
    const exact = new RegExp(`^${optionLabel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
    const option = this.page.locator('[role="option"]').filter({ hasText: exact }).first();
    await option.click();
  }

  // SearchInput commits on Enter (onEnterPress in ds/SearchInput.jsx) — typing alone
  // filters nothing and leaves the applied name untouched.
  async searchAndApply(input: Locator, term: string): Promise<void> {
    await input.click();
    await input.fill(term);
    await input.press("Enter");
  }

  // The X only renders while the field holds a value, so clearing goes through the field
  // itself. SearchInput's onChange fast path already drops the applied name on an empty
  // value; the Enter re-commits it so the listing refetches either way.
  async clearSearch(input: Locator): Promise<void> {
    await input.click();
    await input.fill("");
    await input.press("Enter");
  }
}
