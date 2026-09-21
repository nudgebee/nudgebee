// Not for OSS
import { Page, Locator } from "@playwright/test";
import { ApplicationGroupLocators } from "../ApplicationGroup/applicationGroupLocators";

// Handles come straight from the components that render the group detail view:
//   app/src/pages/grouping/index.jsx
//     TabsForDrilldown — Summary / Events / Applications / Monitoring, Monitoring disabled
//   app/src/components/k8s/landing/k8sGrouping/KubernetesApplicationGroupingSummary.jsx
//     Heading 'Application Summary' / 'Events/Errors', subtitle2 metric labels
//   app/src/components/k8s/details/KubernetesWorkloads.jsx
//     ListingLayout id='all-workloads', KubernetesTable id='kubernetesWorkloadTable'
//   app/src/components/events/KubernetesEvents.jsx
//     ListingLayout id='all-events'
//
// Not one of those four renders a data-testid on anything this suite reaches
// (`grep -c data-testid` returns 0, 0, 1 and 4 — the workloads hit is an
// optimisation info icon, the events hits are row chips and an investigate
// button, none of them a container). Rung 1 of the ladder therefore does not
// exist here, and role/accessible-name is the highest rung available.
//
// This class extends ApplicationGroupLocators rather than restating it: the tabs,
// the edit action, the Update/Create modal and the listing table are already
// declared there and are deliberately not re-declared. That file is imported, never
// edited — CI runs `playwright test --only-changed`, which walks the import graph,
// so changing it would pull the whole ApplicationGroup spec into this PR's run.
export const WORKLOADS_TABLE = "kubernetesWorkloadTable";

// CustomTable puts the caller's id on the <table> and `${id}-body` on the <tbody>;
// with no rows it renders EmptyData in their place, which ids its heading
// `${id}-no-data`. The workloads table does not pass showUpdatedEmptyData, so it
// takes that id'd branch rather than the fixed "All good here!" panel.
const DATA_ROW = "tr:has(td:nth-child(2))";

export class GroupingLocators extends ApplicationGroupLocators {
  readonly monitoringTab: Locator;
  readonly applicationSummaryHeading: Locator;
  readonly eventsErrorsHeading: Locator;
  readonly applicationsMetric: Locator;
  readonly eventsMetric: Locator;
  readonly optimizationsMetric: Locator;
  readonly summaryEmptyState: Locator;
  readonly workloadsListing: Locator;
  readonly workloadsTable: Locator;
  readonly workloadsRows: Locator;
  readonly workloadsEmptyState: Locator;
  readonly workloadsSearch: Locator;
  readonly eventsListing: Locator;
  readonly dialogUpdateBtn: Locator;

  constructor(page: Page) {
    super(page);

    // The parent's tabByName is private, so the fourth tab is built here the same
    // way: scoped to the tablist because role="tab" is page-wide and a11yProps
    // numbers each strip from its own tabOptions, so several pages render a #tab-3.
    const tablist = page.getByRole("tablist");
    this.monitoringTab = tablist.getByRole("tab", { name: "Monitoring" }).or(tablist.locator("#tab-3")).first();

    // common/Heading.tsx renders its value as a bare <Typography className='border_text'>
    // with no variant, so it lands as a <p> and carries no heading role — getByRole
    // would match zero. Exact text is the highest rung that exists; the fallback
    // narrows to that class rather than widening.
    this.applicationSummaryHeading = page
      .getByText("Application Summary", { exact: true })
      .or(page.locator("p.border_text").filter({ hasText: /^Application Summary$/ }))
      .first();
    this.eventsErrorsHeading = page
      .getByText("Events/Errors", { exact: true })
      .or(page.locator("p.border_text").filter({ hasText: /^Events\/Errors$/ }))
      .first();

    // MUI maps Typography variant='subtitle2' onto <h6>, so these three metric
    // labels do carry a heading role — which is what keeps them apart from the
    // "Applications" and "Events" *tabs*, whose role is "tab". Exact names, so
    // "Events" cannot also satisfy "Events/Errors".
    this.applicationsMetric = this.metricLabel("Applications");
    this.eventsMetric = this.metricLabel("Events");
    this.optimizationsMetric = this.metricLabel("Optimizations");

    // Rendered in place of the whole summary when the group maps no application.
    this.summaryEmptyState = page.getByText("No data available. Please configure application.", { exact: true });

    // ListingLayout renders its id on a plain Box with no role of its own, so there
    // is no wider handle to degrade to — an unscoped fallback would match the body.
    this.workloadsListing = page.locator("#all-workloads");
    this.eventsListing = page.locator("#all-events");

    this.workloadsTable = page.locator(`#${WORKLOADS_TABLE}`);
    this.workloadsRows = page.locator(`#${WORKLOADS_TABLE}-body ${DATA_ROW}`);
    this.workloadsEmptyState = page
      .locator(`#${WORKLOADS_TABLE}-no-data`)
      .or(this.workloadsListing.getByRole("heading", { name: "No Data Available" }))
      .first();

    // This SearchInput callsite passes no `id`, and SearchInput forwards `label` as
    // the placeholder. Scoped to the listing rather than given a getByRole("textbox")
    // fallback, which would resolve to the global account autocomplete in the header.
    this.workloadsSearch = this.workloadsListing.getByPlaceholder("Application Name");

    // The modal supplies its own actionButtons, so ds/Modal's built-in #cancel /
    // #submit are never rendered and these DsButtons carry no id. Exact name so
    // "Update" cannot also match the "Update Grouping" title node.
    this.dialogUpdateBtn = this.dialog.getByRole("button", { name: "Update", exact: true }).first();
  }

  // Fallback stays on <h6> rather than widening to any text: "Applications" and
  // "Events" both appear as tab labels on this very page.
  private metricLabel(name: string): Locator {
    return this.page
      .getByRole("heading", { name, exact: true })
      .or(this.page.locator("h6").filter({ hasText: new RegExp(`^${name}$`) }))
      .first();
  }

  // The clickable count under a metric label. Anchored to the label rather than
  // indexed from the page: Applications wraps its value in a flex Box while Events
  // renders the <h5> as a direct sibling, so descendant-or-self covers both.
  metricValue(label: Locator): Locator {
    return label.locator("xpath=following-sibling::*[1]/descendant-or-self::h5[1]").first();
  }

  // A table either has rows or shows its empty panel in their place, never both, and
  // which one depends on what the shared tenant holds. While `loading` is true
  // CustomTable swaps in a skeleton <tbody> carrying no id, so the id'd body being
  // attached means the response has landed.
  async waitForWorkloadsSettled(timeout = 60000): Promise<void> {
    await this.page.locator(`#${WORKLOADS_TABLE}-body`).or(this.workloadsEmptyState).first().waitFor({ state: "attached", timeout });
  }

  // SearchInput commits on Enter (onEnterPress) — typing alone filters nothing.
  async searchWorkloads(term: string): Promise<void> {
    await this.workloadsSearch.click();
    await this.workloadsSearch.fill(term);
    await this.workloadsSearch.press("Enter");
  }
}
