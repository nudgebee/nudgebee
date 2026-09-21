// Not for OSS
import { Page, Locator } from "@playwright/test";
import { TroubleshootLocators } from "../TroubleshootLocators";

// Troubleshoot > Knowledge Graph (app/src/components/knowledge-graph/KnowledgeGraph.jsx).
//
// The module renders 11 `data-testid`s of its own, so every control it owns is reached
// at rung 1. The filter dropdowns are `@ui/FilterDropdown` instances, which render no
// testid at all (`grep -c data-testid app/src/components/common/ds/FilterDropdown.jsx`
// returns 0) — see the note on filterAccount below for why those take an id primary.
export const KG_RELATIONSHIP_LABELS = ["CALLS", "ROUTES_THROUGH", "RUNS_ON", "MOUNTS"] as const;

// Traversal depths, static in the module (KnowledgeGraph.jsx:112-114).
export const KG_LEVEL_OPTIONS = ["1 - Direct neighbors", "2 - 2 hops", "3 - 3 hops"] as const;

// FilterDropdown builds its trigger id as `auto-complete-${toKebabCase(id)}` from the `id`
// prop the caller passes (FilterDropdown.jsx:1035,1042).
const triggerSelector = (filterId: string) => `[id="auto-complete-${filterId}"]`;

export class KnowledgeGraphLocators extends TroubleshootLocators {
  // Panels. WidgetCard renders neither a testid nor a role, and the ids are the only
  // handles the component offers — deliberate rung 3, no fallback worth having.
  readonly filterPanel: Locator;
  readonly canvas: Locator;
  // The graph column wraps the canvas plus the history/toolbar rows and carries no id of
  // its own, so it is reached as the canvas's parent — a labelled anchor, not an index chain.
  readonly graphPane: Locator;

  readonly collapseFiltersBtn: Locator;
  readonly expandFiltersBtn: Locator;
  readonly applyFiltersBtn: Locator;
  readonly clearAllBtn: Locator;
  readonly backBtn: Locator;
  readonly forwardBtn: Locator;
  readonly relationshipsBtn: Locator;

  // Filter dropdown triggers. Their accessible name is the label plus the current
  // selection chips, so it mutates the moment a filter is applied — and "Node" is a
  // prefix of "Node Type", so role+name cannot separate those two at all. The id is the
  // half that stays still, so it is the primary here; the role fallback is anchored and
  // scoped to the filter panel.
  readonly filterAccount: Locator;
  readonly filterNodeType: Locator;
  readonly filterNode: Locator;
  readonly filterLevel: Locator;

  // Canvas toolbar search (FilterDropdown id='kg-node-search').
  readonly nodeSearch: Locator;

  // The open Level panel, anchored to its own trigger — see panelFor(). Level is the only
  // dropdown this suite drives; see the note on panelFor for why the other three are not.
  readonly levelPanel: Locator;
  readonly levelOptions: Locator;
  readonly levelTwoHopsOption: Locator;

  // Relationships legend — a MUI Tooltip, so it lands in the portalled tooltip role.
  readonly legendTooltip: Locator;

  // Canvas states — exactly one of these renders once the graph fetch settles
  // (KnowledgeGraph.jsx:2650-2870).
  readonly graphSurface: Locator;
  readonly emptyPopulating: Locator;
  readonly emptyFiltered: Locator;
  readonly emptyFilteredClearBtn: Locator;
  readonly limitExceeded: Locator;
  readonly canvasSettled: Locator;

  constructor(page: Page) {
    super(page);

    // Id-only by design: both are WidgetCard/Box wrappers with no testid, no role and no
    // accessible name, so any wider match would resolve to some other panel on the page.
    this.filterPanel = page.locator("#kg-filter-panel");
    this.canvas = page.locator("#kg-canvas");
    this.graphPane = this.canvas.locator("xpath=..");

    this.collapseFiltersBtn = page
      .getByTestId("kg-collapse-filters-btn")
      .or(this.filterPanel.getByRole("button", { name: "Hide filters" }))
      .first();
    this.expandFiltersBtn = page.getByTestId("kg-expand-filters-btn").or(page.getByRole("button", { name: "Show filters" })).first();
    this.applyFiltersBtn = page
      .getByTestId("kg-apply-filters-btn")
      .or(this.filterPanel.getByRole("button", { name: "Apply Filters" }))
      .first();
    // Scoped to the filter panel on purpose: an open dropdown panel renders its own
    // "Clear All" control, and an unscoped fallback would resolve to whichever comes
    // first in document order.
    this.clearAllBtn = page.getByTestId("kg-clear-filters-btn").or(this.filterPanel.getByRole("button", { name: "Clear All" })).first();
    this.backBtn = page.getByTestId("kg-back-btn").or(this.graphPane.getByRole("button", { name: "Back" })).first();
    this.forwardBtn = page.getByTestId("kg-forward-btn").or(this.graphPane.getByRole("button", { name: "Forward" })).first();
    // No testid on this one, but it carries a stable visible label, so role wins over its id.
    this.relationshipsBtn = this.graphPane
      .getByRole("button", { name: "Relationships" })
      .or(page.locator("#relationship-types-btn"))
      .first();

    this.filterAccount = page.locator(triggerSelector("kg-filter-account")).or(this.filterPanel.getByRole("button", { name: /^Account/ })).first();
    this.filterNodeType = page
      .locator(triggerSelector("kg-filter-node-type"))
      .or(this.filterPanel.getByRole("button", { name: /^Node Type/ }))
      .first();
    this.filterNode = page.locator(triggerSelector("kg-filter-node")).or(this.filterPanel.getByRole("button", { name: /^Node(?! Type)/ })).first();
    this.filterLevel = page.locator(triggerSelector("kg-filter-level")).or(this.filterPanel.getByRole("button", { name: /^Level/ })).first();
    this.nodeSearch = page.locator(triggerSelector("kg-node-search")).or(this.graphPane.getByRole("button", { name: /Search nodes/ })).first();

    this.levelPanel = this.panelFor("kg-filter-level");
    // `[role="option"]` as CSS, not getByRole("option"): OptionItem sets the attribute
    // (FilterDropdown.jsx:196) and Playwright's own aria snapshot lists the rows, but its role
    // engine resolves them to 0 at runtime — run 33030502497 timed out on `getByRole('option')`
    // ("64 × locator resolved to 0 elements") while, in that same run, the two tests reaching a
    // row through the `.or()` text fallback below both passed. The attribute selector matches
    // regardless, so it leads here and the text form stays as the fallback.
    this.levelOptions = this.levelPanel.locator('[role="option"]');
    this.levelTwoHopsOption = this.levelOptions
      .filter({ hasText: "2 - 2 hops" })
      .or(this.levelPanel.getByText("2 - 2 hops"))
      .first();

    this.legendTooltip = page.getByRole("tooltip");

    this.graphSurface = this.canvas.locator(".react-flow");
    this.emptyPopulating = page.getByTestId("kg-empty-populating");
    this.emptyFiltered = page.getByTestId("kg-empty-filtered");
    this.emptyFilteredClearBtn = page.getByTestId("kg-empty-filtered-clear-btn");
    this.limitExceeded = this.canvas.getByText("Graph Too Large to Render");
    this.canvasSettled = this.graphSurface.or(this.emptyPopulating).or(this.emptyFiltered).or(this.limitExceeded).first();
  }

  // FilterDropdown passes `disablePortal` (default true, FilterDropdown.jsx:784), so an open
  // panel mounts inline under its trigger's wrapper rather than in a body-level portal.
  // Anchoring each panel to its own trigger is therefore exact, and it is what makes the
  // Level dropdown reachable.
  //
  // It does NOT make Account / Node Type / Node reachable. On those three the panel resolves
  // and reports visible, but `getByRole("option")` inside it finds nothing — run 33029674259
  // failed with "element(s) not found" on this exact chain while the Level panel, driven
  // through the same code, passed in the same run. Level renders 3 static options; the other
  // three are populated from the tenant graph (5,480 nodes on dev) and cross FilterDropdown's
  // 200-option virtualization threshold. Those three are out of scope for this suite until
  // the component gives their rows a stable handle — see the PR's Follow-ups.
  private panelFor(filterId: string): Locator {
    return this.page.locator(triggerSelector(filterId)).locator("xpath=..").locator(".MuiPopover-paper");
  }
}
