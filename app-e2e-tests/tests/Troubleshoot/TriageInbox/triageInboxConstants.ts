// Not for OSS

// Triage Inbox is the DEFAULT sub-tab of /troubleshoot#all-events, rendered by
// KubernetesGroupedEventsTable with groupEventType='fingerprint' (see
// app/src/pages/troubleshoot/index.jsx:287).

// The eight columns the fingerprint variant declares, in render order
// (KubernetesGroupedEventsTable.tsx:745-781). The Action column is the only one
// CustomTable's column selector cannot hide, so all eight are asserted by name
// rather than by position.
export const INBOX_COLUMNS = [
  "Severity",
  "Application",
  "Event Type",
  "Count",
  "Triage Score",
  "Triage Status",
  "Alert Status",
  "Action",
] as const;

// ListingLayout id and CustomTable id, both carrying a literal space. They are
// used through [id="..."] rather than "#...", because a CSS id selector breaks on
// the space and would silently match nothing.
export const INBOX_LISTING_CARD = "Grouped Applications";
export const INBOX_TABLE = "Grouped Events";

// The GraphQL operation every listing refetch goes through
// (app/src/api1/kubernetes/index.ts:2135). Filter changes leave the previous
// <tbody> attached, so waiting on the DOM alone reads the stale table.
export const INBOX_LIST_OPERATION = "k8s_event_groupings";

// Issue Type options. 'all' is the component's own default and the only value
// that clears the URL param, because applyFiltersOnRouter drops empty values.
export const ISSUE_TYPE_ALL = "All Issues";
export const ISSUE_TYPE_NEW = "New Issues";
export const ISSUE_TYPE_RECURRING = "Recurring Issues";

// Severity filter options. 'High' is chosen for the filter test on purpose: the
// rendered severity is derived from distinct_priority with HIGH taking top
// precedence (KubernetesGroupedEventsTable.tsx:137), so a group returned for
// priority=HIGH always renders HIGH. Filtering a lower severity gives no such
// guarantee — a group holding both MEDIUM and HIGH still renders HIGH.
export const SEVERITY_HIGH_LABEL = "High";
export const SEVERITY_HIGH_VALUE = "HIGH";

// Sort By option whose column the table also renders, so the resulting order is
// observable. It maps to fingerprint_event_count, which is exactly what the Count
// column shows, and onSortByChange always applies 'desc'.
export const SORT_BY_EVENT_COUNT = "Event Count";

// Two of the classification choices CLASSIFICATION_OPTIONS offers
// (app/src/api1/triage/index.ts:918). Asserted as present, never clicked —
// classifying an event on shared dev changes it for every other user.
export const CLASSIFY_OPTIONS = ["True Positive", "False Positive"] as const;
