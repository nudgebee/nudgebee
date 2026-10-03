// Not for OSS
import { Page, Locator } from "@playwright/test";
import { AWSLocators } from "../AWSLocators";

// The four recommendation categories the account-level Optimize tab mounts, keyed by the
// sub-tab that selects them. Each value is the CATEGORY_CONFIG.tableId the category hands
// to ListingLayout and CustomTable (CloudOptimizeRecommendationsTable.tsx:80-131), so the
// four panels are four distinct DOM roots rather than one restyled panel.
export const OPTIMIZE_TABLE = {
  "right-sizing": "cloudaccount-optimize-rightsizing-change",
  configuration: "cloudaccount-optimize-configuration-change",
  security: "cloudaccount-optimize-security-change",
  "infra-upgrade": "cloudaccount-optimize-infra-upgrade",
} as const;

export type OptimizeCategoryTab = keyof typeof OPTIMIZE_TABLE;

// Recommendation Resolution is the fifth sub-tab and a different component
// (ListingRecommendationResolution.jsx:382), so it has a root of its own.
export const RESOLUTION_ROOT_ID = "recommendation-resolution-listing-layout";

// showSavings is true for RightSizing and InfraUpgrade only, and it is what decides
// whether the Estimated Savings stat and the Savings column exist at all.
export const CATEGORY_SHOWS_SAVINGS: Record<OptimizeCategoryTab, boolean> = {
  "right-sizing": true,
  configuration: false,
  security: false,
  "infra-upgrade": true,
};

// The declared column names, from the TABLE_COLUMNS ternary
// (CloudOptimizeRecommendationsTable.tsx:667-682). The trailing header is deliberately
// blank (the actions column), so it is not listed and never asserted.
export const SAVINGS_HEADERS = ["Severity", "Rule Name", "Instance", "Recommendation", "Savings"] as const;
export const NO_SAVINGS_HEADERS = ["Severity", "Rule Name", "Instance", "Recommendation"] as const;

// Zero-based data-cell indices. Identical for both column sets — Savings is appended
// after Recommendation, so it never shifts the four columns ahead of it.
export const OPTIMIZE_COLUMN = { severity: 0, ruleName: 1, instance: 2, recommendation: 3 } as const;

// The drilldown tabs every category declares (CloudOptimizeRecommendationsTable.tsx:791-847).
export const DRILLDOWN_TABS = ["Evidence", "Description", "Mitigation", "Audit History"] as const;

// The listing opens pre-filtered to Open + InProgress (CloudOptimizeRecommendationsTable
// .tsx:154-157), so an unfiltered landing is already a filtered listing.
//
// Only the FIRST of the two is visible on the trigger. ds/FilterDropdown defaults
// limitTag=1 (FilterDropdown.jsx:800) and renders `value.slice(0, limitTag)` as text with
// the remainder collapsed into a `+N` badge whose labels live in a tooltip
// (FilterDropdown.jsx:857-859, 1143-1170). CI run 33702249100 confirmed the rendered text
// is exactly `StatusOpen+1` — so asserting "In Progress" on the trigger would fail.
export const DEFAULT_STATUS_VISIBLE_LABEL = "Open";
export const DEFAULT_STATUS_OVERFLOW_BADGE = "+1";

// RECOMMENDATION_SERVERITY (api1/recommendation/index.ts:3) — a fixed list, not fetched
// per account, so these are the same five options on every environment.
export const SEVERITY_OPTIONS = ["Critical", "High", "Medium", "Low", "Info"] as const;

function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export class AwsOptimizeLocators extends AWSLocators {
  constructor(page: Page) {
    super(page);
  }

  // Panel root for one category — the ListingLayout Card that carries id={tableId}
  // (ListingLayout.tsx:205), which ds/Card renders as a Box, so a div.
  //
  // The `div` qualifier is load-bearing, not decoration. CloudOptimizeRecommendationsTable
  // passes the same tableId to BOTH ListingLayout and CustomTable (lines 731 and 784), and
  // CustomTable puts it on its <table> — so the bare id matches two elements and a strict
  // mode violation fails every test in this file. Proved by CI run 33702249100, where all
  // 9 failed with "resolved to 2 elements: <div class='MuiBox-root' id=...> and
  // <table aria-label='table' id=...>". The duplicate id is the app's, not this suite's.
  //
  // Deliberately id-only, with no .or() fallback. The component renders exactly one
  // data-testid, on the toolbar (verified: `grep -c data-testid
  // CloudOptimizeRecommendationsTable.tsx` returns 1), and the Card is a plain container
  // with no role, accessible name or unique text of its own — so rung 3 is the highest
  // rung available for it.
  //
  // A wider fallback would be actively unsafe rather than merely slower: these four roots
  // are what the sub-tab navigation test uses to prove one category replaced another, so a
  // structural match that also resolved against a sibling panel would report a switch that
  // never happened.
  categoryRoot(tab: OptimizeCategoryTab): Locator {
    return this.page.locator(`div#${OPTIMIZE_TABLE[tab]}`);
  }

  // Same id-only reasoning as categoryRoot above: ListingRecommendationResolution renders a
  // single data-testid, on its toolbar, and its ListingLayout Card has no role or text of
  // its own. This root is what proves the fifth sub-tab replaced the recommendations panel,
  // so a structural fallback that also matched a sibling listing would report a switch that
  // never happened.
  get resolutionRoot(): Locator {
    return this.page.locator(`#${RESOLUTION_ROOT_ID}`);
  }

  // ListingLayout.Toolbar spreads its extra props onto the rendered Box
  // (ListingLayout.tsx:136), so this testid is a real handle and the only rung-1 locator
  // this module offers. No .or() fallback: the toolbar carries no id, role or text of its
  // own, and the nearest structural match is the Card that also wraps the table body —
  // asserting "the filters render" against that would pass with no toolbar at all.
  toolbar(tab: OptimizeCategoryTab): Locator {
    return this.page.getByTestId(`${OPTIMIZE_TABLE[tab]}-filter-toolbar`);
  }

  // ds/FilterDropdown renders its trigger as <Box component='button' type='button'
  // id={`auto-complete-${toKebabCase(id)}`}> carrying its label as visible text
  // (FilterDropdown.jsx:1043-1050). It is a real button with an accessible name, so role
  // leads; the id backs it up and is unique because it encodes the table as well as the
  // filter. toKebabCase only lowercases and collapses whitespace/underscores, and these
  // ids contain neither, so they kebab to themselves.
  filterTrigger(tab: OptimizeCategoryTab, filter: string, label: string): Locator {
    const id = `${OPTIMIZE_TABLE[tab]}-filter-${filter}`;
    return this.toolbar(tab)
      .getByRole("button", { name: new RegExp(`^${escapeForRegex(label)}`) })
      .or(this.page.locator(`#auto-complete-${id}`))
      .first();
  }

  // shared/buttons/DownloadButton renders aria-label='Download' alongside its id
  // (DownloadButton.jsx:124-128), so role leads, scoped to this category's toolbar.
  downloadBtn(tab: OptimizeCategoryTab): Locator {
    return this.toolbar(tab)
      .getByRole("button", { name: "Download" })
      .or(this.page.locator(`#${OPTIMIZE_TABLE[tab]}-download`))
      .first();
  }

  // Data rows only.
  //
  // This table passes showExpandable + an expandable config, so CustomTable emits TWO <tr>
  // per record — the data row and the Collapse row beneath it (CustomTable.jsx:487-498).
  // A bare `tbody tr` count would therefore be double the record count. Filtering on the
  // expand toggle is what separates them: only the data row carries that cell
  // (CustomTable.jsx:458-485), and the toggle is an IconButton whose aria-label flips
  // between Expand row and Collapse row, so the filter must accept either.
  //
  // No .or() on the tbody id: every row-count baseline and every column assertion in this
  // area is taken from this locator, so a fallback that matched a second table on the page
  // would silently grade the wrong listing. The id is generated by CustomTable from the
  // table id it is given, so it changes only if that table id does.
  rows(tab: OptimizeCategoryTab): Locator {
    return this.page
      .locator(`#${OPTIMIZE_TABLE[tab]}-body tr`)
      .filter({ has: this.page.getByRole("button", { name: /^(Expand|Collapse) row$/ }) });
  }

  cell(tab: OptimizeCategoryTab, rowIndex: number, column: keyof typeof OPTIMIZE_COLUMN): Locator {
    return this.rows(tab).nth(rowIndex).locator("td").nth(OPTIMIZE_COLUMN[column]);
  }

  // One column's cell across every data row, in row order. nth-child rather than
  // .locator("td").nth(n), which would index into the whole table's cells instead of each
  // row's. OPTIMIZE_COLUMN is zero-based; nth-child is 1-based.
  columnCells(tab: OptimizeCategoryTab, column: keyof typeof OPTIMIZE_COLUMN): Locator {
    return this.rows(tab).locator(`td:nth-child(${OPTIMIZE_COLUMN[column] + 1})`);
  }

  // The row's expand toggle. aria-label is set by CustomTable itself
  // (CustomTable.jsx:466) and aria-expanded alongside it, which is what lets the drilldown
  // test wait on the panel's real state instead of on a sleep. No .or(): the toggle is
  // given no id, and it is already the only button in its cell, so the role match is exact.
  rowExpandToggle(tab: OptimizeCategoryTab, rowIndex: number): Locator {
    return this.rows(tab).nth(rowIndex).getByRole("button", { name: /^(Expand|Collapse) row$/ }).first();
  }

  // The severity chip. ds/SeverityIcon is called here with `level` and `aria-label` and
  // neither `label` nor `count`, so its composition is 'icon-only' and it renders
  // role='img' with aria-label=`Severity: <raw severity>`
  // (SeverityIcon.tsx:154-166, CloudOptimizeRecommendationsTable.tsx:461). The raw severity
  // is what the Severity filter's options carry, so the two are directly comparable.
  //
  // Role-only, and deliberately so: the chip carries no id or testid, and the only wider
  // match available is the severity cell's text, which is the timestamp beside the icon
  // rather than the level. A fallback onto that would return a date where the test reads a
  // severity, and the comparison would fail as a value mismatch instead of a missing chip.
  severityChip(tab: OptimizeCategoryTab, rowIndex: number): Locator {
    return this.rows(tab).nth(rowIndex).getByRole("img", { name: /^Severity: / }).first();
  }

  severityChips(tab: OptimizeCategoryTab): Locator {
    return this.rows(tab).getByRole("img", { name: /^Severity: / });
  }

  // The row's three-dot trigger. ds/Button puts the caller's aria-label on the button
  // (CloudOptimizeRecommendationsTable.tsx:542), so role leads. No id fallback: this
  // trigger is given none, and its sibling Ask-NuBi button's id embeds a per-row
  // recommendation uuid that the test has no way to know.
  rowMenuTrigger(tab: OptimizeCategoryTab, rowIndex: number): Locator {
    return this.rows(tab).nth(rowIndex).getByRole("button", { name: "More actions" }).first();
  }

  // ds/DropdownMenu renders items as role='menuitem' carrying the caller's item id, but
  // here that id embeds the recommendation's uuid (`optimize-action-<uuid>-<n>`), so it
  // cannot be matched literally — the numeric suffix is the stable half. getByRole with a
  // name is deliberately not used: it matches zero menu items in this app (nested spans
  // plus aria-hidden). :visible narrows to the one menu actually open.
  menuItem(label: RegExp, idSuffix: string): Locator {
    return this.page
      .locator('[role="menuitem"]:visible')
      .filter({ hasText: label })
      .or(this.page.locator(`[role="menuitem"][id$="${idSuffix}"]:visible`))
      .first();
  }

  get openMenuItems(): Locator {
    return this.page.locator('[role="menuitem"]:visible');
  }

  // Drilldown tabs inside an expanded row. common/navigation/Tabs renders MUI <Tab>
  // elements (Tabs.jsx:382), which carry role='tab' natively, and the strip renders only
  // when more than one tab is declared (CustomTable.jsx:310) — four are, for every
  // category. Scoped to the table body so it can never resolve against the page's own
  // anchor tabs.
  drilldownTab(tab: OptimizeCategoryTab, name: string): Locator {
    return this.page
      .locator(`#${OPTIMIZE_TABLE[tab]}-body`)
      .getByRole("tab", { name })
      .first();
  }

  // CustomTable's empty state is shared/EmptyData, not ds/EmptyState — an
  // <h2 id={`${id}-no-data`}> carrying CustomTable's default heading
  // (EmptyData.jsx:26, CustomTable.jsx:564,941).
  //
  // This doubles as the listing's settled signal: renderEmptyState returns null while
  // `loading` is true (CustomTable.jsx:918), so it appears only once a fetch has finished
  // and genuinely returned nothing.
  emptyState(tab: OptimizeCategoryTab): Locator {
    return this.page
      .locator(`#${OPTIMIZE_TABLE[tab]}-no-data`)
      .or(this.categoryRoot(tab).getByRole("heading", { name: "No Data Available" }))
      .first();
  }

  // ds/Stat renders its label as plain text inside the WidgetCard, with no id, role or
  // testid of its own — scoped text is the highest rung available (rung 4). Scoped to the
  // page rather than to the panel root because the two stat cards sit above ListingLayout,
  // outside it (CloudOptimizeRecommendationsTable.tsx:690-729). exact:true and no .or():
  // the tests assert Estimated Savings is *absent* on the non-savings categories, so a
  // looser match would report a card that is not there.
  stat(label: string): Locator {
    return this.page.getByText(label, { exact: true }).first();
  }

  // ── Shared ds/FilterDropdown panel ──────────────────────────────────────────

  // Options are role='option' boxes inside the open panel (FilterDropdown.jsx:199).
  // Scoped by :visible rather than by the popover's MUI class: only one panel is ever
  // open, and every other dropdown's options are unmounted.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  get visibleOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  // The panel's search box, which FilterDropdown renders only above eight options
  // (FilterDropdown.jsx:1228), so callers must treat its absence as an expected outcome
  // rather than a failure.
  get filterSearchInput(): Locator {
    return this.page.locator(".MuiPopover-root").getByPlaceholder("Search...").first();
  }
}
