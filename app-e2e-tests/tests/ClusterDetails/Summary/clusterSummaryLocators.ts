// Not for OSS
import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

// Anchor ids for the Summary tab's jump-nav, declared as `options` (not `tabOptions`)
// on tabOptions[0] in app/src/pages/kubernetes/details/[KubernetesDetails].jsx. Only
// these two reach the DOM: they are passed down as the `id` prop of each section's
// ListingLayout. The third option id, 'cluster-summary', is never rendered anywhere —
// see COST_SECTION's note and the PR's Follow-ups.
export const COST_SECTION = "cost-summary";
export const UTILIZATION_SECTION = "utilization";

// The chart canvas id KuberneteComputeSummary passes to Chart.Bar / Chart.Line. Both
// branches receive the same id (LineCharts does `uniqueId = id || uuidv4()`), so the
// canvas proves the fetch landed but never which chart type is on screen — the
// ToggleGroup's aria-checked is what distinguishes those.
export const COST_CHART_CANVAS = "KuberneteComputeCostSummaryChart";

export class ClusterSummaryLocators extends ClusterDetailsLocators {
  // Jump-nav buttons above the sections (AnchorComponent.jsx, the
  // `filterOptions[activeDropdownTab]?.options` branch).
  readonly jumpNavClusterSummary: Locator;
  readonly jumpNavCostSummary: Locator;
  readonly jumpNavUtilization: Locator;

  // KubernetesClusterSummary.jsx — headline stats and section headings.
  readonly nodesStat: Locator;
  readonly applicationsStat: Locator;
  readonly podsStat: Locator;
  readonly insightsHeading: Locator;
  readonly utilizationAndHealthHeading: Locator;
  readonly quickLinksHeading: Locator;

  // Cost Summary section (KubernetesComputeSummary.jsx).
  readonly costSection: Locator;
  readonly costFrequencyFilter: Locator;
  readonly costChartBarToggle: Locator;
  readonly costChartLineToggle: Locator;
  readonly costChartCanvas: Locator;
  readonly costDownloadBtn: Locator;

  // Utilization section (KuberneteUtilizationSummary.jsx).
  readonly utilizationSection: Locator;
  readonly utilizationFrequencyFilter: Locator;

  constructor(page: Page) {
    super(page);

    // Role primary, deliberately with no `.or()`: the jump-nav strip is an unadorned
    // MUI Button row carrying neither an id nor a testid, and each button's label is
    // repeated verbatim by the section Heading it scrolls to — so any getByText
    // fallback would resolve to that <p> in document order and click nothing.
    this.jumpNavClusterSummary = page.getByRole("button", { name: "Cluster Summary", exact: true }).first();
    this.jumpNavCostSummary = page.getByRole("button", { name: "Cost Summary", exact: true }).first();
    this.jumpNavUtilization = page.getByRole("button", { name: "Utilization", exact: true }).first();

    // ds/Stat renders role="button" only when it is given an onClick, which these three
    // are (each routes into an Apps & Infra sub-tab). Its accessible name is the label
    // followed by the value, so the name is anchored with ^ rather than matched exactly.
    this.nodesStat = page.getByRole("button", { name: /^Nodes/ }).first();
    this.applicationsStat = page.getByRole("button", { name: /^Applications/ }).first();
    this.podsStat = page.getByRole("button", { name: /^Pods/ }).first();

    // components/common/Heading.tsx renders a plain Typography <p> — no heading role —
    // so these are text matches. Each string is unique on the tab, unlike "Cluster
    // Summary", which the jump-nav button above also carries.
    this.insightsHeading = page.getByText("Insights", { exact: true }).first();
    this.utilizationAndHealthHeading = page.getByText("Utilization & Health", { exact: true }).first();
    this.quickLinksHeading = page.getByText("Quick Links", { exact: true }).first();

    // Neither the page nor any component in this area renders a single data-testid
    // (verified with `grep -c data-testid` over all five files), so P1's rung 3 is the
    // top available rung. These two ids are also the app's own scroll targets, which
    // makes them a contract the jump-nav would break with them.
    this.costSection = page.locator(`#${COST_SECTION}`);
    this.utilizationSection = page.locator(`#${UTILIZATION_SECTION}`);

    // ds/FilterDropdown renders `id="auto-complete"` whenever the caller passes no
    // inputId — and both toolbars on this tab do exactly that, so the id is duplicated
    // across the page. Scoping to the owning section is what makes each one unique; an
    // unscoped `#auto-complete` would silently return the Cost Summary one for both.
    this.costFrequencyFilter = this.costSection
      .locator("#auto-complete")
      .or(this.costSection.getByRole("button", { name: /Frequency/ }))
      .first();
    this.utilizationFrequencyFilter = this.utilizationSection
      .locator("#auto-complete")
      .or(this.utilizationSection.getByRole("button", { name: /Frequency/ }))
      .first();

    // ChartSwitcher is a ds/ToggleGroup with selection="single", so each option is a
    // <button role="radio">. Its accessible name is the TOOLTIP text, not the label:
    // ToggleGroup wraps every option carrying a `tooltip` in a MUI Tooltip, and with
    // describeChild left at its default false, Tooltip.js writes
    // `nameOrDescProps['aria-label'] = title` onto the child — so these radios are named
    // "Bar chart" / "Line chart". `{ name: "Bar", exact: true }` matched zero elements in
    // CI for exactly that reason. The regex accepts either form so the locator survives
    // the tooltip being reworded away or dropped. (VM's identical getByRole("radio")
    // pattern works because its GROUP_TABS options carry no tooltip.)
    this.costChartBarToggle = this.costSection.getByRole("radio", { name: /^Bar( chart)?$/i }).first();
    this.costChartLineToggle = this.costSection.getByRole("radio", { name: /^Line( chart)?$/i }).first();

    this.costChartCanvas = page.locator(`#${COST_CHART_CANVAS}`);
    // DownloadButton forwards the caller's id straight to ds/Button, and
    // KuberneteComputeSummary passes `${id}-download`.
    this.costDownloadBtn = page.locator(`#${COST_SECTION}-download`);
  }

  // Options inside an open FilterDropdown panel. The panel is a portalled MUI Popover,
  // so it is addressed at page level rather than through the section that owns the
  // trigger — only one dropdown is ever open at a time, which keeps the match unique.
  //
  // Filtered on the row's TEXT, not on its accessible name. Each option's label goes
  // through OptionLabel, which wraps it in a CustomTooltip; MUI writes a Tooltip title
  // onto its child as aria-label, so an option's computed name is not reliably its label.
  // The row's text content is just the label, and these three options carry no badge,
  // type or icon that could add to it.
  filterOption(name: string): Locator {
    return this.page
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${name}$`) })
      .first();
  }

  // Opens a FilterDropdown and waits for its panel before anything tries to read it.
  //
  // The click is deliberately offset to the trigger's left edge instead of Playwright's
  // default centre point. ds/FilterDropdown defaults `clearable` to true and renders a
  // clickable clear "X" at the trigger's RIGHT edge whenever a value is selected — which
  // is always here, since Frequency ships selected on "Month". A centre click on a
  // compact toolbar trigger can land on that X, which fires handleClear and clears the
  // selection instead of opening the panel: no panel, and every option lookup then fails
  // with "element(s) not found" — the CI failure this replaces.
  async openFilter(trigger: Locator): Promise<void> {
    await trigger.click({ position: { x: 8, y: 8 } });
    // Fail here, on the panel, rather than later on a named option, so "the dropdown
    // never opened" stays distinguishable from "the option is named something else".
    await this.page
      .locator('[role="option"]')
      .first()
      .waitFor({ state: "visible", timeout: 15000 });
  }

  // Quick Links are DSLink anchors carrying the link name as their only text
  // (KubernetesClusterSummary.jsx). A name the current user lacks :Read for renders as
  // a disabled <p> instead, which is why callers assert reachability rather than assume.
  quickLink(name: string): Locator {
    return this.page.getByRole("link", { name, exact: true }).first();
  }

  // Utilization renders four DSCards labelled CPU / Memory (GB) / Network Ingress (GB) /
  // Network Egress (GB). Scoped to the section because "CPU" also appears in the
  // Utilization & Health card higher up the page.
  utilizationCard(label: string): Locator {
    return this.utilizationSection.getByText(label, { exact: true }).first();
  }

  // The chart is swapped for a Loader while its request is in flight (BarChart.jsx's
  // `loading` branch renders no canvas at all), so the canvas being attached is the
  // signal that the cost data has landed and the toolbar is acting on real state.
  async waitForCostChart(timeout = 60000): Promise<void> {
    await this.costChartCanvas.waitFor({ state: "attached", timeout });
  }
}
