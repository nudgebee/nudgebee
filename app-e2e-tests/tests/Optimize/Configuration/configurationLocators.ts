// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeModuleLocators } from "../optimizeModuleLocators";

// Configuration sub-tab of the tenant-level Optimize module (/optimise#configuration).
//
// The tab is OptimizeNewPage.tsx mounted with lockedCategory='Configuration', so it
// shares that page's toolbar, chip rows and detail panel with the Cost tab — every one
// of those locators is inherited from OptimizeModuleLocators rather than re-declared.
// What is unique to this tab is the body: while no rule filter and no search are set,
// ListingLayout.Body renders ConfigRuleRollup (one row per check) instead of the flat
// recommendations table, and each rollup row expands into a ConfigRuleFindings table of
// the resources failing that check.
//
// Rung choices below were measured, not assumed:
//   ConfigRuleRollup.tsx   data-testid=0    ConfigRuleFindings.tsx data-testid=0
//   RowActions.tsx         data-testid=0    OptimizeNewPage.tsx    data-testid=6
//   RecommendationDetailPanel.tsx data-testid=1
// So rung 1 exists only for the page root, the chip rows and the detail panel; the
// rollup and its findings are reached by the CustomTable ids they pass, and the row
// controls by their accessible names.

// CustomTable id passed by ConfigRuleRollup.tsx.
export const CONFIG_RULES_TABLE = "optimise-config-rules";

// ConfigRuleFindings.tsx builds its id as `optimise-config-findings-${ruleName}`, and
// the rule name is whatever the tenant's scan produced — so the prefix is the only part
// that is static. Matched as an attribute prefix rather than a `#` selector because a
// rule name carrying a dot would be parsed as a class chain.
export const FINDINGS_TABLE_PREFIX = "optimise-config-findings-";

// Column contract from HEADERS in ConfigRuleRollup.tsx.
export const ROLLUP_HEADERS = ["Severity", "Check", "Accounts", "Findings"] as const;

// Column contract from HEADERS in ConfigRuleFindings.tsx. The fifth is the row-actions
// column and is deliberately unnamed.
export const FINDINGS_HEADERS = ["Severity", "Resource", "Account", "Last Seen"] as const;

// An empty table here renders "All good here!", NOT the copy either component asks for.
// CustomTable.jsx renderEmptyState: the default branch is `EmptyData id={id}`, whose
// heading carries `${id}-no-data`; the showUpdatedEmptyData branch renders a fixed
// EmptyData and forwards NO id, ignoring emptyHeading/emptySubHeading entirely. Both
// tables on this tab pass showUpdatedEmptyData, so ConfigRuleRollup's 'No configuration
// findings' and ConfigRuleFindings' 'No findings' never reach the DOM and neither table
// has a `-no-data` anchor. The id form is kept as the primary only so these locators
// self-heal if that branch ever starts forwarding one.
export const NO_DATA_HEADING = "All good here!";

export class ConfigurationLocators extends OptimizeModuleLocators {
  readonly configRulesTable: Locator;
  readonly configRulesRows: Locator;
  readonly configRulesEmpty: Locator;
  readonly summaryCardAll: Locator;
  readonly detailPanel: Locator;
  readonly detailPanelClose: Locator;

  constructor(page: Page) {
    super(page);

    // CustomTable puts the id on the <table> itself and `${id}-body` on the tbody.
    // Id-only: the table carries aria-label="table", which every other CustomTable on
    // the page carries too, so a role fallback would resolve in document order and
    // could return the findings table nested inside an expanded row.
    this.configRulesTable = page.locator(`#${CONFIG_RULES_TABLE}`);
    this.configRulesRows = this.rowsFor(CONFIG_RULES_TABLE);

    // See NO_DATA_HEADING: the id anchor does not exist on this branch, so the heading
    // is what actually resolves. Scoped to the listing because the findings drawer's
    // empty state renders the identical copy.
    this.configRulesEmpty = page
      .locator(`#${CONFIG_RULES_TABLE}-no-data`)
      .or(this.recommendationsListing.getByRole("heading", { name: NO_DATA_HEADING, exact: true }))
      .first();

    // Rendered only when lockedCategory is unset, so on this tab its absence is the
    // assertion. Kept as a testid because OptimizeNewPage.tsx declares one for it.
    this.summaryCardAll = page.getByTestId("optimize-card-all");

    this.detailPanel = page.getByTestId("recommendation-detail-panel");

    // The close button carries aria-label="Close", so role leads; scoped to the panel
    // because "Close" also names the buttons on any modal the page can open.
    this.detailPanelClose = this.detailPanel
      .getByRole("button", { name: "Close" })
      .or(page.locator("#detail-panel-close"))
      .first();
  }

  // The chevron CustomTable renders in each expandable row's trailing cell. Its
  // aria-label flips between Expand row and Collapse row, and aria-expanded carries the
  // state — which is what the tests wait on instead of a sleep.
  expandToggle(row: Locator): Locator {
    return row.getByRole("button", { name: /^(Expand|Collapse) row$/ }).first();
  }

  // The overflow trigger inside a findings row (RowActions.tsx). Id is
  // `action-menu-${rec.id}`, a runtime value, so the accessible name is the only
  // static handle and the id form cannot be written as a fallback.
  rowActionsMenu(row: Locator): Locator {
    return row.getByRole("button", { name: "More actions" }).first();
  }

  // A rollup row's collapsed drill-down lives in the <tr> immediately after it, so the
  // findings table for the row under test is reached through that sibling rather than
  // page-wide. `xpath=following-sibling::tr[1]` is a structural step from a located
  // anchor, not an index chain into the document.
  drilldownOf(row: Locator): Locator {
    return row.locator("xpath=following-sibling::tr[1]");
  }

  // The findings table, its rows and its empty state, each scoped to the drilldown of the
  // check under test rather than to the page. ConfigRuleFindings builds its id from the
  // rule name at runtime, so the prefix is all that is static — and a page-wide prefix
  // match would resolve to whichever check happened to be expanded first.
  findingsTableIn(row: Locator): Locator {
    return this.drilldownOf(row).locator(`[id^="${FINDINGS_TABLE_PREFIX}"]`).first();
  }

  // While the fetch is in flight CustomTable swaps in a skeleton tbody that carries no
  // id, so scoping to the id'd body is what keeps a skeleton row out of this count.
  findingsRowsIn(row: Locator): Locator {
    return this.drilldownOf(row).locator(`[id^="${FINDINGS_TABLE_PREFIX}"][id$="-body"] tr:has(td:nth-child(2))`);
  }

  findingsEmptyIn(row: Locator): Locator {
    return this.drilldownOf(row).getByRole("heading", { name: NO_DATA_HEADING, exact: true }).first();
  }
}
