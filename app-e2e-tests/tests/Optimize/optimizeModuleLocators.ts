// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeLocators } from "./OptimizeLocators";

// Tenant-level Optimize module (app/src/pages/optimise/index.jsx). The tab strip is
// AnchorComponent; each tab's body is a lazily imported view:
//   Summary         -> components/optimise-new/summary/SummaryView.tsx
//   Recommendations -> components/optimise-new/OptimizeNewPage.tsx
//   Resolutions     -> components/optimise-new/ResolutionsView.tsx
//   Security        -> components/optimise-new/SecurityView.tsx
//
// Rung choices below follow the qa-automation-code-check ladder against what each of
// those files actually renders, measured rather than assumed:
//   AnchorComponent.jsx  data-testid=0   SummaryView.tsx    data-testid=0
//   OptimizeNewPage.tsx  data-testid=6   ResolutionsView.tsx data-testid=4
//   SecurityView.tsx     data-testid=3   FilterDropdown.jsx  data-testid=0
// So the Recommendations chip rows, the Resolutions toolbar and the Security states
// are reached by testid; everything the app leaves untestid'd falls to its id.

// CustomTable renders `${id}` on the table and `${id}-body` on the tbody. A table that
// passes showExpandable emits a second <tr> per data row to hold the collapsed
// drill-down — it is in the DOM open or closed and carries a single colSpan cell, so
// requiring a second cell is what stops every count doubling. Neither table below
// passes it any more (Resolutions moved to a row-click side panel), but the guard
// stays: it costs nothing and the next expandable table would silently double.
const DATA_ROW = "tr:has(td:nth-child(2))";

export const RECOMMENDATIONS_TABLE = "optimize-recommendations-table";
export const RESOLUTIONS_TABLE = "optimise-resolutions";

// SEVERITY_ORDER and SAFETY_ORDER in OptimizeNewPage.tsx — the chip rows render one
// chip per entry, testid'd with the lowercased value.
export const SEVERITY_BANDS = ["critical", "high", "medium", "low", "info"] as const;
export const SAFETY_BANDS = ["safe", "review", "risky", "unknown"] as const;

export class OptimizeModuleLocators extends OptimizeLocators {
  // Tab strip.
  readonly securityTab: Locator;

  // Summary tab.
  readonly summarySavingsCard: Locator;
  readonly summaryCategoryFacet: Locator;
  readonly summaryProviderFacet: Locator;
  readonly summaryAccountFilter: Locator;

  // Recommendations tab.
  readonly recommendationsRoot: Locator;
  readonly recommendationsListing: Locator;
  readonly recommendationsSearch: Locator;
  readonly recommendationsAccountFilter: Locator;
  readonly recommendationsRulesFilter: Locator;
  readonly recommendationsSavingsFilter: Locator;
  readonly recommendationsLastSeenFilter: Locator;
  readonly recommendationsStatusFilter: Locator;
  readonly recommendationsClearFilters: Locator;
  readonly recommendationsSortTrigger: Locator;
  readonly recommendationsDownload: Locator;
  readonly severityBar: Locator;
  readonly recommendationsTable: Locator;
  readonly recommendationsRows: Locator;
  readonly recommendationsNoMatchText: Locator;
  readonly recommendationsNoDataText: Locator;

  // Resolutions tab.
  readonly resolutionsToolbar: Locator;
  readonly resolutionsListing: Locator;
  readonly resolutionsAccountFilter: Locator;
  readonly resolutionsSeverityFilter: Locator;
  readonly resolutionsRecommendationFilter: Locator;
  readonly resolutionsResolverFilter: Locator;
  readonly resolutionsTable: Locator;
  readonly resolutionsRows: Locator;
  readonly resolutionsEmpty: Locator;

  // Security tab.
  readonly securityView: Locator;
  readonly securityEmpty: Locator;
  readonly securityLoading: Locator;
  readonly securityAccountFilter: Locator;

  constructor(page: Page) {
    super(page);

    // The strip's tabs are `Button component={Link}` with a visible name, so getByRole
    // is the higher rung — but "Summary", "Security" and "Recommendations" all appear
    // again in the sidebar flyout and in page headings, and `.or()` resolves in
    // document order, so a page-wide role fallback could return the sidebar link and
    // navigate somewhere else entirely. The id is the highest UNAMBIGUOUS rung here.
    // AnchorComponent.jsx renders no data-testid at all (verified: grep -c = 0).
    this.securityTab = page.locator("#anchor-tab-security");

    // SummaryView.tsx renders no data-testid, and its controls have no unique
    // accessible name — "Account" names the Recommendations and Resolutions account
    // filters too. Id-only, for the same document-order reason as the strip above.
    this.summarySavingsCard = page.locator("#summary-savings-card");
    this.summaryCategoryFacet = page.locator("#summary-filter-category");
    this.summaryProviderFacet = page.locator("#summary-filter-provider");
    this.summaryAccountFilter = page.locator("#auto-complete-account-filter-select");

    this.recommendationsRoot = page.getByTestId("optimize-new-page");
    // OptimizeNewPage gives ListingLayout an id and no data-testid, and the wrapper it
    // renders carries neither a role nor a name — so the id is the only rung that
    // reaches it, and a text or CSS fallback would match the page body just as well.
    // Every control below is scoped to this root so its fallback cannot escape it.
    this.recommendationsListing = page.locator("#optimize-recommendations");

    // ds/SearchInput forwards the id to the <input> itself and passes `label` through
    // as the placeholder, so the id (rung 3) outranks the placeholder (rung 4) here.
    this.recommendationsSearch = page
      .locator("#optimize-search")
      .or(this.recommendationsListing.getByPlaceholder(/Search resource/))
      .first();

    // FilterDropdown renders its trigger as `auto-complete-${toKebabCase(id)}` on a
    // <button> whose text starts with the label — so the role fallback is real, and
    // scoping it to the listing keeps it off the other tabs' identically named filters.
    this.recommendationsAccountFilter = this.filterTrigger("optimize-account-filter", "Account", this.recommendationsListing);
    this.recommendationsRulesFilter = this.filterTrigger("optimize-rules-filter", "Rules", this.recommendationsListing);
    this.recommendationsSavingsFilter = this.filterTrigger("optimize-savings-filter", "Savings", this.recommendationsListing);
    this.recommendationsLastSeenFilter = this.filterTrigger("optimize-last-seen-filter", "Last seen", this.recommendationsListing);
    this.recommendationsStatusFilter = this.filterTrigger("optimize-status-filter", "Status", this.recommendationsListing);

    // These three carry a real accessible name — visible text on Clear all, an
    // aria-label on the other two — so role leads and the id backs it up. Clear all is
    // rendered only while `hasActiveFilter` is true, so its absence is the assertion
    // that every filter is cleared, not a broken locator.
    this.recommendationsClearFilters = this.recommendationsListing
      .getByRole("button", { name: "Clear all" })
      .or(page.locator("#optimize-clear-filters"))
      .first();

    this.recommendationsSortTrigger = this.recommendationsListing
      .getByRole("button", { name: "Sort recommendations" })
      .or(page.locator("#optimize-sort-trigger"))
      .first();

    this.recommendationsDownload = this.recommendationsListing
      .getByRole("button", { name: "Download recommendations as CSV" })
      .or(page.locator("#optimize-download"))
      .first();

    this.severityBar = page.getByTestId("severity-summary-bar");
    this.recommendationsTable = page.locator(`#${RECOMMENDATIONS_TABLE}`);
    this.recommendationsRows = page.locator(`#${RECOMMENDATIONS_TABLE}-body ${DATA_ROW}`);

    // CustomTable's showEmptyStateText branch renders bare Typography — no id, no
    // heading role — so the copy from OptimizeNewPage's emptyStateText is the handle.
    // Which of the two strings shows depends on whether a filter is active.
    this.recommendationsNoMatchText = this.recommendationsListing.getByText(/No recommendations match these filters/);
    this.recommendationsNoDataText = this.recommendationsListing.getByText(/No active recommendations/);

    this.resolutionsToolbar = page.getByTestId("resolutions-filter-toolbar");
    this.resolutionsListing = page.locator(`#${RESOLUTIONS_TABLE}-listing-layout`);
    this.resolutionsAccountFilter = this.filterTrigger("resolutions-filter-account", "Account", this.resolutionsListing);
    this.resolutionsSeverityFilter = this.filterTrigger("resolutions-filter-severity", "Severity", this.resolutionsListing);
    this.resolutionsRecommendationFilter = this.filterTrigger("resolutions-filter-recommendation", "Type", this.resolutionsListing);
    this.resolutionsResolverFilter = this.filterTrigger("resolutions-filter-resolver", "Resolver", this.resolutionsListing);
    this.resolutionsTable = page.locator(`#${RESOLUTIONS_TABLE}`);
    this.resolutionsRows = page.locator(`#${RESOLUTIONS_TABLE}-body ${DATA_ROW}`);
    // This table takes CustomTable's default empty branch, which DOES id its heading.
    this.resolutionsEmpty = page.locator(`#${RESOLUTIONS_TABLE}-no-data`);

    this.securityView = page.getByTestId("optimise-security-view");
    this.securityEmpty = page.getByTestId("optimise-security-empty");
    this.securityLoading = page.getByTestId("optimise-security-loading");
    // SecurityView hands this one FilterDropdown to whichever sub-view is open, which
    // renders it inside its own toolbar — so it exists only on the populated branch.
    this.securityAccountFilter = this.filterTrigger("optimise-security-filter-account", "Account", this.securityView);
  }

  // FilterDropdown renders its trigger as a <button> whose leading text is the label,
  // so getByRole is the higher rung and scoping it to the tab's own listing keeps it
  // off the identically named filters on the other tabs. The id it also renders,
  // `auto-complete-${toKebabCase(id)}`, is the fallback.
  protected filterTrigger(id: string, label: string, scope: Locator): Locator {
    return scope
      .getByRole("button", { name: new RegExp(`^${label}`) })
      .or(this.page.locator(`#auto-complete-${id}`))
      .first();
  }

  // A table either has rows or renders an empty state in their place, and which one
  // depends on what the shared dev tenant holds rather than on the code under test.
  // Every list assertion goes through these so a legitimately empty tenant reads as a
  // pass instead of a locator that "never appeared".
  rowsFor(tableId: string): Locator {
    return this.page.locator(`#${tableId}-body ${DATA_ROW}`);
  }

  // Waits out the fetch. While `loading` is true CustomTable swaps in a skeleton tbody
  // that carries no id, so the id'd body being attached means the response has landed
  // and the rows on screen are the current ones.
  async waitForTable(tableId: string, emptyState: Locator, timeout = 60000): Promise<void> {
    await this.page.locator(`#${tableId}-body`).or(emptyState).first().waitFor({ state: "attached", timeout });
  }

  // Only for a baseline taken before any interaction. After an action that refetches,
  // assert the end state with a retrying expect(...).toHaveCount(n) instead — a plain
  // count races the in-flight request and reads the stale table.
  async rowCount(tableId: string, emptyState: Locator): Promise<number> {
    await this.waitForTable(tableId, emptyState);
    return this.rowsFor(tableId).count();
  }

  // SearchInput commits on Enter (onEnterPress in ds/SearchInput.jsx) — typing alone
  // filters nothing and leaves the URL untouched.
  async searchAndApply(input: Locator, term: string): Promise<void> {
    await input.click();
    await input.fill(term);
    await input.press("Enter");
  }
}
