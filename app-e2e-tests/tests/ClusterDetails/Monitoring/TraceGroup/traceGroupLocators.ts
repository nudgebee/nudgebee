// Not for OSS
import { Page, Locator } from "@playwright/test";
import { MonitoringTabLocator } from "../MonitoringTabLocator";

// Infra > K8s > cluster detail > Monitoring > Trace Group.
//
// Rung 1 is unavailable on this whole screen: KubernetesTracesGroupListing.tsx,
// KubernetesTracesListing.tsx, [KubernetesDetails].jsx, ds/FilterDropdown.jsx and
// ds/EmptyState.tsx render ZERO data-testid between them, and CustomTable's only two
// are on the column-selector and the resize handles. So every locator below is rung 2
// (role + accessible name) where a name exists, and rung 3 (id) where it does not.
export class TraceGroupLocators extends MonitoringTabLocator {
  // ListingLayout puts this id on its root, which scopes every toolbar and summary
  // lookup away from the Nubi panel and the sidenav.
  readonly TraceGroupBox: Locator;
  readonly TraceGroupTable: Locator;
  readonly UnsupportedState: Locator;
  readonly NoDataHeading: Locator;
  readonly ResultSummary: Locator;

  readonly SpanTypeFilter: Locator;
  readonly DestinationNamespaceFilter: Locator;
  readonly DestinationWorkloadFilter: Locator;
  readonly ResourceSearch: Locator;
  readonly ClearResourceSearch: Locator;

  constructor(page: Page) {
    super(page);

    // Deliberately id-only, no .or(): this is the scoping root for every locator below,
    // so a wider fallback that resolved to a different container would silently re-scope
    // the whole class and assert against the wrong listing rather than fail.
    this.TraceGroupBox = page.locator("#k8s-traces-group-box");
    // The nested drilldown renders its own table, so this stays scoped and takes the first.
    this.TraceGroupTable = this.TraceGroupBox.locator("#k8s-trace-group-listing")
      .or(this.TraceGroupBox.getByRole("table"))
      .first();

    // Rendered INSTEAD of the whole listing when the account's trace provider reports
    // supports_trace_grouping false — so its presence means the module is off for this
    // provider, not that the page is still loading.
    this.UnsupportedState = page
      .locator("#trace-grouping-unsupported")
      .or(page.getByText("Trace Grouping not supported", { exact: true }))
      .first();

    // CustomTable's empty branch renders EmptyData as a SIBLING of the table, not inside
    // it, so this is scoped to the listing box rather than to the table.
    this.NoDataHeading = this.TraceGroupBox.getByRole("heading", { name: "No Data Available" })
      .or(this.TraceGroupBox.locator("#k8s-trace-group-listing-no-data"))
      .first();

    // CustomTablePagination's row-range caption: "Showing 1-10 of 34 results", or the
    // literal "No results found" at totalRows 0. Anchoring on "results"/the exact empty
    // string is what excludes its ancestors — their text continues into the "Rows"
    // page-size control, so only the caption itself ends here.
    this.ResultSummary = this.TraceGroupBox.getByText(/^No results found$|of\s+[\d,]+\s+results$/).first();

    // ds/FilterDropdown kebab-cases `id || label` onto its trigger button as
    // `auto-complete-<slug>`; these four pass no id, so the label is what names them and
    // each one is distinct. The trigger's accessible name is "<label> <selected value>",
    // which is the scoped rung-2 fallback.
    // Scoped to the listing box, not the page: ListingLayout renders the Toolbar that
    // holds these inside the Card carrying #k8s-traces-group-box, and the id is derived
    // from the label, so another tab mounting its own "Destination Namespace" filter
    // would otherwise be reachable from here.
    this.SpanTypeFilter = this.TraceGroupBox.locator("#auto-complete-span-type")
      .or(this.TraceGroupBox.getByRole("button", { name: /^Span Type\b/ }))
      .first();
    this.DestinationNamespaceFilter = this.TraceGroupBox.locator("#auto-complete-destination-namespace")
      .or(this.TraceGroupBox.getByRole("button", { name: /^Destination Namespace\b/ }))
      .first();
    this.DestinationWorkloadFilter = this.TraceGroupBox.locator("#auto-complete-destination-workload")
      .or(this.TraceGroupBox.getByRole("button", { name: /^Destination Workload\b/ }))
      .first();

    // ds/SearchInput passes `label` through as the placeholder and this callsite gives it
    // no id, so the placeholder is the only handle — scoped to the listing box.
    this.ResourceSearch = this.TraceGroupBox.getByPlaceholder("Search By Resource").first();
    // The trailing X exists only while the field holds a value; its MUI CloseIcon carries
    // aria-label="clear search", which is a real accessible name.
    this.ClearResourceSearch = this.TraceGroupBox.getByLabel("clear search").first();
  }

  // A sortable column header. CustomTable wraps the label of any `sortEnabled` column in
  // a <span role="button">, so the header is reachable by role+name; the plain text
  // fallback is scoped to the same table for the non-sortable ones.
  columnHeader(name: string): Locator {
    return this.TraceGroupTable.getByRole("button", { name: new RegExp(`^${name}\\b`, "i") })
      .or(this.TraceGroupTable.getByText(name, { exact: true }))
      .first();
  }

  // One row of an open FilterDropdown panel. Matched on the row's TEXT, not its
  // accessible name: OptionLabel wraps every label in a CustomTooltip, and MUI writes a
  // tooltip title onto its child as an aria-label, so the computed name is not reliably
  // the label. The panel is a portalled Popover and only one is ever open, so this is
  // addressed at page level.
  filterOption(name: string): Locator {
    return this.page.locator('[role="option"]').filter({ hasText: name }).first();
  }
}
