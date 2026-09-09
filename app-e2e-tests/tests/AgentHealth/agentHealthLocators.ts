// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Column contracts, copied from the header arrays in app/src/pages/agentHealth.jsx.
export const AGENT_HEADERS = ["Status", "Agent Version", "Latest Version", "Last Connected", "K8s(Provider/Version)"];

// Every entry the Agent tab's Features block renders for a k8s account, in page order.
export const AGENT_FEATURES = ["Relay", "Prometheus", "Alert Manager", "Logs", "Traces", "OpenCost", "Node Agent"];

// What a feature row is allowed to report. OpenCost has a third state ("Managed server-side")
// because the spend sync can collect it outside the agent — see agentHealth.jsx.
export const FEATURE_STATE = /Connected|Disconnected|Managed server-side/;

// The status the page renders when the agent has never reported in. agentHealth.jsx prints
// `acc.status.replace('_', ' ')`, so the raw NOT_CONNECTED reaches the cell as "NOT CONNECTED".
export const NOT_CONNECTED = "NOT CONNECTED";

export class AgentHealthLocators extends CommonLocators {
  readonly agentTab: Locator;
  readonly proxyAgentTab: Locator;

  readonly agentCard: Locator;
  readonly agentTable: Locator;
  readonly agentEmpty: Locator;
  readonly notConnectedMessage: Locator;
  readonly upgradeWarning: Locator;
  readonly featuresHeading: Locator;
  readonly scheduledJobsCard: Locator;

  readonly proxyCard: Locator;
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

    // The three cards are ListingLayout ids and nothing else: ListingLayout renders a plain
    // Card <div> with no data-testid, no role and no accessible name, and the only text in
    // it is the toolbar title — which is a child node, so matching on it would return the
    // title rather than the card. Deliberately id-only; a wider fallback would be wrong.
    this.agentCard = page.locator("#agent-health");
    this.scheduledJobsCard = page.locator("#scheduled-jobs-table");
    this.proxyCard = page.locator("#proxy-agent-health");

    // Scoped to its own card, so the CSS fallback cannot drift to another table.
    this.agentTable = this.agentCard.getByRole("table").or(this.agentCard.locator("table")).first();

    // CustomTable is given no `id` here, so EmptyData's `#<id>-no-data` handle is useless —
    // the heading is an <h2>, which is what the role primary reaches.
    this.agentEmpty = this.agentCard
      .getByRole("heading", { name: "No Data Available" })
      .or(this.agentCard.getByText("No Data Available", { exact: true }))
      .first();

    // Both banners are bare <Typography color='red'> inside the card: no role, no id, and
    // the copy is the only thing identifying them. Scoped to the card and left fallback-less
    // rather than matched page-wide.
    this.notConnectedMessage = this.agentCard.getByText("The Agent is not connected", { exact: true });
    this.upgradeWarning = this.agentCard.getByText("Please update your agent version", { exact: true });
    this.featuresHeading = this.agentCard.getByText("Features", { exact: true });

    // The proxy empty state is an unadorned Box; its headline is the only stable handle.
    this.proxyOnboarding = page.getByText("Get started with Proxy Agent monitoring", { exact: true });
    // Whichever of the two the account has: a proxy agent reporting in gets the health card,
    // an account without one gets the onboarding panel. Both are the Proxy Agent tab's own body.
    this.proxyPanel = this.proxyCard.or(this.proxyOnboarding).first();
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

  // One Features entry. Filtering on "<name> - " picks the outer <li> that owns the label:
  // the nested detail items under Prometheus/Logs/Traces/OpenCost read "Status - ", "URL - "
  // and never repeat their parent's label.
  featureItem(name: string): Locator {
    return this.agentCard
      .locator("li")
      .filter({ hasText: `${name} - ` })
      .first();
  }

  // Settles the card on its end state. Skeleton-gone is checked first and the outcome
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
