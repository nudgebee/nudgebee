import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Contracts copied from app/src/pages/agentHealth.jsx. They describe the K8s shape of the
// page: a cloud account keeps the older one-row agent table (HEADERS_CLOUD) and a Features
// block, which this suite's configured cluster never renders.
// The facts the K8s summary card states about the install, as the `agent-fact-<key>`
// testids name them. Same five values the old five-column agent table carried, minus
// "Latest Version" — which now only appears in the update callout, since a cluster that
// is already current has no use for it.
export const AGENT_FACTS = ["version", "kubernetes", "namespace", "agent-url"];

// Every component the Agent tab's Components table reports on, in page order, keyed the
// way its `component-<key>` testid is.
export const AGENT_COMPONENTS = ["relay", "prometheus", "alert-manager", "logs", "traces", "opencost", "node-agent"];

// What a component row is allowed to report. OpenCost has a third state ("Managed
// server-side") because the spend sync can collect it outside the agent, and
// logs/metrics/traces read "Served by integration" when a non-agent backend owns that
// signal — the Agent tab states no health verdict of its own for those, and the
// Observability tab reports them instead.
export const COMPONENT_STATE = /Connected|Disconnected|Managed server-side|Served by integration/;

// Column contract of the Components table, copied from HEADERS_COMPONENTS.
export const COMPONENT_HEADERS = ["Component", "Status", "Endpoint", "Details"];

// Column contract of the Scheduled Jobs table, copied from HEADERS_SCHEDULED_JOBS.
export const SCHEDULED_JOB_HEADERS = ["Action Name", "Job Status", "Schedule", "Last Execution", "Executions"];

// The status the summary card's chip reports when the agent has never reported in.
// agentHealth.jsx prints the raw status with underscores swapped for spaces, so
// NOT_CONNECTED reaches the chip as "NOT CONNECTED".
export const NOT_CONNECTED = "NOT CONNECTED";

// The signals the Observability tab reports on, keyed as their `observability-<key>`
// testids are, and what each card's chip is allowed to say.
export const OBSERVABILITY_SIGNALS = ["logs", "metrics", "traces"];
export const SIGNAL_STATE = /Connected|Disconnected|Not configured/;

export class AgentHealthLocators extends CommonLocators {
  readonly agentTab: Locator;
  readonly proxyAgentTab: Locator;
  readonly observabilityTab: Locator;

  readonly agentCard: Locator;
  readonly agentStatusChip: Locator;
  readonly disconnectedBanner: Locator;
  readonly servicesBanner: Locator;
  readonly updateBanner: Locator;
  readonly updateGuideButton: Locator;

  readonly componentsCard: Locator;
  readonly componentsTable: Locator;
  readonly componentsEmpty: Locator;

  readonly scheduledJobsCard: Locator;
  readonly scheduledJobsTable: Locator;
  readonly scheduledJobsFilter: Locator;

  // The pre-redesign Agent tab's "Features" heading. Kept solely so the suite can tell an
  // app that predates the redesign from one that already carries it — see
  // hasRedesignedAgentTab.
  readonly legacyFeaturesHeading: Locator;

  readonly proxyCard: Locator;
  readonly observabilityCard: Locator;
  readonly observabilitySummary: Locator;
  readonly proxyOnboarding: Locator;
  readonly proxyPanel: Locator;

  constructor(page: Page) {
    super(page);

    // Role-first: @shared/navigation/Tabs renders each entry as a MUI Tab, so it carries
    // role="tab" and its label as the accessible name. The id fallback is the `id` those
    // same tabOptions declare (AGENT_TAB / PROXY_AGENT_TAB in agentHealth.jsx). "Agent" is
    // a prefix of "Proxy Agent", so the primary has to be exact.
    this.agentTab = page.getByRole("tab", { name: "Agent", exact: true }).or(page.locator("#tab-agent")).first();
    this.proxyAgentTab = page.getByRole("tab", { name: "Proxy Agent", exact: true }).or(page.locator("#tab-proxy-agent")).first();
    this.observabilityTab = page
      .getByRole("tab", { name: "Observability", exact: true })
      .or(page.locator("#tab-observability"))
      .first();

    // The cards are ListingLayout ids and nothing else: ListingLayout renders a plain
    // Card <div> with no data-testid, no role and no accessible name, and the only text in
    // it is the header/toolbar title — which is a child node, so matching on it would
    // return the title rather than the card. Deliberately id-only; a wider fallback would
    // be wrong.
    this.agentCard = page.locator("#agent-health");
    this.componentsCard = page.locator("#agent-components");
    this.scheduledJobsCard = page.locator("#scheduled-jobs-table");
    this.proxyCard = page.locator("#proxy-agent-health");
    this.observabilityCard = page.locator("#observability");

    // ds/Label takes an `id` but no data-testid (it declares its props and drops the rest),
    // so the chips carry ids. Same for ds/Banner.
    this.agentStatusChip = page.locator("#agent-status");
    this.disconnectedBanner = page.locator("#agent-disconnected-banner");
    this.servicesBanner = page.locator("#agent-services-banner");
    this.updateBanner = page.locator("#agent-update-banner");
    this.updateGuideButton = page.getByTestId("agent-update-guide-btn");
    this.observabilitySummary = page.locator("#observability-summary");

    // Each table is given an `id` by the page, which CustomTable puts on the <table>
    // itself; role="table" via its aria-label is the primary, the id the fallback.
    this.componentsTable = this.componentsCard.getByRole("table").or(page.locator("#agent-components-table")).first();
    this.scheduledJobsTable = this.scheduledJobsCard.getByRole("table").or(page.locator("#scheduled-jobs")).first();
    this.scheduledJobsFilter = page.locator("#scheduled-jobs-filter");

    // CustomTable's EmptyData heading is an <h2> carrying `#<table id>-no-data`.
    this.componentsEmpty = this.emptyIn(this.componentsCard);

    this.legacyFeaturesHeading = this.agentCard.getByText("Features", { exact: true });

    // The proxy empty state is an unadorned Box; its headline is the only stable handle.
    this.proxyOnboarding = page.getByText("Get started with Proxy Agent monitoring", { exact: true });
    // Whichever of the two the account has: a proxy agent reporting in gets the health card,
    // an account without one gets the onboarding panel. Both are the Proxy Agent tab's own body.
    this.proxyPanel = this.proxyCard.or(this.proxyOnboarding).first();
  }

  emptyIn(card: Locator): Locator {
    return card
      .getByRole("heading", { name: "No Data Available" })
      .or(card.getByText("No Data Available", { exact: true }))
      .first();
  }

  // Rows that hold real data. While the fetch is in flight CustomTable swaps in a skeleton
  // <tbody> whose rows look identical to a plain `tbody tr` count, so the skeleton cells —
  // @ui/Skeleton renders role="status" aria-busy="true" — are excluded structurally.
  dataRowsIn(table: Locator): Locator {
    return table.locator('tbody tr:not(:has([aria-busy="true"]))');
  }

  skeletonsIn(table: Locator): Locator {
    return table.locator('[aria-busy="true"]');
  }

  columnHeaderIn(table: Locator, name: string): Locator {
    return table.getByRole("columnheader", { name, exact: true }).or(table.locator("th", { hasText: name })).first();
  }

  // One fact from the summary card — "Agent version", "Kubernetes", "Namespace", "Agent URL".
  agentFact(key: string): Locator {
    return this.agentCard.getByTestId(`agent-fact-${key}`);
  }

  // One row of the Components table. The name cell carries the testid, so the row is its
  // ancestor <tr> — matched structurally rather than by the component's label, which is a
  // substring of nothing but is still safer read this way ("Logs" also appears in the
  // Details column of other rows).
  componentRow(key: string): Locator {
    return this.dataRowsIn(this.componentsTable).filter({ has: this.page.getByTestId(`component-${key}`) }).first();
  }

  // One option of the Scheduled Jobs filter. ds/ToggleGroup in single-selection mode
  // renders each option as role="radio" (not a button) with its label as the accessible
  // name, and marks the active one with aria-checked.
  jobFilterOption(name: RegExp): Locator {
    return this.scheduledJobsFilter.getByRole("radio", { name });
  }

  // One signal's card on the Observability tab.
  signalCard(key: string): Locator {
    return this.observabilityCard.getByTestId(`observability-${key}`);
  }

  // Settles a card on its end state. Skeleton-gone is checked first and the outcome
  // second, because CustomTable renders the empty panel only once `loading` is false —
  // asserting the outcome alone would accept the pre-fetch frame.
  async waitForTable(table: Locator, empty: Locator, timeout = 60000): Promise<void> {
    await expect(this.skeletonsIn(table)).toHaveCount(0, { timeout });
    await expect(this.dataRowsIn(table).first().or(empty)).toBeVisible({ timeout });
  }

  // Cell text of one data row, left to right.
  async rowCells(table: Locator, index = 0): Promise<string[]> {
    const cells = this.dataRowsIn(table).nth(index).locator("td");
    return (await cells.allTextContents()).map((cell) => cell.trim());
  }
}
