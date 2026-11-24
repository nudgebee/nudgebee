// Not for OSS
import { Page, Locator } from "@playwright/test";
import { AWSLocators } from "../AWSLocators";
import { escapeForRegex } from "../EC2/ec2Constants";
import {
  COST_CANVAS,
  COST_TREND_ROOT,
  DRILLDOWN_TABS,
  FILTER,
  FILTER_LABEL,
  LISTING_ROOT,
  RECOMMENDATIONS_TABLE,
  RESOURCE_FILTER,
  RESOURCE_FILTER_LABEL,
  RESOURCES_ROOT,
  RESOURCES_TABLE,
  SERVICES_TABLE,
} from "./servicesConstants";

// CloudAccountServices.tsx renders zero data-testid (`grep -c data-testid` returns 0),
// so rung 1 does not exist on the listing itself and the ids the components are
// explicitly given are the primary, each with a role-based fallback scoped to the same
// container wherever the accessible name is unambiguous. The two exceptions are the
// date picker's shortcut buttons and the recommendations toolbar, which do carry
// testids and use them as the primary.
//
// Every expandable row emits a second <tr> holding the collapsed drill-down, present in
// the DOM whether or not the row is open and carrying a single colSpan cell. Requiring
// a second cell is what separates the data rows from it — counting `tr` alone would
// silently double every row count in this file.
const DATA_ROW = "tr:has(td:nth-child(2))";

// The date trigger's text is either a shortcut label or "Aug 14 - Sep 13"
// (CustomDateTimeRangePicker.jsx:247-259).
const DATE_RANGE_TEXT = /^(Last|Current) |[A-Z][a-z]{2} \d{1,2} - [A-Z][a-z]{2} \d{1,2}/;

// Extends AWSLocators rather than CommonLocators directly: openAWSCloudAccountFromConfig
// (the integration gate plus the account picker) and AnchorTabServices already live
// there and are what every test here starts from.
export class ServicesLocators extends AWSLocators {
  readonly listingRoot: Locator;
  readonly servicesTable: Locator;
  readonly servicesBody: Locator;
  readonly servicesRows: Locator;
  readonly servicesEmpty: Locator;
  readonly dateRangeTrigger: Locator;
  readonly downloadButton: Locator;
  readonly paginationSummary: Locator;
  readonly nextPageButton: Locator;

  readonly resourcesRoot: Locator;
  readonly resourcesTable: Locator;
  readonly resourcesBody: Locator;
  readonly resourcesRows: Locator;
  readonly resourcesEmpty: Locator;
  readonly resourceDetailsPanel: Locator;
  readonly resourceDetailsContent: Locator;

  readonly costTrendRoot: Locator;
  readonly costTrendTitle: Locator;
  readonly frequencyFilter: Locator;
  readonly costCanvas: Locator;

  readonly recommendationsToolbar: Locator;
  readonly recommendationsRoot: Locator;
  readonly recommendationsTable: Locator;
  readonly recommendationsBody: Locator;
  readonly recommendationsRows: Locator;
  readonly recommendationsEmpty: Locator;
  readonly categoryFilter: Locator;

  // The one open MUI Popover — a filter panel or the date picker. Built once here so
  // the two methods that scope into it share a single definition.
  private readonly popover: Locator;

  constructor(page: Page) {
    super(page);

    this.popover = page.locator(".MuiPopover-root").first();

    // Deliberately id-only: the Card is a plain styled div with no role and no
    // accessible name, and it is the container the toolbar assertions scope to — a
    // wider fallback here would silently widen all of them.
    this.listingRoot = page.locator(`#${LISTING_ROOT}`).first();

    // CustomTable's <table> carries only aria-label='table' (CustomTable.jsx:1120).
    this.servicesTable = page.locator(`#${SERVICES_TABLE}`).or(this.listingRoot.getByRole("table")).first();
    // Body and rows are deliberately id-only: while `loading` is true CustomTable swaps
    // in an id-less skeleton <tbody> (CustomTable.jsx:899, 1345), so the id'd body
    // being attached is the settle signal waitForServices relies on. A `tbody` fallback
    // would match the skeleton and report the table ready before its rows exist.
    this.servicesBody = page.locator(`#${SERVICES_TABLE}-body`);
    this.servicesRows = page.locator(`#${SERVICES_TABLE}-body ${DATA_ROW}`);

    // EmptyData renders <h2 id={`${id}-no-data`}>. The heading fallback is scoped to
    // the card because "No Data Available" is CustomTable's default copy everywhere.
    this.servicesEmpty = page
      .locator(`#${SERVICES_TABLE}-no-data`)
      .or(this.listingRoot.getByRole("heading", { name: "No Data Available" }))
      .first();

    // The picker's trigger is a plain <button> with no id, name or testid
    // (CustomDateTimeRangePicker.jsx:276-283); its text is the one handle it has, and
    // hasText tolerates the icon alt text that also lands in the button's name.
    this.dateRangeTrigger = this.listingRoot.getByRole("button").filter({ hasText: DATE_RANGE_TEXT }).first();

    // DownloadButton takes its id verbatim and sets aria-label='Download'
    // (DownloadButton.jsx:124-128).
    this.downloadButton = page
      .locator(`#${SERVICES_TABLE}-download`)
      .or(this.listingRoot.getByRole("button", { name: "Download" }))
      .first();

    // CustomTablePagination's row-range copy (CustomTablePagination.jsx:66-76). Scoped
    // to the card because the drill-down tables render the same footer. No fallback:
    // the string is the assertion in the pagination case.
    this.paginationSummary = this.listingRoot.getByText(/^Showing .* results$|^No results found$/).first();
    // MUI Pagination labels its arrows "Go to next page" / "Go to previous page".
    this.nextPageButton = this.listingRoot.getByRole("button", { name: "Go to next page" }).first();

    // The Resources drill-down (CloudAccountResources, CloudAccountServices.tsx:996).
    // Root, body and rows are id-only for the same reasons as their listing twins
    // above: the card is a role-less div that everything else scopes to, and the id'd
    // body is what separates loaded rows from the skeleton.
    this.resourcesRoot = page.locator(`#${RESOURCES_ROOT}`).first();
    this.resourcesTable = page.locator(`#${RESOURCES_TABLE}`).or(this.resourcesRoot.getByRole("table")).first();
    this.resourcesBody = page.locator(`#${RESOURCES_TABLE}-body`);
    this.resourcesRows = page.locator(`#${RESOURCES_TABLE}-body ${DATA_ROW}`);
    this.resourcesEmpty = page
      .locator(`#${RESOURCES_TABLE}-no-data`)
      .or(this.resourcesRoot.getByRole("heading", { name: "No Data Available" }))
      .first();

    // CustomTable renders every drill-down panel as `table-tabpanel-${index}`
    // (CustomTable.jsx:95), so the nested Details panel (index 0) shares its id with
    // the outer Resources panel (also index 0). Scoping inside the resources card is
    // what separates the two — the outer panel contains the card, not the reverse.
    this.resourceDetailsPanel = this.resourcesRoot.locator('[id^="table-tabpanel-"]:visible').first();
    // ResourceDetails renders labelled blocks for whichever fields the resource has
    // (CloudAccountServices.tsx:569-621) or a one-line fallback — any of these is proof
    // the panel fetched and rendered rather than staying on its Loader.
    this.resourceDetailsContent = this.resourceDetailsPanel
      .getByText(/^(ARN|Status|Created On|First Seen|Last Seen|Open Recommendations)$|No detailed information available/)
      .first();

    // The Cost Trend drill-down (TotalCostChart). Its ListingLayout id is the
    // component default because the drill-down passes none (CloudAccountServices.tsx:756).
    // Id-only: a role-less card that the title, filter and canvas below scope to.
    this.costTrendRoot = page.locator(`#${COST_TREND_ROOT}`).first();
    // The toolbar title is the same string as the drill-down tab, so it stays scoped
    // to the card and exact. No fallback: the text is the assertion.
    this.costTrendTitle = this.costTrendRoot.getByText("Cost Trend", { exact: true }).first();
    // FilterDropdown lowercases the id it is given, so the camel-cased canvas id
    // becomes all-lowercase in the trigger id (utils/common.ts:884-890).
    this.frequencyFilter = page
      .locator(`#auto-complete-${COST_CANVAS.toLowerCase()}-frequency`)
      .or(this.costTrendRoot.getByRole("button", { name: /^Frequency/ }))
      .first();
    // Chart.Bar / Chart.Line take the canvas id (CostChart.tsx:125-141).
    this.costCanvas = page.locator(`#${COST_CANVAS}`).or(this.costTrendRoot.locator("canvas")).first();

    // The Recommendations drill-down. Its toolbar is the one node on the whole surface
    // that carries a data-testid (ServiceRecommendations.tsx:281), so it is the primary.
    this.recommendationsToolbar = page.getByTestId(`${RECOMMENDATIONS_TABLE}-filter-toolbar`).first();
    // Both the card and the table carry this id; document order puts the card first.
    // Id-only for the same reasons as the listing's own root, body and rows.
    this.recommendationsRoot = page.locator(`#${RECOMMENDATIONS_TABLE}`).first();
    this.recommendationsTable = page.locator(`table#${RECOMMENDATIONS_TABLE}`).or(this.recommendationsRoot.getByRole("table")).first();
    this.recommendationsBody = page.locator(`#${RECOMMENDATIONS_TABLE}-body`);
    this.recommendationsRows = page.locator(`#${RECOMMENDATIONS_TABLE}-body ${DATA_ROW}`);
    this.recommendationsEmpty = page
      .locator(`#${RECOMMENDATIONS_TABLE}-no-data`)
      .or(this.recommendationsRoot.getByRole("heading", { name: "No Data Available" }))
      .first();
    this.categoryFilter = page
      .locator(`#auto-complete-${RECOMMENDATIONS_TABLE}-filter-category`)
      .or(this.recommendationsToolbar.getByRole("button", { name: /^Category/ }))
      .first();
  }

  // ds/FilterDropdown renders its trigger as <button id={`auto-complete-${id}`}> whose
  // leading text is its `label`.
  filterTrigger(key: keyof typeof FILTER): Locator {
    return this.page
      .locator(`#auto-complete-${FILTER[key]}`)
      .or(this.listingRoot.getByRole("button", { name: new RegExp(`^${escapeForRegex(FILTER_LABEL[key])}`) }))
      .first();
  }

  resourceFilterTrigger(key: keyof typeof RESOURCE_FILTER): Locator {
    return this.page
      .locator(`#auto-complete-${RESOURCE_FILTER[key]}`)
      .or(this.resourcesRoot.getByRole("button", { name: new RegExp(`^${escapeForRegex(RESOURCE_FILTER_LABEL[key])}`) }))
      .first();
  }

  // The clear (x) is an inline <svg onClick> drawn with two <line>s inside the trigger
  // (FilterDropdown.jsx:1216-1229) — no role, no name, no id — and it replaces the
  // chevron only while a value is selected. The chevron svg has no <line>, which is the
  // one attribute that tells them apart, so this is rung 5 by necessity. Scoped to its
  // own trigger; no fallback exists that would not also match the chevron.
  filterClear(trigger: Locator): Locator {
    return trigger.locator("svg:has(line)").first();
  }

  // Options are role='option' boxes inside the open MUI Popover (FilterDropdown.jsx:199).
  // Scoped by :visible: only one panel is ever open and every other dropdown's options
  // are unmounted, matching the pattern the EC2 and admin specs already use.
  filterOption(label: string): Locator {
    return this.visibleFilterOptions()
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  visibleFilterOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  // FilterDropdown only renders its search box above eight options
  // (FilterDropdown.jsx:1220), so callers must treat its absence as an expected
  // outcome. Scoped to the popover root — the panel has no id or role of its own.
  filterSearchInput(): Locator {
    return this.popover.getByPlaceholder("Search...").first();
  }

  // Prefix-matched rather than exact: "% Change" carries an info icon whose tooltip
  // trigger contributes to the header's accessible name.
  columnHeader(name: string): Locator {
    return this.servicesTable.getByRole("columnheader", { name: new RegExp(`^${escapeForRegex(name)}`) }).first();
  }

  resourceColumnHeader(name: string): Locator {
    return this.resourcesTable.getByRole("columnheader", { name: new RegExp(`^${escapeForRegex(name)}`) }).first();
  }

  recommendationColumnHeader(name: string): Locator {
    return this.recommendationsTable.getByRole("columnheader", { name: new RegExp(`^${escapeForRegex(name)}`) }).first();
  }

  // A sortable header wraps its label in <span role="button"> (CustomTable.jsx:1240-1243).
  // Scoped to the header cell so the same label in a drill-down table cannot match.
  // Fallback: CustomTable2 (still used by CloudAccountTable on OSS) renders the handle as a plain <span> holding the sort icon.
  sortHandle(name: string): Locator {
    const header = this.columnHeader(name);
    return header.getByRole("button").or(header.locator("span:has(svg)")).first();
  }

  // One column's cells across the data rows only, in row order. nth-child is 1-based.
  columnCells(column: number): Locator {
    return this.servicesRows.locator(`td:nth-child(${column + 1})`);
  }

  // CustomTable.jsx:475 — one IconButton per data row whose accessible name and
  // aria-expanded both flip with the row's state.
  expandToggle(rowIndex: number): Locator {
    return this.servicesRows.nth(rowIndex).getByRole("button", { name: /^(Expand|Collapse) row$/ }).first();
  }

  resourceExpandToggle(rowIndex: number): Locator {
    return this.resourcesRows.nth(rowIndex).getByRole("button", { name: /^(Expand|Collapse) row$/ }).first();
  }

  // The drill-down tab strip is a real MUI <Tab> whose label is plain text, so the
  // accessible name is reliable here. CustomTable passes no per-tab id, leaving
  // a11yProps to fall back to `tab-${value}` — too generic to key off.
  drilldownTab(tabName: string): Locator {
    return this.page.getByRole("tab", { name: tabName, exact: true }).first();
  }

  // The panel TabPanel renders for the open service row (CustomTable.jsx:95). Index is
  // the tab's position in DRILLDOWN_TABS.
  drilldownPanel(tabName: string): Locator {
    return this.page.locator(`#table-tabpanel-${DRILLDOWN_TABS.indexOf(tabName)}:visible`).first();
  }

  // ThreeDotsMenu renders a ds/Button with aria-label='More actions' and the SAME
  // id='three-dot-menu' in every row (ThreeDotsMenu.jsx:108), so the row is the scope.
  rowActionsTrigger(rowIndex: number): Locator {
    return this.servicesRows.nth(rowIndex).getByRole("button", { name: "More actions" }).first();
  }

  // ds/DropdownMenu renders each item as <div role="menuitem" id={item.id}>
  // (Overlay.tsx:283-290). getByRole("menuitem", { name }) is deliberately not used —
  // this app nests the label in aria-hidden spans, so it resolves nothing.
  rowActionItem(id: string, label: string): Locator {
    return this.page
      .locator(`[role="menuitem"]#${id}:visible`)
      .or(this.visibleMenuItems().filter({ hasText: new RegExp(`^${escapeForRegex(label)}`) }))
      .first();
  }

  visibleMenuItems(): Locator {
    return this.page.locator('[role="menuitem"]:visible');
  }

  // Each shortcut is a ds/Button carrying `date-range-shortcut-<slug>`
  // (CustomDateTimeRangePicker.jsx:445) — the one testid the listing surface exposes.
  dateShortcut(label: string): Locator {
    return this.page
      .getByTestId(`date-range-shortcut-${label.replace(/\s+/g, "-").toLowerCase()}`)
      .or(this.popover.getByRole("button", { name: label, exact: true }))
      .first();
  }

  // MUI PaginationItem labels an unselected page "Go to page N" and, once selected,
  // renames it to "page N" with aria-current — so the two states are two locators.
  // Scoped to the card so a drill-down's pager cannot answer for it.
  pageButton(pageNumber: number): Locator {
    return this.listingRoot.getByRole("button", { name: `Go to page ${pageNumber}`, exact: true }).first();
  }

  currentPageButton(pageNumber: number): Locator {
    return this.listingRoot.getByRole("button", { name: `page ${pageNumber}`, exact: true }).first();
  }
}
