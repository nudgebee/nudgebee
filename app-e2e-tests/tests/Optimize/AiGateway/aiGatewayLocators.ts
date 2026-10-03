// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeLocators } from "../OptimizeLocators";

// AI Gateway — the BYO-token gateway usage module on the tenant Optimize page
// (app/src/components/llm/gateway-usage/GatewayUsage.tsx), mounted by
// app/src/pages/optimise/index.jsx as filterOptions id 'ai-gateway', fragment
// 'ai-gateway'. Its own screens are a CustomTabs strip that writes
// #ai-gateway/<screen> into the URL.
//
// Rung choices below follow the qa-automation-code-check ladder against what the
// module actually renders, measured rather than assumed:
//   GatewayUsage.tsx 0   ConnectView 0   OverviewView 0   ModelsView 0
//   UsersView 0   ToolsView 0   RequestsView 1   SessionsView 1   GovernanceView 1
// (`grep -c data-testid` over each file under components/llm/gateway-usage.) The three
// testids that do exist are per-row template ids on elements this suite never touches,
// so rung 1 is unavailable for every control below — role leads wherever the element
// carries an accessible name, and the ids the module renders are the rest.

// Screens on the inner strip, in the left-to-right order TAB_IDS declares them, so a
// caller iterating this reads the same order a user does.
export const GATEWAY_SCREENS = ["connect", "overview", "models", "users", "requests", "sessions", "tools", "governance"] as const;

export type GatewayScreen = (typeof GATEWAY_SCREENS)[number];

// CustomTable renders `${id}` on the table and `${id}-body` on the tbody; while a fetch
// is in flight it swaps in a skeleton tbody that carries no id, so the id'd body being
// attached is the signal the response landed.
export const REQUESTS_TABLE = "gateway-requests-table";

// RequestsView's Status column is the 8th in HEADERS, and each cell renders the raw HTTP
// code through StatusPill (an em-dash when the request recorded none).
const REQUESTS_STATUS_CELL = "td:nth-child(8)";

export class AiGatewayLocators extends OptimizeLocators {
  // Outer Optimize strip.
  readonly aiGatewayTab: Locator;

  // Module root and inner strip.
  readonly root: Locator;
  readonly screenStrip: Locator;

  // Shared filter bar — date window + chart granularity, withheld on Connect.
  readonly filterBar: Locator;
  readonly granularityGroup: Locator;

  // Overview screen.
  readonly kpiRow: Locator;
  readonly usageOverTime: Locator;
  readonly metricGroup: Locator;
  readonly providerTable: Locator;
  readonly modelTable: Locator;
  readonly userTable: Locator;

  // Connect screen.
  readonly connectRoot: Locator;
  readonly generateTokenBtn: Locator;

  // Requests screen.
  readonly requestsStatusFilter: Locator;
  readonly requestsBody: Locator;
  readonly requestsStatusCells: Locator;
  readonly requestsFilteredEmpty: Locator;
  readonly requestsClearFiltersBtn: Locator;

  // Sessions screen.
  readonly sessionsSearch: Locator;
  readonly sessionsBody: Locator;
  readonly sessionsEmpty: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders every Optimize tab as `#anchor-tab-<opt.id>`. "AI Gateway"
    // is not unique on the page — the sidebar's Optimize flyout carries the same label —
    // and `.or()` resolves in document order, so a page-wide role fallback could return
    // the sidebar link and navigate away. The id is the highest unambiguous rung here.
    this.aiGatewayTab = page.locator("#anchor-tab-ai-gateway");

    // A bare <Box id> with no role and no accessible name, so rungs 1-2 do not exist and a
    // text or CSS fallback would match the page body just as well. Id-only, deliberately.
    this.root = page.locator("#gateway-usage-root");

    // CustomTabs passes ariaLabel straight to MuiTabs' aria-label, and MUI gives that
    // element role="tablist" — a real accessible name, so this is rung 2 with the module
    // root as the structural fallback. `.first()` matters: the Connect screen mounts a
    // second CustomTabs (its snippet picker) inside the same root, and the outer strip is
    // the one that comes first in document order.
    this.screenStrip = page.getByRole("tablist", { name: "AI Gateway screens" }).or(this.root.locator('[role="tablist"]')).first();

    // Also a bare <Box id>, and it is the scope the granularity group narrows to — a wider
    // fallback here would widen that one too, so it stays id-only.
    this.filterBar = page.locator("#gateway-filter-bar");

    // ds/ToggleGroup renders role="group" with the ariaLabel as its accessible name and one
    // role="radio" per option. No id is passed to either group, so role is the only rung
    // both offer; each is scoped so its fallback cannot reach the other.
    this.granularityGroup = this.filterBar.getByRole("group", { name: "Chart granularity" });
    this.metricGroup = this.root.getByRole("group", { name: "Chart metric" });

    // The Overview widgets are bare <Box id> wrappers and CustomTable ids — none carries a
    // role or an accessible name, and each holds only figures this suite must not pin, so
    // the id is the only rung.
    this.kpiRow = page.locator("#gateway-kpi-row");
    this.usageOverTime = page.locator("#gateway-usage-over-time");
    this.providerTable = this.tableOrNoData("gateway-provider-table");
    this.modelTable = this.tableOrNoData("gateway-model-table");
    this.userTable = this.tableOrNoData("gateway-user-table");

    // Another bare <Box id>, and the screen's own heading copy ("Your endpoints", "Setup")
    // sits inside it rather than naming it — so there is no role or accessible name to lead
    // with and a text fallback would match a child of the very element being located.
    this.connectRoot = page.locator("#gateway-connect");

    // Visible text on a ds/Button, so role leads and the id ConnectView renders backs it up.
    // Scoped to the module root because the other Optimize tabs stay mounted behind this one.
    this.generateTokenBtn = this.root
      .getByRole("button", { name: "Generate token" })
      .or(page.locator("#gateway-connect-generate-token-btn"))
      .first();

    // FilterDropdown renders its trigger as a <button> whose leading text is the label and
    // gives it `auto-complete-${toKebabCase(id)}`, so role leads and the id backs it up.
    // Scoped to the module root so a fallback cannot reach an identically labelled filter on
    // another Optimize tab.
    this.requestsStatusFilter = this.root
      .getByRole("button", { name: /^Status/ })
      .or(page.locator("#auto-complete-gateway-requests-filter-status"))
      .first();

    // Every screen renders a table, so role="table" is ambiguous page-wide and a fallback on
    // it could return another screen's table left mounted behind this one. Id-only.
    this.requestsBody = page.locator(`#${REQUESTS_TABLE}-body`);
    this.requestsStatusCells = page.locator(`#${REQUESTS_TABLE}-body tr ${REQUESTS_STATUS_CELL}`);

    // ds/EmptyState renders role="status" with the title inside it. RequestsView titles the
    // empty body differently depending on whether a filter is active, and only the filtered
    // wording is a legitimate body for a filtered list — which is what makes this the
    // assertion rather than a bare "something rendered".
    this.requestsFilteredEmpty = this.emptyState("No matching requests");
    this.requestsClearFiltersBtn = this.requestsFilteredEmpty.getByRole("button", { name: "Clear filters" });

    // ds/Input puts the caller's id on the <input> itself; the placeholder scoped to the
    // module root is the fallback, so a moved id degrades to a slower match rather than a
    // red suite.
    this.sessionsSearch = page.locator("input#gateway-sessions-search").or(this.root.getByPlaceholder("Search session id")).first();

    // The other legitimate Sessions body, id-only for the same reason as the Requests one:
    // role="table" is ambiguous with the screens left mounted behind this one.
    this.sessionsBody = page.locator("#gateway-sessions-table-body");
    this.sessionsEmpty = this.emptyState("No sessions");
  }

  // One tab on the inner strip. MUI Tab keeps role="tab" and aria-selected in CustomTabs'
  // behavior='filter' mode (it renders as a plain button there, not a Link), so role scoped
  // to the strip is unambiguous; the id a11yProps derives from the tab value is the fallback.
  //
  // No `exact` here on purpose: CustomTabs renders a SafeIcon whose alt is the tab text
  // beside the label, so the accessible name can read as the label twice over — an exact
  // match would resolve nothing.
  screenTab(screen: GatewayScreen): Locator {
    return this.screenStrip
      .getByRole("tab", { name: SCREEN_LABEL[screen] })
      .or(this.page.locator(`#tab-${screen}`))
      .first();
  }

  // One option inside a ds/ToggleGroup. Each is a ButtonBase with role="radio" and the
  // option's visible label, so role is the only rung the component offers. exact: true
  // because getByRole matches the accessible name as a substring by default and this
  // module's own option labels repeat elsewhere on the same screen ("Requests" names both
  // a metric and a sub-tab).
  toggleOption(group: Locator, label: string): Locator {
    return group.getByRole("radio", { name: label, exact: true });
  }

  // A CustomTable that may legitimately have nothing to draw. It returns early with an
  // EmptyData box when the rows are empty, so `#<id>` is absent then and only the h2 that
  // EmptyData ids as `#<id>-no-data` is on screen. Both are the widget rendering; which one
  // appears is tenant data, which this suite must not pin.
  tableOrNoData(id: string): Locator {
    return this.page.locator(`#${id}`).or(this.page.locator(`#${id}-no-data`)).first();
  }

  // A ds/EmptyState body. It renders role="status" with the title inside, so role plus the
  // title is unambiguous even with another screen's empty state still mounted behind it.
  emptyState(title: string): Locator {
    return this.page.getByRole("status").filter({ hasText: title }).first();
  }

  // One option row in an open FilterDropdown panel (the role='option' boxes OptionItem
  // renders, FilterDropdown.jsx:199).
  //
  // NOT getByRole("option"): FilterDropdown defaults to disablePortal (line 792) and
  // RequestsView does not override it, so the Popover mounts INLINE and MUI's ModalManager
  // marks that container aria-hidden while it is open — the accessibility tree then exposes
  // no options at all and getByRole matches zero. Proved in CI on this suite's first run:
  // the wait timed out at 30s with "element(s) not found" while the panel was on screen.
  // The CSS role attribute survives, which is why admin/Audits locates the same component's
  // options this way. Same trap as admin/Ownership and admin/roles document for ds/Select.
  //
  // Anchored so "Error" cannot also resolve a label that merely contains it, and :visible
  // because only one panel is ever open — every other dropdown's options are unmounted.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  // The Overview chart's section header, which SectionHeader writes from the selected
  // metric — the readable proof the toggle reached the chart rather than only restyling
  // itself.
  chartHeading(metricLabel: string): Locator {
    return this.root.getByText(`${metricLabel} over time`);
  }
}

// Same local helper the admin and ClusterDetails locators declare — this suite anchors an
// option label that carries regex metacharacters ("Success (2xx)").
function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Tab copy from tabOptions in GatewayUsage.tsx. Kept beside the locator that reads it so a
// renamed tab is one edit, not a hunt through the spec.
const SCREEN_LABEL: Record<GatewayScreen, string> = {
  connect: "Connect",
  overview: "Overview",
  models: "Models",
  users: "Users",
  requests: "Requests",
  sessions: "Sessions",
  tools: "Tools",
  governance: "Governance",
};
