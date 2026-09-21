// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeLocators } from "../OptimizeLocators";

// LLM Analyser — the Cost Analyser tab on the tenant Optimize page
// (app/src/components/llm/cost-analyser/CostAnalyser.tsx), mounted by
// app/src/pages/optimise/index.jsx as filterOptions id 'llm-analyser', fragment
// 'cost-analyser'. Its own screens are a CustomTabs strip that writes
// #cost-analyser/<screen> into the URL.
//
// Rung choices below follow the qa-automation-code-check ladder against what the
// module actually renders, measured rather than assumed:
//   CostAnalyser.tsx 0   FilterBar.tsx 0   OverviewView.tsx 0   ConversationsView.tsx 0
//   ModelsView.tsx   0   UsersView.tsx 0   ToolsView.tsx    0   AgentsView.tsx      0
// (`grep -c data-testid` over every file under components/llm/cost-analyser returns 0.)
// Rung 1 is therefore unavailable everywhere in this module — role leads wherever the
// element carries an accessible name, and the ids the module does render are the rest.

// Screens on the inner strip. `critiques` is last on the strip but is listed here in
// strip order so a caller iterating this reads the same left-to-right order a user does.
// 'cost-report' is deliberately absent: CostAnalyser.tsx renders it only for
// isTenantWideRole(), so a suite that asserted it would pass or fail on the run user's
// role rather than on the module.
export const ANALYSER_SCREENS = ["overview", "conversations", "models", "agents", "tools", "users", "critiques"] as const;

export type AnalyserScreen = (typeof ANALYSER_SCREENS)[number];

// CustomTable renders `${id}` on the table, `${id}-body` on the tbody, and — through
// EmptyData — `${id}-no-data` on the empty-state heading.
const DATA_ROW = "tr:has(td:nth-child(2))";

export const CONVERSATIONS_TABLE = "conversations-table";

export class LlmAnalyserLocators extends OptimizeLocators {
  // Outer Optimize strip.
  readonly llmAnalyserTab: Locator;

  // Module root and inner strip.
  readonly root: Locator;
  readonly screenStrip: Locator;

  // Shared filter bar.
  readonly filterBar: Locator;
  readonly agentFilter: Locator;
  readonly userFilter: Locator;
  readonly resetButton: Locator;
  readonly granularityGroup: Locator;

  // Overview screen.
  readonly kpiRow: Locator;
  readonly costOverTime: Locator;
  readonly breakdowns: Locator;
  readonly topConversationsTable: Locator;

  // Conversations screen.
  readonly conversationPresets: Locator;
  readonly conversationsRows: Locator;
  readonly conversationsEmpty: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders every Optimize tab as `#anchor-tab-<opt.id>` and marks the
    // open one with data-tab-selected. "LLM Analyser" is not unique on the page — the
    // sidebar's Optimize flyout carries the same label — and `.or()` resolves in document
    // order, so a page-wide role fallback could return the sidebar link and navigate away.
    // The id is the highest unambiguous rung here.
    this.llmAnalyserTab = page.locator("#anchor-tab-llm-analyser");

    // A bare <Box id> with no role and no accessible name, so rungs 1-2 do not exist and a
    // text or CSS fallback would match the page body just as well. Id-only, deliberately.
    this.root = page.locator("#cost-analyser-root");

    // CustomTabs passes ariaLabel straight to MuiTabs' aria-label, and MUI gives that
    // element role="tablist" — a real accessible name, so this is rung 2 with the module
    // root as the structural fallback. Every screen tab below is scoped to this strip so
    // its own fallback cannot escape into the sidebar or a page heading.
    this.screenStrip = page.getByRole("tablist", { name: "Cost Analyser screens" }).or(this.root.locator('[role="tablist"]')).first();

    // Also a bare <Box id>, and it is the scope every filter locator below narrows to — a
    // wider fallback here would widen all of them at once, so it stays id-only.
    this.filterBar = page.locator("#cost-filter-bar");

    this.agentFilter = this.filterTrigger("cost-filter-agent", "Agent");
    this.userFilter = this.filterTrigger("cost-filter-user", "User");

    // Visible text on a ds/Button, so role leads and the id FilterBar.tsx renders backs
    // it up. Scoped to the filter bar because "Reset" also names controls on the other
    // Optimize tabs, which stay mounted behind this one.
    this.resetButton = this.filterBar.getByRole("button", { name: "Reset" }).or(page.locator("#cost-filter-reset")).first();

    // ds/ToggleGroup renders role="group" with the ariaLabel as its accessible name and
    // one role="radio" per option. No id is passed here, so role is both the primary and
    // the only rung — a structural fallback inside the bar would match the conversation
    // presets on the Conversations screen just as well.
    this.granularityGroup = this.filterBar.getByRole("group", { name: "Chart granularity" });

    // The four Overview widgets are bare <Box id> wrappers (KpiRow, CostOverTime,
    // BreakdownWidgets) and one CustomTable id — none carries a role or an accessible name,
    // and each holds only numbers this suite must not pin, so the id is the only rung.
    this.kpiRow = page.locator("#cost-kpi-row");
    this.costOverTime = page.locator("#cost-over-time");
    this.breakdowns = page.locator("#cost-breakdowns");
    this.topConversationsTable = page.locator("#top-conversations-table");

    // ds/ToggleGroup again — role="group" with the ariaLabel as its name is the only rung it
    // offers, and a structural fallback would match the granularity toggle just as well.
    this.conversationPresets = page.getByRole("group", { name: "Filter conversations" });

    // A CustomTable id. Every screen renders a table, so role="table" is ambiguous page-wide
    // and a fallback on it could return another screen's table left mounted behind this one.
    this.conversationsRows = page.locator(`#${CONVERSATIONS_TABLE}-body ${DATA_ROW}`);

    // ConversationsTable takes CustomTable's default empty branch, which ids its heading
    // through EmptyData — so this is the other legitimate body when the tenant has no
    // conversations in the window, not a missing element.
    this.conversationsEmpty = page.locator(`#${CONVERSATIONS_TABLE}-no-data`);
  }

  // One tab on the inner strip. MUI Tab keeps role="tab" and aria-selected even with
  // component={Link} (CustomTabs' behavior='router' mode), so role scoped to the strip is
  // unambiguous; the id a11yProps derives from the tab value is the fallback.
  screenTab(screen: AnalyserScreen): Locator {
    return this.screenStrip
      .getByRole("tab", { name: SCREEN_LABEL[screen] })
      .or(this.page.locator(`#tab-${screen}`))
      .first();
  }

  // One option inside a ds/ToggleGroup. Each is a ButtonBase with role="radio" and the
  // option's visible label, so role is the only rung the component offers.
  //
  // exact: true is load-bearing, not tidiness. getByRole matches the accessible name as a
  // substring by default, and this group's own labels contain each other — "All" is inside
  // "Top 5 by cALLs", which resolves to two radios and fails on strict mode.
  toggleOption(group: Locator, label: string): Locator {
    return group.getByRole("radio", { name: label, exact: true });
  }

  // FilterDropdown renders its trigger as a <button> whose leading text is the label and
  // gives it `auto-complete-${toKebabCase(id)}`, so role leads and the id backs it up.
  // Scoped to the filter bar so a fallback cannot reach an identically labelled filter on
  // another Optimize tab.
  protected filterTrigger(id: string, label: string): Locator {
    return this.filterBar
      .getByRole("button", { name: new RegExp(`^${label}`) })
      .or(this.page.locator(`#auto-complete-${id}`))
      .first();
  }

  // Waits out the fetch. While `loading` is true CustomTable swaps in a skeleton tbody
  // that carries no id, so the id'd body being attached means the response has landed.
  async waitForTable(tableId: string, emptyState: Locator, timeout = 60000): Promise<void> {
    await this.page.locator(`#${tableId}-body`).or(emptyState).first().waitFor({ state: "attached", timeout });
  }
}

// Tab copy from tabOptions in CostAnalyser.tsx. Kept beside the locator that reads it so a
// renamed tab is one edit, not a hunt through the spec.
const SCREEN_LABEL: Record<AnalyserScreen, string> = {
  overview: "Overview",
  conversations: "Conversations",
  models: "Models",
  agents: "Agents",
  tools: "Tools",
  users: "Users",
  critiques: "Critiques",
};
