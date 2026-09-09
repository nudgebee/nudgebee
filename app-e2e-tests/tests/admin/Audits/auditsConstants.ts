// Not for OSS

// The Audits tab of /user-management (app/src/pages/user-management/index.jsx:31),
// whose body is AuditsTable (app/src/components/audits/index.jsx).
export const AUDITS_PATH = "/user-management#audits";
export const USERS_PATH = "/user-management#users";

// The GraphQL operation AuditsTable issues for every filter, page and page-size
// change (app/src/api1/audits/index.ts:3).
export const AUDITS_OPERATION = "ListAuditEvents";
export const GRAPHQL_PATH = "/api/graphql";

// Column order of the `headers` array in app/src/components/audits/index.jsx:32.
export const COLUMN = {
  user: 0,
  summary: 1,
  categoryType: 2,
  action: 3,
  status: 4,
  createdAt: 5,
  target: 6,
} as const;

export const COLUMN_HEADERS = ["User", "Summary", "Category/Type", "Action", "Status", "Created At", "Target"];

// The `id` prop each FilterDropdown is given in app/src/components/audits/index.jsx.
// The component renders its trigger as `auto-complete-${toKebabCase(id)}`.
export const FILTER = {
  status: "audit-filter-status",
  category: "audit-filter-category",
  eventType: "audit-filter-event-type",
  action: "audit-filter-action",
  user: "audit-filter-user",
  cluster: "audit-filter-cluster",
} as const;

// Visible labels of the toolbar filters, used as the scoped role fallback for
// each trigger. These are the `label` props in app/src/components/audits/index.jsx.
export const FILTER_LABEL = {
  status: "Status",
  category: "Category",
  eventType: "Event Type",
  action: "Action",
  user: "User",
  cluster: "Cluster",
} as const;

// A category and an event type that can never appear on the same audit row:
// listAudits sends both as bare `_ilike` equality predicates, so a TICKETS row
// cannot also carry a K8s-relay agent event type. This makes the empty state
// reachable without depending on how quiet the shared dev cluster happens to be.
export const IMPOSSIBLE_PAIR = {
  categoryValue: "TICKETS",
  categoryLabel: "Tickets",
  eventTypeValue: "K8SRELAY_AGENT_CONNECTED",
  eventTypeLabel: "K8srelay Agent Connected",
} as const;

// FilterDropdown only renders its search box when a list holds more than eight
// options (FilterDropdown.jsx:1220). Category has 24 and Event Type has 131, so
// both search; Status (2) and Action (5) never do.
export const SEARCHABLE_FILTER_MIN_OPTIONS = 8;

// The page sizes ds/Select offers in the table footer (CustomTablePagination.jsx:17).
export const PAGE_SIZE_OPTIONS = ["5", "10", "20", "50", "100"] as const;

// Mirrors `capitalize` in app/src/utils/common.ts:856 — first character upper,
// the rest lowered. It is what turns a stored SUCCESS/CREATE into the Status
// chip and Action cell the table renders.
export function toDisplayLabel(value: string): string {
  return value ? value.charAt(0).toUpperCase() + value.slice(1).toLowerCase() : value;
}

export function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
