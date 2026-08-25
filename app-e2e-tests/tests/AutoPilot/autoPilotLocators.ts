// Not for OSS
import { Page, Locator } from "@playwright/test";
import { AutoOptimizeLocators } from "../AutoOptimize/autoOptimizeLocators";

// AutoPilot task details — app/src/pages/auto-pilot/task/[TaskDetails].jsx.
// Two tabs, each its own body:
//   Tasks   -> AutoOptimizeTasks   (KubernetesTable id 'auto-pilot', expandable rows)
//   Details -> AutoOptimizeSummary (namespace/workload filters, stat cards, charts)
//
// Extends AutoOptimizeLocators rather than CommonLocators because this page has no entry
// point of its own: the only in-product route to it is the task-name link in the Auto
// Optimize listing, so every test walks that listing first and the table/filter machinery
// there (waitForTable, rowsFor, cellsInColumn, pickFilterOption) is the same machinery
// this page needs. Re-declaring it here would be the P5 duplication the gate forbids.
//
// Rung choice, measured rather than assumed. Every file behind this page renders zero
// data-testid attributes:
//   grep -c 'data-testid' app/src/pages/auto-pilot/task/[TaskDetails].jsx -> 0
//   grep -c 'data-testid' on all 7 files under app/src/components/autopilot -> 0
// So rung 1 does not exist here. Where the element carries a real accessible name that
// scopes without ambiguity, that name is the primary or the `.or()` fallback; where it
// does not, the locator is deliberately id-only and says so on the line above it.

// KubernetesTable forwards this id to CustomTable, which puts it on the <table> and
// `${id}-body` on the <tbody>. Same string as the Auto Optimize listing's table id — the
// two never coexist, they are one page apart.
export const TASKS_TABLE = "auto-pilot";

// LISTING_HEADER in [TaskDetails].jsx. Used as 1-based nth-child indices. showExpandable
// appends the chevron cell AFTER the data cells (CustomTable ExpandableTableRowBase), so
// the data columns keep their natural positions.
export const TASKS_HEADERS = ["Scheduled Time", "Status", "Resource", "Message", "PR/Ticket", "Recommendation"];
export const STATUS_COLUMN = 2;

// statusFilter in [TaskDetails].jsx. "All" is the empty-value reset entry.
export const TASK_STATUS_OPTIONS = ["All", "Complete", "Scheduled", "Failed", "Skipped", "Dryrun"];
export const ALL_STATUS = "All";
export const COMPLETE_STATUS = "Complete";

// DRILL_DOWN_LISTING_HEADER in [TaskDetails].jsx — the columns of the expanded row's table.
export const DRILLDOWN_HEADERS = ["Name", "Old Value", "New Value"];

// The Details tab's stat cards whose label appears nowhere else in that tab body. The
// other two cards are labelled "Namespace" and "Workload", which are also the labels of
// that tab's own filter triggers, so those two are asserted through the triggers instead
// of through detailsStat() — a text match would resolve to the toolbar, not the card.
export const DETAILS_STATS = ["Kind", "Category", "Last Executed At"];

// tabOptions in [TaskDetails].jsx.
export const TASKS_TAB_ID = "tab-tasks";
export const DETAILS_TAB_ID = "tab-details";

export class AutoPilotLocators extends AutoOptimizeLocators {
  readonly tasksTab: Locator;
  readonly detailsTab: Locator;

  // Tasks tab.
  readonly tasksListing: Locator;
  readonly pageTitle: Locator;
  readonly statusFilter: Locator;
  readonly tasksTable: Locator;
  readonly tasksRows: Locator;
  readonly tasksEmpty: Locator;

  // Details tab.
  readonly namespaceFilter: Locator;
  readonly workloadFilter: Locator;

  constructor(page: Page) {
    super(page);

    // navigation/Tabs.jsx puts the tabOption's own `id` on the MUI <Tab> via a11yProps,
    // and MUI renders it role="tab". These are the only role="tab" elements on the page,
    // so the role fallback cannot resolve to something else.
    this.tasksTab = page.locator(`#${TASKS_TAB_ID}`).or(page.getByRole("tab", { name: "Tasks" })).first();
    this.detailsTab = page.locator(`#${DETAILS_TAB_ID}`).or(page.getByRole("tab", { name: "Details" })).first();

    // The page renders id="auto-pilot" TWICE — ds/ListingLayout puts it on its Card (a
    // Box, so a <div>) and KubernetesTable passes the same string to CustomTable, which
    // puts it on the <table>. A bare "#auto-pilot" is therefore ambiguous and would
    // resolve in document order. Both locators are element-qualified so each one names
    // exactly the node it means. Raised as a product issue under Follow-ups.
    //
    // Id-only by design on both: the Card has no role, no accessible name and no testid,
    // and a <table> carries no accessible name to fall back to.
    this.tasksListing = page.locator("div#auto-pilot");
    this.tasksTable = page.locator("table#auto-pilot");

    // ds/CustomTable ids its empty heading `${tableId}-no-data` (EmptyData). Id-only by
    // design: the heading's own text is the generic "No Data Available" that every empty
    // table on the app renders, so a role+name fallback would match another table's empty
    // state rather than this one's. The id is the only thing that names this table.
    this.tasksEmpty = page.locator(`#${TASKS_TABLE}-no-data`);
    this.tasksRows = this.rowsFor(TASKS_TABLE);

    // ds/ListingLayout.ToolbarTitle renders the title as a bare <p> — no role, no
    // accessible name, no id — so this is rung 5, scoped to the listing Card. Matched as
    // the <p> itself rather than with getByText, which also matches every ancestor
    // carrying the same text and would settle on a wrapper Box. The title is CSS-
    // truncated at 320px but the DOM holds the full string, so toContainText still reads
    // the whole name.
    this.pageTitle = this.tasksListing.locator("p").filter({ hasText: /^Auto Optimize Task >/ }).first();

    // ds/FilterDropdown derives its trigger id from `toKebabCase(id || label)`, and this
    // page passes only `label`, so the ids below are the labels kebab-cased. The trigger
    // is a <button> whose leading text is the label, so getByRole is a genuine fallback.
    // Scoped to the tab's own container: the Details tab renders its own filters and a
    // page-wide fallback could reach the wrong one after a tab switch.
    this.statusFilter = this.filterTrigger("status", "Status", this.tasksListing);

    // The Details tab's ListingLayout is given no id, so there is no container to scope
    // to. These two ids are unique page-wide, and the role+name fallback is page-scoped
    // for the same reason — accepted here because the Tasks tab renders no Namespace or
    // Workload filter, so only one of the two tab bodies can ever match.
    this.namespaceFilter = page.getByRole("button", { name: /^Namespace/ }).or(page.locator("#auto-complete-namespace")).first();
    this.workloadFilter = page.getByRole("button", { name: /^Workload/ }).or(page.locator("#auto-complete-workload")).first();
  }

  // The task-name link in one Auto Optimize listing row. Rung 5 (attribute CSS scoped to
  // the row): ds/Link renders a plain <a> with no id and no testid, and the row's other
  // anchors point elsewhere, so the route prefix is the only thing that identifies it.
  // The route is a stronger contract than the name text anyway — the name is tenant data.
  taskLinkIn(row: Locator): Locator {
    return row.locator('a[href^="/auto-pilot/task/"]').first();
  }

  // One column header of the executions table, scoped to the table so the page's other
  // headings cannot answer for it. Rung 4 — CustomTable's <th> carries no id and no
  // testid, and its text is the header itself.
  tasksHeaderCell(header: string): Locator {
    return this.tasksTable.getByText(header, { exact: true }).first();
  }

  // One entry of an open FilterDropdown popover. Page-scoped because MUI portals the
  // Popover to <body>, so the options are not inside the trigger; only one popover is
  // open at a time, so this cannot reach a different filter's list. Anchored so a label
  // that is a strict prefix of another cannot match the wrong row.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
      .first();
  }

  // The chevron CustomTable appends to every expandable row. aria-label is the real
  // accessible name (rung 2) and flips with state, so the closed and open forms are
  // different locators — which is what lets a test wait on the toggle having committed.
  // No fallback by design: the button's only other handle is its icon, and a wider match
  // would reach every other row's chevron, which is why both rungs stay scoped to `row`.
  expandToggleIn(row: Locator): Locator {
    return row.getByRole("button", { name: "Expand row" });
  }

  collapseToggleIn(row: Locator): Locator {
    return row.getByRole("button", { name: "Collapse row" });
  }

  // The panel CustomTable renders under an expanded row — the row's immediately following
  // sibling <tr>, whose single colSpan cell holds the drilldown. Taken as a sibling step
  // from the row itself rather than as an index into the tbody, so it stays correct when
  // the rows re-order or the table re-pages.
  drilldownPanelFor(row: Locator): Locator {
    return row.locator("xpath=following-sibling::tr[1]");
  }

  // One column header of an expanded row's drilldown table, scoped to that panel.
  drilldownHeaderCell(panel: Locator, header: string): Locator {
    return panel.getByText(header, { exact: true }).first();
  }

  // One stat card on the Details tab, by the label ds/Stat prints beside its value. Rung
  // 4, page-scoped: Stat renders no id and no testid, and the Details tab's ListingLayout
  // is given no id either, so there is no container to scope to. Safe only for the
  // DETAILS_STATS labels — see the note on that constant.
  detailsStat(label: string): Locator {
    return this.page.getByText(label, { exact: true }).first();
  }
}
