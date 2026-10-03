// Not for OSS

// Event Resolutions — app/src/components/troubleshoot/EventResolutions.jsx, mounted as the
// third top-level pane of /troubleshoot (pages/troubleshoot/index.jsx:326, `selectedTab === 2`).
// It is NOT the Optimize listing at /optimise#resolutions: that one is
// components/optimise-new/ResolutionsView.tsx and is covered by tests/Optimize/Resolutions.
// This pane lists event_resolution rows — what was done about an event — and is read-only:
// there is no create, edit or delete control anywhere on it.
//
// The tab is not permission-gated. pages/troubleshoot/index.jsx:244 gates only Investigations
// (value 1) and Knowledge Graph (value 3), so a deep link to this fragment always lands here
// rather than bouncing back to All Events.

export const TROUBLESHOOT_PATH = "/troubleshoot";
export const EVENT_RESOLUTIONS_FRAGMENT = "event-resolutions";
// The neighbouring pane the navigation case bounces through. A pill click lands on the bare
// top-level hash and STAYS there: the page rewrites a hash to `<parent>/<first sub-tab>` only
// when a sub-fragment was present but unrecognized — `if (!!subFragment && ...)` at
// index.jsx:263 — so with no sub-fragment at all the sub-tab state resets to 0 while the URL is
// left untouched. Proved by CI run 532, which held `#all-events` across 62 polls over 30s.
export const ALL_EVENTS_FRAGMENT = "all-events";

// Ids the module itself renders. Every one was read off the component, not guessed.
// ListingLayout puts the caller's id on a ds/Card (EventResolutions.jsx:360).
export const LISTING_ID = "event-resolutions";
// CustomTable puts the caller's id on the <table> and `${id}-body` on the loaded <tbody>,
// swapping in an id-less skeleton body while loading (CustomTable.jsx:859, :1112).
export const TABLE_ID = "eventResolutionsTable";
// CustomTable's default empty branch renders EmptyData with id={id} (CustomTable.jsx:941), and
// EmptyData ids only its <h2>, as `${id}-no-data` (EmptyData.jsx:26).
export const EMPTY_STATE_ID = `${TABLE_ID}-no-data`;
export const EMPTY_STATE_HEADING = "No Data Available";
export const DOWNLOAD_BUTTON_ID = "event-resolutions-download";
// DownloadButton passes this straight to ds/Button as aria-label (DownloadButton.jsx:128).
export const DOWNLOAD_BUTTON_LABEL = "Download";

// The toolbar ids the module composes as `filter-${label.toLowerCase()}` (EventResolutions.jsx:371).
// ds/FilterDropdown then renders its trigger as <button id={`auto-complete-${toKebabCase(id)}`}>
// (FilterDropdown.jsx:1043-1050); toKebabCase only lowercases and joins on whitespace/underscore
// (app/src/utils/common.ts:861), so an already-kebab id passes through unchanged.
export const ACCOUNT_FILTER_ID = "filter-account";
export const STATUS_FILTER_ID = "filter-status";
export const TYPE_FILTER_ID = "filter-type";
export const RESOLVER_FILTER_ID = "filter-resolver";
export const ACCOUNT_FILTER_LABEL = "Account";
export const STATUS_FILTER_LABEL = "Status";
export const TYPE_FILTER_LABEL = "Type";
export const RESOLVER_FILTER_LABEL = "Resolver";

// The query parameter the Account filter writes through applyFiltersOnRouter
// (EventResolutions.jsx:310), and reads back on mount (:32-40). It is the module's only
// filter that survives a reload — the other three live in component state only.
export const ACCOUNT_QUERY_PARAM = "accountIds";

// Column contract from the `headers` array (EventResolutions.jsx:386-394). 1-based, which is
// what td:nth-child takes.
export const SUBJECT_COLUMN = 1;
export const SOURCE_COLUMN = 2;
export const RESOLUTION_COLUMN = 4;
export const STATUS_COLUMN = 6;
export const RESOLVER_COLUMN = 7;

export const COLUMN_HEADINGS = [
  "Subject",
  "Source",
  "Severity",
  "Resolution",
  "Resolution Details",
  "Status",
  "Resolver",
  "Updated",
] as const;

// The four Status options (EventResolutions.jsx:323). The Status cell prints `item.status`
// verbatim (:254), so an option label and the cell text it filters to are the same string.
export const STATUS_OPTIONS = ["Success", "Failed", "InProgress", "Configuring"] as const;

// The five Type options (EventResolutions.jsx:336) — every value the event_resolution CHECK
// constraint allows. Their labels go through snakeToTitleCase, which splits on underscores
// only, so a CamelCase value passes through UNCHANGED and the option reads "PullRequest".
// The Resolution cell renders the same value spaced at each lower->upper boundary (:174), so
// it reads "Pull Request" for the row the "PullRequest" option selects. The two spellings of
// one value are the app's own inconsistency; both are pinned here so the filter cases can
// assert the cell text a chosen option is expected to produce.
export const TYPE_OPTIONS = ["PullRequest", "Ticket", "DeploymentChange", "WorkflowExecution", "CommandExecution"] as const;

// The four Resolver options (EventResolutions.jsx:349). The Resolver cell runs the value
// through the same snakeToTitleCase (:271), so option label and cell text match exactly.
export const RESOLVER_OPTIONS = ["AutoPilot", "Manual", "System", "User"] as const;

// EventResolutions.jsx:174 — `item.type.replace(/([a-z])([A-Z])/g, '$1 $2')`.
export function typeCellText(option: string): string {
  return option.replace(/([a-z])([A-Z])/g, "$1 $2");
}

// The inverse, for reading a rendered Resolution cell back to the option that selects it.
export function typeOptionFromCell(cellText: string): string {
  return cellText.replace(/\s+/g, "");
}

// CustomTablePagination.jsx:65-76 — "Showing 1-10 of 1,234 results", or "No results found" at
// zero rows. The pagination block is withheld entirely while loading or with no rows
// (CustomTable.jsx:969), so "No results found" is unreachable from this table and the range
// form is the only one a settled, populated listing produces.
export const PAGINATION_SUMMARY_PATTERN = /Showing\s+([\d,]+)-([\d,]+)\s+of\s+([\d,]+)\s+results/;

// MUI PaginationItem's default getAriaLabel for the next-page control.
export const NEXT_PAGE_LABEL = "Go to next page";

// A syntactically valid uuid that cannot name a real cloud account, so the empty-state case is
// about the account scoping working rather than about what the shared dev tenant happens to
// hold. Suffixed per run because this string reaches the URL and the backend query.
export function noMatchAccountId(): string {
  const suffix = `${Date.now().toString(16)}${Math.random().toString(16).slice(2)}`.padEnd(12, "0").slice(0, 12);
  return `00000000-0000-4000-8000-${suffix}`;
}
