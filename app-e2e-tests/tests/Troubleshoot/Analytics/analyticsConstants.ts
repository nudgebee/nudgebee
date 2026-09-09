// Not for OSS

// Troubleshoot > Analytics (app/src/components/troubleshoot/analytics/TroubleshootAnalytics.tsx),
// the fourth top-level tab of app/src/pages/troubleshoot/index.jsx.

export const TROUBLESHOOT_PATH = "/troubleshoot";
export const ANALYTICS_FRAGMENT = "analytics";

// AnchorComponent falls back to `anchor-tab-<name>` when a filterOptions entry carries no
// `id`, and none of the Troubleshoot top-level entries do. The name, not the fragment.
export const ANALYTICS_TAB_NAME = "Analytics";

// Where a drill-down lands: applyWidgetFilter pushes hash 'all-events/all' and switches to
// the flat Events sub-tab (app/src/pages/troubleshoot/index.jsx).
export const DRILLDOWN_FRAGMENT = "all-events/all";

// An unknown top-level fragment is canonicalised to the first tab plus its first sub-tab.
export const CANONICAL_FALLBACK_FRAGMENT = "all-events/fingerprint";

// The single GraphQL operation behind every number on the tab (kubernetes1/index.ts:2108).
export const ANALYTICS_OP = "TroubleshootAnalyticsAggregates";

// The tab pins seven days when the URL carries no range, and the volume chart needs at
// least four day-buckets before it will draw a trend.
export const DEFAULT_WINDOW_DAYS = 7;
export const MIN_TREND_DAYS = 4;
export const DAY_MS = 86400000;

// Overview scoreboard — the four ds/Stat labels, in render order.
export const OVERVIEW_TILES = [
  "Investigations finished",
  "Problems we explained",
  "Time to explain a problem",
  "Engineer time saved",
] as const;

// "Are we improving?" — the first two are clickable DeltaStats, the third has no previous
// window to compare against so it renders as a plain tile.
export const DISTINCT_PROBLEMS_TILE = "Distinct problems";
export const URGENT_PROBLEMS_TILE = "Problems we rated urgent";
export const RECURRENCE_TILE = "Problems we have seen before";

// Section headings. Rendered uppercase by CSS text-transform only, so the DOM keeps this case.
export const OVERVIEW_HEADING = "Overview";
export const IMPROVING_HEADING = "Are we improving?";
export const RECURRING_HEADING = "What keeps coming back?";
export const VOLUME_HEADING = "Issue volume";

export const VOLUME_PANEL_TITLE = "New issues per day";
export const RECURRING_PANEL_TITLE = "Issues that will fire again unless something changes";

// Both halves of every branch this tab can render, so an assertion can accept the shape the
// dev tenant actually produces instead of the one the author happened to see.
export const NO_RECURRENCE_COPY = "Nothing in this window has fired more than once";
export const NOT_ENOUGH_HISTORY_COPY = "not enough to show a trend";
export const SEVEN_DAY_SWITCH_COPY = "Switch to the last 7 days";
export const ERROR_BANNER_TITLE = "Analytics unavailable";

export const CHART_ID = "analytics-issue-volume";
export const ACCOUNT_FILTER_ID = "auto-complete-briefing-filter-account";

export const SPEC_TIMEOUT_MS = 180000;
