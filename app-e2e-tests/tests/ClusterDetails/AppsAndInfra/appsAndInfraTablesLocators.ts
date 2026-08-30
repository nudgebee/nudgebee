// Not for OSS
import { Page, Locator } from "@playwright/test";
import { AppsAndInfraLocators } from "./AppsAndInfraLocators";

// Handles come straight from the components that render this section:
//   app/src/pages/kubernetes/details/[KubernetesDetails].jsx
//     tabOptions[3] — fragment 'kubernetes', nine sub-tabs each carrying its own id/text
//   app/src/components/k8s/details/Kubernetes{Nodes,Workloads,Pods,Namespace,
//     Services,PVC,PV,Dbms,Queue}.jsx — each names its own CustomTable id
//   app/src/components/common/tables/CustomTable.jsx
//     id={id} on <table>, id={`${id}-body`} on <tbody>, EmptyData id={id} when empty
//   app/src/components/common/EmptyData.jsx — <h2 id={`${id}-no-data`}>
//   app/src/components/common/ds/FilterDropdown.jsx — trigger id `auto-complete-${kebab(label)}`
//   app/src/components/common/ds/SearchInput.jsx — `label` renders as the placeholder
//
// Only one of the nine listings is mounted at a time (the page renders
// `selectedSubTab == N && <Component/>`), so a table id is unambiguous at runtime
// even where two components reuse a ListingLayout id — 'all-namespaces' is shared
// by Namespace, Services, PVC, PV and Queue, which is why no locator here scopes
// to a ListingLayout.
//
// Of the nine components only KubernetesWorkloads renders a data-testid, and it is
// on an unrelated control; CustomTable's two testids mark the column-selector and
// the resize handles. Rung 1 of the ladder therefore does not exist for anything
// below, and the comments say which rung was used instead.

// The data rows only. An expandable table emits a second <tr> per row holding the
// collapse panel in a single colSpan cell, so requiring a second <td> keeps the
// count to real rows.
const DATA_ROW = "tr:has(td:nth-child(2))";

// Defined here rather than imported: the repo's two copies are module-scoped to
// tests/admin/Audits and tests/admin/groups, with no shared export to reuse.
function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// One entry per sub-tab of the Apps & Infra section: the id the tab strip renders,
// the label it displays (opt.text), the CustomTable id the listing mounts, and the
// column headers that listing declares. Taken from the component files named above.
export interface Listing {
  readonly id: string;
  readonly label: string;
  readonly tableId: string;
  readonly headers: readonly string[];
}

export const LISTINGS = {
  nodes: {
    id: "nodes",
    label: "Nodes",
    tableId: "kubernetesNodeTable",
    headers: ["Node Name", "Type", "Pods", "IP", "Cost", "CPU", "Memory", "Status", "Created"],
  },
  applications: {
    id: "applications",
    label: "Applications",
    tableId: "kubernetesWorkloadTable",
    headers: ["Application Name", "Created At", "Cost", "Replicas", "CPU", "Memory", "Error Count (24h)", "SLO (24h)"],
  },
  pods: {
    id: "pods",
    label: "Pods",
    tableId: "kubernetesPodsTable",
    headers: ["Pod Name", "Namespace", "Cost", "CPU", "Memory", "Status/State", "Restarts", "Created At"],
  },
  namespaces: {
    id: "namespaces",
    label: "Namespace",
    tableId: "kubernetesNamespaceTable",
    headers: ["Namespace", "Workloads/Pods", "Cost"],
  },
  services: {
    id: "services",
    label: "Services",
    tableId: "kubernetesServiceTable",
    headers: ["Name", "Namespaces", "Type", "Cluster-Ip", "External-Ip", "Ports", "Endpoints", "Age"],
  },
  pvc: {
    id: "pvc",
    label: "PVC",
    tableId: "kubernetesPVCTable",
    headers: ["Name", "Namespace", "Status", "Capacity", "StorageClass", "AccessMode", "Age"],
  },
  pv: {
    id: "pv",
    label: "PV",
    tableId: "kubernetesPVTable",
    headers: ["Name", "Capacity", "AccessMode", "Reclaim Policy", "Status", "Claim", "Storage Class", "Age"],
  },
  dbms: {
    id: "dbms",
    label: "Databases",
    tableId: "kubernetesDbmsTable",
    headers: ["Type", "Name", "Namespace", "Status", "Created At", "Updated At"],
  },
  queue: {
    id: "queue",
    label: "Queues",
    tableId: "kubernetesQueueTable",
    headers: ["Type", "Name", "Namespace", "Status", "Created At", "Updated At"],
  },
} as const satisfies Record<string, Listing>;

export const LISTING_ORDER: readonly Listing[] = [
  LISTINGS.nodes,
  LISTINGS.applications,
  LISTINGS.pods,
  LISTINGS.namespaces,
  LISTINGS.services,
  LISTINGS.pvc,
  LISTINGS.pv,
  LISTINGS.dbms,
  LISTINGS.queue,
];

export class AppsAndInfraTablesLocators extends AppsAndInfraLocators {
  // Toolbar controls. FilterDropdown derives its trigger id from the label it is
  // given, so these ids are the label in kebab-case and change only with the label.
  readonly stateFilter: Locator;
  readonly workloadTypeFilter: Locator;
  readonly statusFilter: Locator;

  readonly nodeSearch: Locator;
  readonly applicationSearch: Locator;
  readonly podSearch: Locator;

  // Bulk assign owner — the one write the section offers, covered only as far as
  // its validation and its cancel.
  readonly bulkAssignOwnerBtn: Locator;
  readonly bulkAssignDialog: Locator;
  readonly bulkAssignCancelBtn: Locator;
  readonly bulkAssignSaveBtn: Locator;

  constructor(page: Page) {
    super(page);

    // FilterDropdown's trigger is a <button> whose visible text is the label plus
    // whatever is selected, so an accessible-name match would stop matching the
    // moment a value is picked. The id it derives from the label does not, so it is
    // the primary here, with the name match kept as the fallback for the unselected
    // state. Namespace is not redeclared — ClusterDetailsLocators already owns it as
    // `namespacedropdown`, and it is the same #auto-complete-namespace trigger.
    this.stateFilter = this.filterTrigger("state", "State");
    this.workloadTypeFilter = this.filterTrigger("workload-type", "Workload Type");
    this.statusFilter = this.filterTrigger("status", "Status");

    // SearchInput forwards `label` as the placeholder and these callsites pass no id,
    // so the placeholder is the highest rung available. Each is unique to its own
    // listing and only one listing is mounted at a time.
    this.nodeSearch = page.getByPlaceholder("Node Name");
    this.applicationSearch = page.getByPlaceholder("Application Name");
    this.podSearch = page.getByPlaceholder("Pod Name");

    this.bulkAssignOwnerBtn = page
      .getByRole("button", { name: "Bulk assign owner" })
      .or(page.locator('[id="bulk-assign-owner"]'))
      .first();
    // ds/Modal renders through MUI Dialog and hardcodes #alert-dialog-title on its
    // title node, so filtering the dialog on that title separates this modal from
    // any other dialog the listing can open.
    this.bulkAssignDialog = page
      .getByRole("dialog")
      .filter({ has: page.locator('[id="alert-dialog-title"]', { hasText: "Bulk assign owner" }) })
      .first();
    this.bulkAssignCancelBtn = this.bulkAssignDialog
      .getByRole("button", { name: "Cancel", exact: true })
      .or(this.bulkAssignDialog.locator('[id="bulk-assign-cancel"]'))
      .first();
    // The label carries the selection count ("Assign (3)"), so the name match is a
    // prefix regex rather than an exact string.
    this.bulkAssignSaveBtn = this.bulkAssignDialog
      .getByRole("button", { name: /^Assign/ })
      .or(this.bulkAssignDialog.locator('[id="bulk-assign-save"]'))
      .first();
  }

  // The sub-tab strip, pinned by a tab it is known to contain. AnchorComponent gives
  // the strip no aria-label, and the expanded-row detail panels render tablists of
  // their own, so an unqualified getByRole("tablist") is not unique on this page.
  private get subTabStrip(): Locator {
    return this.page.getByRole("tablist").filter({ has: this.page.locator('[id="nodes"]') }).first();
  }

  // Tabs.jsx renders each sub-tab through MUI Tab with a11yProps(opt.value, opt.id),
  // so the tab carries the listing's own id and its text as the accessible name.
  // `exact` matters: a substring match on "PV" also selects "PVC".
  subTab(listing: Listing): Locator {
    return this.subTabStrip
      .getByRole("tab", { name: listing.label, exact: true })
      .or(this.subTabStrip.locator(`[id="${listing.id}"]`))
      .first();
  }

  // CustomTable puts the caller's id on the <table> and labels every table it renders
  // aria-label="table", so the id is the only handle that tells one listing's table
  // from another's. Left without a fallback deliberately.
  table(listing: Listing): Locator {
    return this.page.locator(`[id="${listing.tableId}"]`);
  }

  // While `loading` is true CustomTable swaps in a skeleton <tbody> carrying no id, so
  // the id'd body being attached means the listing's response has landed.
  tableBody(listing: Listing): Locator {
    return this.page.locator(`[id="${listing.tableId}-body"]`);
  }

  rows(listing: Listing): Locator {
    return this.page.locator(`[id="${listing.tableId}-body"] ${DATA_ROW}`);
  }

  // EmptyData ids its heading `${tableId}-no-data`. No fallback: a page-wide match on
  // the "No Data Available" text would also answer to any other empty panel on screen.
  emptyState(listing: Listing): Locator {
    return this.page.locator(`[id="${listing.tableId}-no-data"]`);
  }

  // A listing either has rows or shows its empty panel in their place, never both, and
  // which one depends on what the shared dev cluster happens to hold. Every assertion
  // about a listing's contents goes through this first so a legitimately empty listing
  // reads as a pass rather than as a locator that never appeared.
  async waitForListingSettled(listing: Listing, timeout = 90000): Promise<void> {
    await this.tableBody(listing).or(this.emptyState(listing)).first().waitFor({ state: "attached", timeout });
  }

  // The nth data row's expand control. CustomTable renders it as an IconButton whose
  // aria-label flips between "Expand row" and "Collapse row" and which carries
  // aria-expanded, so both the handle and the assertion are the accessible state.
  expandToggle(listing: Listing, index = 0): Locator {
    return this.rows(listing)
      .nth(index)
      .getByRole("button", { name: /^(Expand|Collapse) row$/ })
      .first();
  }

  // CustomTablePagination's left-hand summary. It reports either the range it is
  // showing or "No results found", and it is the only place the listing states its
  // total, so it is what a pagination assertion reads. Scoped with .first() because an
  // expanded row's own detail table can render a second summary underneath.
  paginationSummary(): Locator {
    return this.page.getByText(/Showing\s[\d,]+-[\d,]+\sof\s[\d,]+\sresults|No results found/).first();
  }

  // Options are role='option' boxes inside the open panel (FilterDropdown.jsx:195-228).
  // Matched as a CSS role selector with hasText, deliberately NOT
  // getByRole("option", { name }): each row nests its label inside a CustomTooltip
  // span, and accessible-name matching does not resolve these rows — the same MUI trap
  // the standard calls out for menuitem. Proven by CI, where the role+name form found
  // nothing even against this filter's two hardcoded options. This is the pattern the
  // admin specs already use (tests/admin/Audits/auditsLocators.ts).
  // `:visible` is the scope: only one panel is open at a time, and every other
  // dropdown's options are unmounted rather than merely hidden.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  // The distinct, sorted values of one column, joined — but only once the listing has
  // settled, and null until then. A refetch swaps in CustomTable's skeleton <tbody>,
  // which carries no id, so "neither the id'd body nor the empty panel is present"
  // is exactly the mid-flight window. Polling on this waits the refetch out instead of
  // reading the pre-filter rows or mistaking the loading gap for an empty result.
  //
  // `extract` narrows a cell that renders more than one value to the part under test —
  // a whole-cell read otherwise concatenates them (Nodes' Status cell yields
  // "ActiveReady", the state Label followed by the readiness Text). A cell the
  // extractor does not recognise is returned whole, so a surprise shows up in the diff
  // rather than being quietly normalised away.
  async settledColumnValues(listing: Listing, nthChild: number, extract: (cellText: string) => string = (cellText) => cellText): Promise<string | null> {
    const [bodyCount, emptyCount] = await Promise.all([this.tableBody(listing).count(), this.emptyState(listing).count()]);
    if (bodyCount === 0 && emptyCount === 0) return null;
    if (emptyCount > 0) return "";
    const cells = await this.rows(listing).locator(`td:nth-child(${nthChild})`).allTextContents();
    return [...new Set(cells.map((cell) => extract(cell.trim())))].sort().join(",");
  }

  private filterTrigger(kebabLabel: string, label: string): Locator {
    return this.page
      .locator(`[id="auto-complete-${kebabLabel}"]`)
      .or(this.page.getByRole("button", { name: label, exact: true }))
      .first();
  }
}
