// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Escapes a literal string for use inside a RegExp. Declared locally, as the
// sibling Automations and TaskRunner locator files each do.
function esc(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Executions tab of /automation — app/src/components/workflow/execution-dashboard/.
// Sibling of tests/workflow/Automations (tab 1) and tests/workflow/TaskRunner (tab 3).
//
// Testid coverage is split across this module, so the ladder rung differs per
// control and each choice below is the highest one the app actually offers:
// ExecutionSummaryCards, MostFailedAutomations and ExecutionDetailDrawer render
// data-testids, so those are reached by testid. The toolbar's ds/FilterDropdown
// triggers and the shared CustomTable render none between them (both greped at 0
// and 2, the two on CustomTable being the column-selector and resize handles that
// none of these tests touch), so those fall to their ids.
export class ExecutionsLocators extends CommonLocators {
  readonly automationSidenavBtn: Locator;
  readonly automationsTab: Locator;
  readonly executionsTab: Locator;

  readonly dashboardRoot: Locator;
  readonly scopeCaption: Locator;

  readonly summaryCard: Locator;
  readonly shareSucceeded: Locator;
  readonly shareTimedOut: Locator;
  readonly mostFailedPanel: Locator;
  readonly mostFailedRows: Locator;

  readonly refreshBtn: Locator;
  readonly accountFilter: Locator;
  readonly automationFilter: Locator;
  readonly userFilter: Locator;
  readonly statusFilter: Locator;

  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;
  readonly rowRangeSummary: Locator;

  readonly drawerTitle: Locator;
  readonly drawer: Locator;
  readonly drawerCloseBtn: Locator;
  readonly drawerFullViewBtn: Locator;

  constructor(page: Page) {
    super(page);

    // Id first: the rail renders the module name as an aria-label, but the hover
    // flyout repeats "Automations" as a sub-item, so role+name is ambiguous here.
    this.automationSidenavBtn = page.locator("#auto-pilot-sidenavbutton").or(page.locator('a[href="/automation"]')).first();
    this.automationsTab = page.locator("#anchor-tab-automations").or(page.locator('a[href="/automation#automations"]')).first();
    this.executionsTab = page.locator("#anchor-tab-executions").or(page.locator('a[href="/automation#executions"]')).first();

    // Deliberately id-only: the dashboard is an unlabelled ListingLayout box, and
    // any widened match (a bare div, the page body) would scope the toolbar and
    // table locators below to the whole page instead of to the dashboard.
    this.dashboardRoot = page.locator("#execution-dashboard");
    // The caption that states the Account filter is page-level while the rest are
    // table-level. No fallback: the copy is the only thing identifying this line.
    this.scopeCaption = this.dashboardRoot.getByText(/Account applies to the whole page/).first();

    // Testid marks the loaded card only — the in-flight branch of
    // ExecutionSummaryCards renders the same id with no testid, so this is also
    // the signal that the aggregate has landed rather than a skeleton.
    this.summaryCard = page.getByTestId("execution-dashboard-summary");
    this.shareSucceeded = page.getByTestId("execution-dashboard-share-succeeded");
    this.shareTimedOut = page.getByTestId("execution-dashboard-share-timed-out");
    // Same split: the loading panel carries `-loading`, so this matches the real
    // one only. The panel renders nothing at all when no failure was ranked.
    this.mostFailedPanel = page.getByTestId("execution-dashboard-most-failed");
    // Role+name over the per-row testid on purpose: the row testids are prefixed
    // `execution-dashboard-most-failed-`, which a prefix match would share with
    // the loading panel's own testid. The aria-label cannot collide.
    this.mostFailedRows = page.getByRole("button", { name: /^Filter executions to / });

    this.refreshBtn = page
      .getByTestId("execution-dashboard-refresh-btn")
      .or(this.dashboardRoot.getByRole("button", { name: "Refresh executions" }))
      .first();

    this.accountFilter = this.filterTrigger("execution-dashboard-filter-account", "Account");
    this.automationFilter = this.filterTrigger("execution-dashboard-filter-automation", "Automation");
    this.userFilter = this.filterTrigger("execution-dashboard-filter-user", "User");
    this.statusFilter = this.filterTrigger("execution-dashboard-filter-status", "Status");

    this.table = page.locator("#execution-dashboard-table").or(this.dashboardRoot.getByRole("table")).first();
    // Deliberately id-only. CustomTable renders its loading skeleton in a second
    // <tbody> that carries no id, so a widened tbody match would resolve to the
    // skeleton — exactly what this locator exists to exclude.
    this.tableBody = page.locator("#execution-dashboard-table-body");
    // One <tr> per data row: the dashboard passes no `expandable` and no
    // `showExpandable`, so CustomTable's second drill-down <tr> is never emitted.
    this.rows = this.tableBody.locator("tr");
    // No fallback: this copy is the only thing identifying the empty panel, and a
    // wider match would also catch the error branch that replaces it.
    this.emptyState = this.dashboardRoot.getByText("No executions in this range.").first();
    // The "Showing a-b of n results" line. Absent at zero rows: CustomTable's
    // renderPaginationOrViewAll returns null then.
    this.rowRangeSummary = this.dashboardRoot.getByText(/Showing\s+[\d,]+-[\d,]+\s+of\s+[\d,]+\s+results/).first();

    // MUI's temporary Drawer renders no dialog role — its root is
    // role="presentation" — so the drawer is reached from its own title, which is
    // unique across app/src, and the paper is that title's nearest ancestor.
    this.drawerTitle = page.getByText("Execution details", { exact: true }).first();
    this.drawer = this.drawerTitle.locator('xpath=ancestor::div[contains(@class,"MuiDrawer-paper")][1]');
    this.drawerCloseBtn = this.drawer
      .getByTestId("custom-drawer-close")
      .or(this.drawer.getByRole("button", { name: "Close" }))
      .first();
    this.drawerFullViewBtn = this.drawer
      .getByTestId("execution-dashboard-open-full-view-btn")
      .or(this.drawer.getByRole("button", { name: "Open full execution view" }))
      .first();
  }

  // ds/FilterDropdown renders its trigger as `auto-complete-<kebab of the id
  // prop>`, not the id the call site passes — see FilterDropdown.jsx's inputId.
  // The trigger's text is "<label><selected values>", so an exact role+name match
  // is not stable; the id is, and the fallback narrows by label inside the
  // dashboard so it can never resolve to another page's filter of the same name.
  private filterTrigger(id: string, label: string): Locator {
    return this.page
      .locator(`#auto-complete-${id}`)
      .or(this.dashboardRoot.getByRole("button", { name: new RegExp(esc(label)) }))
      .first();
  }

  // The dropdown panel is portaled to body level, so options are page-scoped.
  filterOption(label: string): Locator {
    return this.page
      .getByRole("option", { name: label, exact: true })
      .or(this.page.locator('[role="option"]').filter({ hasText: new RegExp(`^${esc(label)}$`) }))
      .first();
  }

  // Trimmed, never matched with an anchored regex: CustomTable renders each header
  // as `capitalize(name){' '}` plus an empty secondary-text span, so the cell's text
  // is "Status " and a /^Status$/ hasText filter matches nothing.
  async headerNames(): Promise<string[]> {
    const headers = await this.table.locator("thead th").allInnerTexts();
    return headers.map((text) => text.trim()).filter((text) => text !== "");
  }

  // CustomTable renders no per-column attribute on its cells, so a column can only
  // be addressed by position. The position is resolved from the rendered header row
  // rather than hardcoded, so a column inserted upstream moves the read with it
  // instead of silently returning the neighbouring column's text.
  async columnIndex(headerName: string): Promise<number> {
    const headers = await this.table.locator("thead th").allInnerTexts();
    const index = headers.findIndex((text) => text.trim() === headerName);
    if (index < 0) {
      throw new Error(`Column "${headerName}" is not in the executions table header: [${headers.map((h) => h.trim()).join(", ")}]`);
    }
    return index + 1;
  }

  columnCellsAt(index: number): Locator {
    return this.tableBody.locator(`tr td:nth-child(${index})`);
  }
}
