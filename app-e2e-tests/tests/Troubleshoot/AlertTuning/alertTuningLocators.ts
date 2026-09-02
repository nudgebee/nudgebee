// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { TroubleshootEventsLocators } from "../troubleshootEventsLocators";

// The five sources ThresholdSuggestionsManager.tsx offers (SOURCE_OPTIONS), in
// declaration order. All five render while no account is selected; the component
// narrows them to the selected accounts' platforms, so every assertion on this
// list runs before an account filter is applied.
export const SOURCE_LABELS = ["AWS CloudWatch", "Azure Monitor", "Prometheus", "GCP Metric Alert", "PagerDuty"] as const;

// CONFIDENCE_OPTIONS in the same file.
export const CONFIDENCE_LABELS = ["High", "Medium", "Low"] as const;

// The listing's columns in the multi-account view /troubleshoot mounts (the page
// renders <ThresholdSuggestionsManager /> with no accountId, so isMultiAccountView
// is true and the Account column is present).
export const ALERT_TUNING_COLUMNS = ["Account", "Alert", "Recommendation", "Threshold", "Confidence", "Noise Reduction"] as const;

// The module's own emptySubHeading. Generic to CustomTable is "Please check back
// later or try refreshing the page"; this copy is unique to Alert Tuning, which is
// what makes it proof the right listing emptied rather than a neighbouring one.
export const ALERT_TUNING_EMPTY_HINT =
  "Alert tuning suggestions are automatically generated for noisy alerts with 5+ firings in the last 30 days.";

// The two drilldown tabs the manager passes to CustomTable's `expandable`.
export const DRILLDOWN_TABS = { evidence: "Evidence", recentEvents: "Recent Events" } as const;

export class AlertTuningLocators extends TroubleshootEventsLocators {
  readonly listBox: Locator;
  readonly accountFilter: Locator;
  readonly downloadBtn: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;
  readonly emptyHint: Locator;
  readonly optionRows: Locator;
  readonly openPanelContent: Locator;
  readonly groupHeaders: Locator;
  readonly expandToggles: Locator;

  constructor(page: Page) {
    super(page);

    // thresholdToolbar / thresholdSourceFilter / thresholdConfidenceFilter come from
    // TroubleshootEventsLocators and are deliberately not re-declared here.

    // Deliberately no .or(): this is the scope for the empty-state and download
    // lookups below, and the page mounts a ListingLayout on sibling sub-tabs too — a
    // wider match would let one of those stand in for a listing that never rendered.
    this.listBox = page.locator("#threshold-suggestions-list-box");

    // FilterDropdown rewrites the id it is handed as `auto-complete-<kebab id>`
    // (FilterDropdown.jsx:1050). Same shape as the Source and Confidence triggers the
    // parent class declares, scoped to the same toolbar.
    this.accountFilter = this.thresholdToolbar
      .locator("#auto-complete-threshold-suggestions-filter-account")
      .or(this.thresholdToolbar.getByText("Account", { exact: true }))
      .first();

    // DownloadButton.jsx sets aria-label='Download' on the button and the manager
    // passes id={`${tableId}-download`}, so role+name leads over the id.
    this.downloadBtn = this.thresholdToolbar
      .getByRole("button", { name: "Download" })
      .or(this.thresholdToolbar.locator("#thresholdSuggestionsManager-download"))
      .first();

    // CustomTable puts the manager's tableId straight on the <table> and stamps
    // `${id}-body` on its TableBody. Deliberately no .or() on either: a wider match
    // would pick up whatever other table is mounted, turning "this listing emptied"
    // into a false pass.
    this.table = page.locator("#thresholdSuggestionsManager");
    this.tableBody = page.locator("#thresholdSuggestionsManager-body");

    // showExpandable renders a second <tr> per record for the collapse panel, so this
    // count is only ever compared against itself, never asserted as a record count.
    this.rows = this.tableBody.locator("tr");

    // With no suggestion the manager swaps the table for <EmptyData>, which stamps
    // `${id}-no-data` on its heading (common/EmptyData.jsx:26).
    this.emptyState = this.listBox
      .getByRole("heading", { name: "No Data Available" })
      .or(this.listBox.locator("#thresholdSuggestionsManager-no-data"))
      .first();
    this.emptyHint = this.listBox.getByText(ALERT_TUNING_EMPTY_HINT).first();

    // NOT getByRole("option"): FilterDropdown mounts its Popover with disablePortal
    // (FilterDropdown.jsx:792, and the manager does not override it) and MUI's
    // ModalManager marks that container aria-hidden while it is open, so the
    // accessibility tree exposes no options and getByRole matches zero. The role
    // attribute survives in the DOM. Same trap ClusterDetails/ServiceCriticality and
    // Optimize/AiGateway document for this component. disablePortal is also why the
    // panel can be scoped to the toolbar: it mounts inline, under the trigger's own
    // subtree, rather than at the end of <body>.
    this.optionRows = this.thresholdToolbar.locator('[role="option"]:visible');

    // Everything an open panel puts on screen: option rows plus the panel's own controls.
    // Used to tell "panel open" from "panel closed" without assuming option rows survive —
    // see pickFirstAccountOption below for why they do not. Nothing in the CLOSED toolbar
    // matches: ListingLayout and DownloadButton render no role attribute at all, and the
    // three FilterDropdown triggers are real <button> elements with an implicit role, so
    // every explicit role="button" in FilterDropdown.jsx (411, 583, 648, 671) is inside
    // the Popover.
    this.openPanelContent = this.thresholdToolbar.locator('[role="option"]:visible, [role="button"]:visible');

    // A group header in the grouped (Account) panel — the row that expands a provider's
    // accounts (FilterDropdown.jsx:583, onClick={toggleGroup}).
    //
    // The label exclusions are load-bearing, not cosmetic: three of the four explicit
    // role="button" nodes in this component are ACTIONS, not headers — the flat panel's
    // Select All/Clear (411) and the per-group "Clear"/"Clear All" (648) and "Select All"
    // (671). The per-group pair renders only inside an already-expanded group, so a naive
    // "click every role=button to expand everything" would start clicking them on the
    // second pass and silently mutate the tenant's selection mid-test.
    this.groupHeaders = this.thresholdToolbar
      .locator('[role="button"]:visible')
      .filter({ hasNotText: /^\s*(Clear|Clear All|Select All)\s*$/ });

    // CustomTable.jsx:466 gives the per-row chevron aria-label='Expand row' while the
    // row is closed. Scoped to the body so the count is this listing's rows only.
    this.expandToggles = this.tableBody.getByRole("button", { name: "Expand row" });
  }

  // One option row in the open FilterDropdown panel. Anchored on the full label so a
  // short one ("High") cannot also resolve a longer row that contains it, and because
  // the multi-select checkbox contributes no text of its own.
  filterOption(label: string): Locator {
    return this.optionRows.filter({ hasText: new RegExp(`^\\s*${escapeForRegex(label)}\\s*$`) }).first();
  }

  // A column header of the suggestions table. MUI TableCell in a TableHead renders
  // role="columnheader", so role+name leads; scoped to this table either way.
  columnHeader(name: string): Locator {
    return this.table.getByRole("columnheader", { name, exact: true }).first();
  }

  // A drilldown tab inside an expanded row. The manager passes numeric `value`s and no
  // `id`, so Tabs.jsx falls back to the generic `tab-0` / `tab-1` — ids that collide
  // with every other tab strip on the page. Role+name scoped to this table's body is
  // the only unambiguous handle, and the panel is inline (not in a Popover), so the
  // accessibility tree is intact here.
  drilldownTab(name: string): Locator {
    return this.tableBody.getByRole("tab", { name, exact: true }).first();
  }

  // The panel behind a drilldown tab. CustomTable.jsx:95 renders role='tabpanel' with
  // id=`table-tabpanel-<value>` and toggles `hidden`, so a visible panel is proof the
  // switch landed rather than only restyling the tab.
  drilldownPanel(index: number): Locator {
    return this.tableBody.locator(`[id="table-tabpanel-${index}"]`).first();
  }

  // Opens a single-select FilterDropdown and picks one option. FilterDropdown closes
  // itself on select in single mode (handleToggle's else branch calls setAnchorEl(null)),
  // so the panel's disappearance is the signal the choice was taken.
  async selectSingleFilter(trigger: Locator, optionLabel: string): Promise<void> {
    await trigger.click();
    await this.optionRows.first().waitFor({ state: "visible", timeout: 30000 });
    await this.filterOption(optionLabel).click();
    await this.optionRows.first().waitFor({ state: "hidden", timeout: 30000 });
  }

  // Opens the Account panel and leaves its option rows on screen, in both shapes the
  // component can take. FilterDropdown only renders group headers when the options span
  // more than one group (effectiveGrouped), and every group starts COLLAPSED — so on a
  // multi-provider tenant the panel opens with no option row visible until a header is
  // clicked, while a single-provider tenant renders the flat list with its rows already
  // there. Which shape the shared dev tenant produces is data this suite must not pin.
  //
  // Idempotent: the trigger is only clicked when the panel is actually shut. Clicking it
  // while open would land on the Modal's backdrop and close the panel instead of opening
  // it, which is the opposite of what the caller asked for.
  async openAccountPanel(): Promise<void> {
    if ((await this.openPanelContent.count()) === 0) {
      await this.accountFilter.click();
      await this.openPanelContent.first().waitFor({ state: "visible", timeout: 30000 });
    }
    if ((await this.optionRows.count()) === 0) {
      await this.groupHeaders.first().click();
      await this.optionRows.first().waitFor({ state: "visible", timeout: 30000 });
    }
  }

  // Picks the first account on offer and waits for the panel to shut behind it.
  //
  // The closure is the point. Selecting an account calls applyFiltersOnRouter, and that
  // router.push re-renders the manager: FilterDropdown remounts and its `anchorEl` resets,
  // so the panel closes on its own even though multi-select would otherwise keep it open.
  // CI proved this twice — run 536 found no `[role="option"]` left to press Escape on, and
  // run 537 found no group header left to click either. Waiting for the panel to be fully
  // gone is what makes the next openAccountPanel() honest rather than racing a popover
  // that is still unmounting.
  async pickFirstAccountOption(): Promise<void> {
    await this.optionRows.first().click();
    await expect(this.openPanelContent, "The Account panel did not close after a selection").toHaveCount(0, {
      timeout: 30000,
    });
  }

  // The label of the first account the panel offers, read rather than pinned: which accounts
  // the shared dev tenant holds is data this suite must not fix. Safe to read as plain text
  // because the Account options carry no icon or badge — the manager builds them as
  // {label, value, group} only, so the row's text is the account name and nothing else.
  async firstAccountOptionLabel(): Promise<string> {
    return ((await this.optionRows.first().innerText()) ?? "").trim();
  }
}

// Same local helper the ClusterDetails and Optimize locators declare — account labels
// on a shared tenant can carry regex metacharacters.
function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
