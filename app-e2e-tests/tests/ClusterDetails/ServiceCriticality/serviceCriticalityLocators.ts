// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { TroubleshootTabLocator } from "../Troubleshoot/TroubleshootTabLocator";

// Cluster Details > Events > Service Criticality, rendered by
// app/src/components/criticality/WorkloadCriticalityManager.tsx (sub-tab value 10).
//
// The component renders exactly ONE data-testid — ListingLayout.Toolbar spreads its
// `data-testid` onto the toolbar Box. Every other control below is a DS primitive that
// forwards only an `id` (or nothing at all), so rung 3 / rung 4 primaries here are the
// highest rung actually available, not a shortcut. Checked with:
//   grep -c 'data-testid' app/src/components/criticality/WorkloadCriticalityManager.tsx  -> 1
export const CRITICALITY_TABLE = "workloadCriticality";

// CustomTable puts the caller id on the <table> and `${id}-body` on the <tbody>
// (app/src/components/common/tables/CustomTable.jsx). This table declares no
// `expandable` tabs and no `showExpandable`, so ExpandableTableRow emits a single <tr>
// per workload — but the second-cell guard is kept anyway so the counts stay right if
// a drill-down is ever added.
const DATA_ROW = "tr:has(td:nth-child(2))";

// The four tiers WorkloadCriticalityManager offers, in its own LEVELS order.
export const CRITICALITY_LEVELS = ["critical", "high", "medium", "low"] as const;

// `tiered_only` returns only workloads with a persisted row. Medium is the unpersisted
// default, so it is exactly the tier that disappears when "Only classified" is on.
export const DEFAULT_TIER = "medium";

export class ServiceCriticalityLocators extends TroubleshootTabLocator {
  readonly listingCard: Locator;
  readonly toolbar: Locator;
  readonly infoBanner: Locator;
  readonly infoBannerDismissBtn: Locator;
  readonly searchInput: Locator;
  readonly namespaceFilter: Locator;
  readonly tierFilter: Locator;
  readonly onlyClassifiedSwitch: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;
  readonly optionRows: Locator;
  readonly loadFailureToast: Locator;

  constructor(page: Page) {
    super(page);

    // ListingLayout forwards `id` to its Card root; the toolbar inside it is the only
    // node carrying a testid, so it doubles as the card's structural fallback.
    this.listingCard = page.locator("#workload-criticality-list-box").or(page.getByTestId("workload-criticality-toolbar").locator("xpath=..")).first();
    this.toolbar = page.getByTestId("workload-criticality-toolbar").or(page.locator("#workload-criticality-list-box").locator("> div").first()).first();

    // ds/Banner forwards `id` to its root and gives an info-tone banner role="status".
    // The fallback is filtered by the banner's own title so it cannot resolve onto a
    // Toast, which shares that role.
    this.infoBanner = page.locator("#workload-criticality-info").or(page.getByRole("status").filter({ hasText: "Why set service criticality?" })).first();
    this.infoBannerDismissBtn = this.infoBanner.getByRole("button", { name: "Dismiss" }).or(this.infoBanner.locator('[aria-label="Dismiss"]')).first();

    // ds/SearchInput is given only `label`, which ds/Input renders as the placeholder —
    // no id reaches the DOM. Placeholder is the input's accessible name, so role is the
    // primary; both rungs are scoped to the toolbar so neither can drift onto the
    // global cluster autocomplete.
    this.searchInput = this.toolbar.getByRole("textbox", { name: "Search workload" }).or(this.toolbar.getByPlaceholder("Search workload")).first();

    // ds/FilterDropdown renders its trigger as <button id={`auto-complete-${kebab(id)}`}>.
    // Its accessible name is the label PLUS the current selection, so it changes as soon
    // as a filter is applied — the id is the only stable handle. Fallback matches the
    // label prefix inside the toolbar for the unselected state.
    this.namespaceFilter = page.locator("#auto-complete-criticality-filter-namespace").or(this.toolbar.getByRole("button", { name: /^Namespace/ })).first();
    this.tierFilter = page.locator("#auto-complete-criticality-filter-tier").or(this.toolbar.getByRole("button", { name: /^Criticality/ })).first();

    // ds/Switch passes `aria-label` through to the underlying MUI checkbox input.
    this.onlyClassifiedSwitch = this.toolbar
      .getByRole("checkbox", { name: "Show only workloads with an assigned tier" })
      .or(this.toolbar.locator('input[type="checkbox"]'))
      .first();

    // No fallback on the next three: CustomTable puts aria-label="table" on EVERY table
    // in the app and renders no testid, so the caller id is the only handle that can
    // name this one — any wider match would resolve onto another table on the page.
    this.table = page.locator(`#${CRITICALITY_TABLE}`);
    this.tableBody = page.locator(`#${CRITICALITY_TABLE}-body`);
    this.rows = page.locator(`#${CRITICALITY_TABLE}-body ${DATA_ROW}`);

    // Every open filter panel's option rows. See filterOption() for why this is the role
    // attribute rather than getByRole. No .or(): the attribute is the contract here.
    this.optionRows = page.locator('[role="option"]');

    // WorkloadCriticalityManager renders its own EmptyData in place of the table; that
    // component puts `${id}-no-data` on the <h2>, so the heading role is the primary.
    this.emptyState = this.listingCard.getByRole("heading", { name: "No Data Available" }).or(page.locator("#workload-criticality-empty-no-data")).first();

    // The snackbar the module raises when the list request fails. Asserted absent so a
    // broken backend cannot masquerade as a legitimately empty listing. No fallback on
    // purpose: this is an exact-text absence check, and a wider match would report some
    // other toast as this failure.
    this.loadFailureToast = page.getByText("Failed to load workload criticality", { exact: true });
  }

  // Either the loaded table body or the module's empty panel — never both. While the
  // request is in flight CustomTable swaps in a skeleton <tbody> that carries no id, so
  // the id'd body being attached is what proves the response has landed.
  async waitForListing(timeout = 60000): Promise<void> {
    await this.listingCard.waitFor({ state: "visible", timeout });
    await this.tableBody.or(this.emptyState).first().waitFor({ state: "attached", timeout });
  }

  // Runs `action` and resolves once the module's own list call comes back. The toolbar's
  // switch refetches server-side, and the previous <tbody> stays attached throughout —
  // so waiting on the DOM alone reads the stale table.
  async waitForListRequest(action: () => Promise<void>, timeout = 60000): Promise<void> {
    const settled = this.page.waitForResponse(
      (response) => response.url().includes("/api/graphql") && (response.request().postData() ?? "").includes("WorkloadListCriticality"),
      { timeout }
    );
    await action();
    await settled;
    await this.waitForListing(timeout);
  }

  // Column order is data-driven — CustomTable renders only the headers left visible by
  // the column selector — so cells are addressed by resolving the header's position at
  // runtime rather than by a hardcoded nth that silently reads the wrong column.
  async columnIndex(header: string): Promise<number> {
    const headerCells = this.table.locator("thead th");
    await expect(headerCells.first()).toBeVisible({ timeout: 30000 });
    const total = await headerCells.count();
    for (let i = 0; i < total; i++) {
      if (((await headerCells.nth(i).textContent()) ?? "").trim() === header) {
        return i;
      }
    }
    throw new Error(`Column "${header}" is not rendered in the Service Criticality table`);
  }

  async columnValues(header: string): Promise<string[]> {
    const index = await this.columnIndex(header);
    const cells = this.rows.locator(`td:nth-child(${index + 1})`);
    return (await cells.allTextContents()).map((value) => value.trim());
  }

  async cellValue(row: Locator, header: string): Promise<string> {
    const index = await this.columnIndex(header);
    return ((await row.locator(`td:nth-child(${index + 1})`).textContent()) ?? "").trim();
  }

  // ds/SearchInput has no onEnterPress here, so the parent filters on every keystroke —
  // filling the field is the whole interaction, there is nothing to commit.
  async setSearch(term: string): Promise<void> {
    await this.searchInput.click();
    await this.searchInput.fill(term);
    await expect(this.searchInput).toHaveValue(term);
  }

  async clearSearch(): Promise<void> {
    await this.searchInput.click();
    await this.searchInput.fill("");
    await expect(this.searchInput).toHaveValue("");
  }

  // ds/FilterDropdown's options are Boxes with role="option" inside a MUI Popover, and
  // the Popover aria-hidden's the surrounding subtree — so getByRole("option") resolves
  // to ZERO here, exactly as it does for this app's MUI menu items. Matched by the role
  // attribute instead. Anchored on the full text because the checkbox contributes none:
  // a bare substring would let one namespace match a longer one that contains it.
  filterOption(label: string): Locator {
    const exact = new RegExp(`^\\s*${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);
    return this.optionRows.filter({ hasText: exact }).or(this.optionRows.filter({ has: this.page.getByText(label, { exact: true }) })).first();
  }

  // ds/FilterDropdown opens a Popover of role="option" rows and stays open in multi
  // mode, so the panel is dismissed explicitly and its closure waited on — a still-open
  // panel covers the toolbar and swallows the next click.
  async applyFilter(trigger: Locator, optionName: string): Promise<void> {
    await trigger.click();
    // Assert the panel opened before hunting for one row in it, so a trigger that did
    // not open reports that rather than masquerading as a missing option.
    await expect(this.optionRows.first(), "The filter panel did not open").toBeVisible({ timeout: 30000 });
    const option = this.filterOption(optionName);
    await option.waitFor({ state: "visible", timeout: 30000 });
    await option.click();
    // FilterDropdown closes on Escape through the Popover's own onKeyDown, so the key has
    // to land on a node INSIDE the panel. A page-level press does not: selecting an option
    // re-keys its row from the unselected list into the Selected section, React unmounts
    // the node under the cursor, and focus falls back to <body> outside the Popover.
    // Observed in CI — a page-level Escape left all 47 namespace options on screen.
    await this.optionRows.first().press("Escape");
    await expect(this.optionRows, "The filter panel did not close").toHaveCount(0, { timeout: 30000 });
  }

  // The per-row overflow trigger. ds/Button renders composition='icon-only' with the
  // aria-label as its only text, so role+name is the primary rung here.
  rowActionTrigger(row: Locator): Locator {
    return row.getByRole("button", { name: "Change criticality" }).or(row.locator('[aria-label="Change criticality"]')).first();
  }

  // ds/DropdownMenu items are Boxes with role="menuitem" whose label sits in nested
  // spans, so getByRole(name) computes an empty accessible name for them — the same
  // trap the repo's TaskRunner locators document. Matched by text instead, scoped to
  // the open overlay so it cannot resolve onto page content behind the menu.
  menuItem(label: string): Locator {
    return this.page.locator('[role="menuitem"]').filter({ hasText: label }).or(this.page.locator('[role="menu"]').getByText(label, { exact: true })).first();
  }
}
