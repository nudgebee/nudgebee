// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Table ids come straight from the components:
//   app/src/components/k8s/details/KubernetesPods.jsx  kubernetesPodsTable = 'kubernetesPodsTable'
//   app/src/components/events/KubernetesEvents.jsx     kubernetesEventsTable = 'kubernetesEventsTable'
// CustomTable puts the id on the <table> and `${id}-body` on the <tbody>; with no rows it
// renders <h2 id={`${id}-no-data`}> in their place (app/src/components/common/EmptyData.jsx).
export const PODS_TABLE = "kubernetesPodsTable";
export const EVENTS_TABLE = "kubernetesEventsTable";

// Both tables are expandable, so CustomTable emits a second <tr> per data row to hold the
// collapsed drill-down. It carries one colSpan cell, so requiring a second cell is what
// keeps every row count from silently doubling.
const DATA_ROW = "tr:has(td:nth-child(2))";

// Every tab label in app/src/components/k8s/pods/PodsDetails.jsx `optionsToDisplay`.
export const POD_TABS = [
  "Pod Details",
  "Utilization Trends",
  "Cost Trends",
  "Recent Events",
  "Yaml",
  "Logs",
  "Profiler",
  "Service Map",
  "App Dashboard",
  "Security",
] as const;

// Nothing under app/src/components/k8s/pods/ renders a test contract: a data-testid grep
// over that directory returns 0 for all five files, and they render no `id` either.
// So the pod header and the summary card are reached by role and by container-scoped exact
// text (ladder rungs 2 and 4). The id primaries below are not those components — they are
// the ListingLayout roots each tab mounts, which do carry an id and no testid.
export class PodDetailsLocators extends CommonLocators {
  // Pods listing on the cluster's Apps & Infra tab — the only route into this module.
  readonly podsTableBody: Locator;
  readonly podRows: Locator;
  readonly podsNoData: Locator;

  // PodTitleBox.jsx — Typography variant='h5', so it is a real <h5> heading.
  readonly podNameHeading: Locator;
  readonly podDebuggerBtn: Locator;

  // PodsDetails.jsx tab strip. Filtered by a label only this strip carries: CustomTable's
  // expandable rows render their own tablist, and an unscoped getByRole('tab') would reach
  // into those as soon as a row is expanded.
  readonly tabStrip: Locator;

  // Panels the tabs mount, each a ListingLayout root (`id` on the Card).
  readonly utilizationPanel: Locator;
  readonly costPanel: Locator;
  readonly eventsPanel: Locator;
  readonly eventsRows: Locator;
  readonly eventsNoData: Locator;
  readonly logsPanel: Locator;
  readonly containerFilter: Locator;
  readonly previousLogsCheckbox: Locator;

  constructor(page: Page) {
    super(page);

    this.podsTableBody = page.locator(`#${PODS_TABLE}-body`);
    this.podRows = page.locator(`#${PODS_TABLE}-body ${DATA_ROW}`);
    this.podsNoData = page.locator(`#${PODS_TABLE}-no-data`);

    this.podNameHeading = page
      .getByRole("heading", { level: 5 })
      .filter({ hasText: "Pod name:" })
      .or(page.getByText("Pod name:", { exact: true }).locator("xpath=.."))
      .first();
    this.podDebuggerBtn = page.getByRole("button", { name: "Open Pod Debugger" }).first();

    this.tabStrip = page.getByRole("tablist").filter({ hasText: "Service Map" }).first();

    // Deliberately id-only, no .or(): ListingLayout's root props accept `id` and nothing
    // else (app/src/components/common/ds/ListingLayout.tsx — only its Toolbar takes a
    // data-testid), so no caller can give these panels one, and the root is a Card with no
    // role and no accessible name. Rung 3 is the top of the ladder here, and every wider
    // match is wrong: the chart panels carry only headings that repeat across the page.
    this.utilizationPanel = page.locator("#box-utilization-charts");
    this.costPanel = page.locator("#box-cost-charts");
    this.eventsPanel = page.locator("#all-events");
    this.eventsRows = page.locator(`#${EVENTS_TABLE}-body ${DATA_ROW}`);
    this.eventsNoData = page.locator(`#${EVENTS_TABLE}-no-data`);
    // `div#pod-logs`, not `#pod-logs`: KubernetesPodLogs.tsx puts id='pod-logs' on BOTH the
    // ListingLayout root and the DownloadButton nested inside it, so the bare id is a strict
    // mode violation ("resolved to 2 elements"). The tag narrows it to the panel; the
    // duplicate id itself is an app bug, reported under Follow-ups rather than fixed here.
    this.logsPanel = page.locator("div#pod-logs");
    // FilterDropdown renders `auto-complete-${toKebabCase(label)}` on its trigger button
    // (app/src/components/common/ds/FilterDropdown.jsx) and no testid; the label is its
    // accessible name, so the fallback stays inside the same logs panel.
    this.containerFilter = page
      .locator("#auto-complete-container")
      .or(page.locator("div#pod-logs").getByRole("button", { name: /Container/ }))
      .first();
    this.previousLogsCheckbox = page.getByRole("checkbox", { name: "Get Previous Logs" }).first();
  }

  // The pod-name cell. KubernetesPods.jsx renders it as a ClusterNameWithRegion whose
  // Typography carries the click handler and neither a testid, an id, nor an accessible
  // name — no rung above 5 exists, so this is a deliberate scoped-CSS primary with no
  // .or(): any wider match would land on the namespace line in the same cell. "Pod Name"
  // being column one is the table's own contract (POD_HEADERS).
  podNameCell(row: Locator): Locator {
    return row.locator("td").first().locator("p").first();
  }

  // The summary card on the Pod Details tab labels every field with a trailing colon
  // (PodDetailsBox.jsx). The colon is load-bearing: the strip above the tab strip renders
  // "Namespace" and "Controlled by" without one, so exact text is what tells them apart.
  summaryField(label: string): Locator {
    return this.page.getByText(`${label}:`, { exact: true }).first();
  }

  // The row holding one summary field. PodDetailsBox puts the label Typography and its
  // value Typography in the same flex Box, so the label's parent is the pair — which is
  // how a value gets asserted without an index chain into the card.
  summaryRow(label: string): Locator {
    return this.summaryField(label).locator("xpath=..");
  }

  // PodTitleBox stacks the <h5> and the ID / Last seen line in one column Box, so the
  // heading's parent is the block that carries both.
  podTitleBox(): Locator {
    return this.podNameHeading.locator("xpath=..");
  }

  tab(name: string): Locator {
    return this.tabStrip.getByRole("tab", { name, exact: true });
  }

  // MUI marks the open tab with aria-selected. That is the app's own notion of which tab
  // is rendered, and unlike the URL it cannot be one navigation behind.
  selectedTab(): Locator {
    return this.tabStrip.locator('[role="tab"][aria-selected="true"]');
  }

  // A table either has rows or renders its empty panel in their place — which one depends
  // on the cluster's data, not on the code under test. While `loading` is true CustomTable
  // swaps in a skeleton <tbody> carrying no id, so the id'd body being attached means the
  // response has landed.
  async waitForTable(tableId: string, timeout = 90000): Promise<void> {
    await this.page
      .locator(`#${tableId}-body`)
      .or(this.page.locator(`#${tableId}-no-data`))
      .first()
      .waitFor({ state: "attached", timeout });
  }
}
