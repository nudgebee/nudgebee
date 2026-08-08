/**
 * The query-engine surface a `nudgebee` panel can author.
 *
 * Columns and operators mirror `api-server/services/query/metadata.go` — the
 * engine validates against the same registry, so anything not declared here
 * would be rejected server-side. Keeping the list explicit (rather than
 * fetching it) is what lets the editor offer type-appropriate operators.
 */

export type EntityColumnType = 'string' | 'number' | 'datetime' | 'boolean' | 'json';

export interface EntityColumn {
  name: string;
  label: string;
  type: EntityColumnType;
  /**
   * False when the panel cannot filter on it. Only the traces tables set this:
   * they are read through the traces service, whose filters are named
   * parameters rather than a free where clause (see traceQuery.ts).
   */
  filterable?: boolean;
}

export interface EntityTable {
  value: string;
  label: string;
  /** Which panel datasource may query it — the server enforces the same split. */
  datasource: 'nudgebee' | 'traces';
  /** One line under the picker — what the table holds. */
  description: string;
  /** What a row IS, and when to reach for this table over the other. */
  detail: string;
  columns: EntityColumn[];
  /** Columns the dashboard's time range may be applied to. */
  timeColumns: string[];
  defaultColumns: string[];
  defaultSort: string;
}

const EVENT_COLUMNS: EntityColumn[] = [
  { name: 'starts_at', label: 'Started at', type: 'datetime' },
  { name: 'ends_at', label: 'Ended at', type: 'datetime' },
  { name: 'created_at', label: 'Created at', type: 'datetime' },
  { name: 'title', label: 'Title', type: 'string' },
  { name: 'description', label: 'Description', type: 'string' },
  { name: 'priority', label: 'Priority', type: 'string' },
  { name: 'computed_priority', label: 'Computed priority', type: 'string' },
  { name: 'computed_score', label: 'Computed score', type: 'number' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'nb_status', label: 'Nudgebee status', type: 'string' },
  { name: 'category', label: 'Category', type: 'string' },
  { name: 'finding_type', label: 'Finding type', type: 'string' },
  { name: 'source', label: 'Source', type: 'string' },
  { name: 'cluster', label: 'Cluster', type: 'string' },
  { name: 'subject_type', label: 'Subject type', type: 'string' },
  { name: 'subject_name', label: 'Subject name', type: 'string' },
  { name: 'subject_namespace', label: 'Namespace', type: 'string' },
  { name: 'subject_node', label: 'Node', type: 'string' },
  { name: 'subject_owner', label: 'Owner', type: 'string' },
  { name: 'resource_id', label: 'Resource id', type: 'string' },
  { name: 'aggregation_key', label: 'Aggregation key', type: 'string' },
  { name: 'fingerprint', label: 'Fingerprint', type: 'string' },
  { name: 'is_new_issue', label: 'Is new issue', type: 'boolean' },
  { name: 'urgency', label: 'Urgency', type: 'string' },
  { name: 'labels', label: 'Labels', type: 'json' },
];

const EVENT_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'title', label: 'Title', type: 'string' },
  { name: 'subject_name', label: 'Subject name', type: 'string' },
  { name: 'subject_namespace', label: 'Namespace', type: 'string' },
  { name: 'subject_type', label: 'Subject type', type: 'string' },
  { name: 'cluster', label: 'Cluster', type: 'string' },
  { name: 'priority', label: 'Priority', type: 'string' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'category', label: 'Category', type: 'string' },
  { name: 'event_count', label: 'Event count', type: 'number' },
  { name: 'count_priority_p0', label: 'P0 count', type: 'number' },
  { name: 'count_priority_p1', label: 'P1 count', type: 'number' },
  { name: 'count_priority_p2', label: 'P2 count', type: 'number' },
  { name: 'count_priority_p3', label: 'P3 count', type: 'number' },
  { name: 'count_new_issues', label: 'New issues', type: 'number' },
  { name: 'count_pod_issues', label: 'Pod issues', type: 'number' },
  { name: 'count_node_issues', label: 'Node issues', type: 'number' },
  { name: 'count_application_issues', label: 'Application issues', type: 'number' },
  { name: 'max_created_at', label: 'Last seen', type: 'datetime' },
  { name: 'min_created_at', label: 'First seen', type: 'datetime' },
  { name: 'starts_at', label: 'Started at', type: 'datetime' },
  { name: 'created_at', label: 'Created at', type: 'datetime' },
];

const RECOMMENDATION_COLUMNS: EntityColumn[] = [
  { name: 'created_at', label: 'Created at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'rule_name', label: 'Rule', type: 'string' },
  { name: 'category', label: 'Category', type: 'string' },
  { name: 'severity', label: 'Severity', type: 'string' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'estimated_savings', label: 'Estimated savings', type: 'number' },
  { name: 'resource_name', label: 'Resource', type: 'string' },
  { name: 'resource_type', label: 'Resource type', type: 'string' },
  { name: 'resource_cloud_service', label: 'Service', type: 'string' },
  { name: 'resource_cloud_provider', label: 'Cloud provider', type: 'string' },
  { name: 'resource_region', label: 'Region', type: 'string' },
  { name: 'resource_status', label: 'Resource status', type: 'string' },
  { name: 'resource_id', label: 'Resource id', type: 'string' },
  { name: 'is_dismissed', label: 'Is dismissed', type: 'boolean' },
  { name: 'dismissed_reason', label: 'Dismissed reason', type: 'string' },
  { name: 'snoozed_until', label: 'Snoozed until', type: 'datetime' },
  // Ranks a recommendation against its own history — the current one is
  // primary, superseded revisions are not. Filter on it to avoid counting the
  // same finding several times.
  { name: 'is_primary_recommendation', label: 'Is primary', type: 'boolean' },
  { name: 'finops_score', label: 'FinOps score', type: 'number' },
  { name: 'finops_band', label: 'FinOps band', type: 'string' },
  { name: 'safety_band', label: 'Safety band', type: 'string' },
  { name: 'note', label: 'Note', type: 'string' },
];

const RECOMMENDATION_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'rule_name', label: 'Rule', type: 'string' },
  { name: 'category', label: 'Category', type: 'string' },
  { name: 'severity', label: 'Severity', type: 'string' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'count', label: 'Recommendations', type: 'number' },
  { name: 'sum_estimated_savings', label: 'Total savings', type: 'number' },
  { name: 'estimated_savings', label: 'Estimated savings', type: 'number' },
  { name: 'account_name', label: 'Account', type: 'string' },
  { name: 'account_cloud_provider', label: 'Cloud provider', type: 'string' },
  { name: 'resource_name', label: 'Resource', type: 'string' },
  { name: 'resource_type', label: 'Resource type', type: 'string' },
  { name: 'resource_cloud_service', label: 'Service', type: 'string' },
  { name: 'resource_region', label: 'Region', type: 'string' },
  { name: 'safety_band', label: 'Safety band', type: 'string' },
  { name: 'timestamp', label: 'Timestamp', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
];

const SPEND_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'spend_date', label: 'Date', type: 'datetime' },
  { name: 'resource_service_name', label: 'Cloud service', type: 'string' },
  { name: 'resource_type', label: 'Resource type', type: 'string' },
  { name: 'resource_region', label: 'Region', type: 'string' },
  { name: 'resource_id', label: 'Resource id', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
  { name: 'spend_amount', label: 'Spend', type: 'number' },
  { name: 'spend_count', label: 'Line items', type: 'number' },
  { name: 'resource_count', label: 'Resources', type: 'number' },
  { name: 'account_count', label: 'Accounts', type: 'number' },
  { name: 'currency_type', label: 'Currency', type: 'string' },
  // Rows the biller excludes from a total — credits, refunds, tax lines. Every
  // spend figure in the product filters these out; a panel that forgets to
  // over-reports the bill.
  { name: 'exclude_aggregate', label: 'Excluded from total', type: 'boolean' },
];

const CLUSTER_COLUMNS: EntityColumn[] = [
  { name: 'account_id', label: 'Cluster account', type: 'string' },
  { name: 'node_count', label: 'Nodes', type: 'number' },
  { name: 'node_spot_count', label: 'Spot nodes', type: 'number' },
  { name: 'node_cpu_capacity', label: 'CPU capacity', type: 'number' },
  { name: 'node_cpu_allocatable', label: 'CPU allocatable', type: 'number' },
  { name: 'node_memory_capacity', label: 'Memory capacity', type: 'number' },
  { name: 'node_memory_allocatable', label: 'Memory allocatable', type: 'number' },
  { name: 'workload_type_counts', label: 'Workloads by kind', type: 'json' },
  { name: 'pod_status_counts', label: 'Pods by status', type: 'json' },
];

const NODE_COLUMNS: EntityColumn[] = [
  { name: 'name', label: 'Node', type: 'string' },
  { name: 'is_active', label: 'Is active', type: 'boolean' },
  { name: 'node_type', label: 'Node type', type: 'string' },
  { name: 'node_flavor', label: 'Instance type', type: 'string' },
  { name: 'node_region', label: 'Region', type: 'string' },
  { name: 'node_zone', label: 'Zone', type: 'string' },
  { name: 'cpu_capacity', label: 'CPU capacity', type: 'number' },
  { name: 'cpu_allocatable', label: 'CPU allocatable', type: 'number' },
  { name: 'cpu_limits', label: 'CPU limits', type: 'number' },
  { name: 'memory_capacity', label: 'Memory capacity', type: 'number' },
  { name: 'memory_allocatable', label: 'Memory allocatable', type: 'number' },
  { name: 'memory_limits', label: 'Memory limits', type: 'number' },
  { name: 'pod_count', label: 'Pods', type: 'number' },
  { name: 'cost', label: 'Cost', type: 'number' },
  { name: 'node_creation_time', label: 'Created at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'cloud_account_id', label: 'Cluster account', type: 'string' },
  { name: 'cloud_resource_id', label: 'Cloud resource id', type: 'string' },
  { name: 'internal_ip', label: 'Internal IP', type: 'string' },
  { name: 'external_ip', label: 'External IP', type: 'string' },
  { name: 'labels', label: 'Labels', type: 'json' },
  { name: 'taints', label: 'Taints', type: 'json' },
  { name: 'conditions', label: 'Conditions', type: 'json' },
];

const TICKET_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'created_at', label: 'Created at', type: 'datetime' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'severity', label: 'Severity', type: 'string' },
  { name: 'assignee', label: 'Assignee', type: 'string' },
  { name: 'created_by', label: 'Created by', type: 'string' },
  { name: 'platform', label: 'Platform', type: 'string' },
  { name: 'ticket_type', label: 'Ticket type', type: 'string' },
  { name: 'title', label: 'Title', type: 'string' },
  { name: 'reference_id', label: 'Reference', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
  { name: 'count', label: 'Tickets', type: 'number' },
];

const ANOMALY_COLUMNS: EntityColumn[] = [
  { name: 'evaluated_at', label: 'Evaluated at', type: 'datetime' },
  { name: 'created_at', label: 'Created at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'anomaly_type', label: 'Anomaly type', type: 'string' },
  { name: 'name', label: 'Subject', type: 'string' },
  { name: 'namespace', label: 'Namespace', type: 'string' },
  { name: 'is_anomaly', label: 'Is anomaly', type: 'boolean' },
  // Strings on the engine, not numbers — a metric anomaly and a spend anomaly
  // carry different units, so they are stored as written rather than coerced.
  { name: 'current_value', label: 'Current value', type: 'string' },
  { name: 'reference_value', label: 'Expected value', type: 'string' },
  { name: 'config_id', label: 'Detector', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
  { name: 'insights', label: 'Insights', type: 'json' },
];

const ANOMALY_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'anomaly_type', label: 'Anomaly type', type: 'string' },
  { name: 'name', label: 'Subject', type: 'string' },
  { name: 'namespace', label: 'Namespace', type: 'string' },
  { name: 'is_anomaly', label: 'Is anomaly', type: 'boolean' },
  { name: 'count', label: 'Occurrences', type: 'number' },
  { name: 'evaluated_at', label: 'Evaluated at', type: 'datetime' },
  { name: 'created_at', label: 'Created at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'config_id', label: 'Detector', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
];

const CIS_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'rule_name', label: 'Rule', type: 'string' },
  { name: 'rule_description', label: 'Description', type: 'string' },
  { name: 'rule_id', label: 'Rule id', type: 'string' },
  { name: 'severity', label: 'Severity', type: 'string' },
  { name: 'severity_weight', label: 'Severity weight', type: 'number' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'count', label: 'Findings', type: 'number' },
  { name: 'updated_at', label: 'Last checked', type: 'datetime' },
  { name: 'account_id', label: 'Account id', type: 'string' },
];

const VULNERABILITY_COLUMNS: EntityColumn[] = [
  { name: 'created_at', label: 'Created at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'severity', label: 'Severity', type: 'string' },
  { name: 'severity_weight', label: 'Severity weight', type: 'number' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'is_active', label: 'Is active', type: 'boolean' },
  { name: 'image', label: 'Image', type: 'string' },
  { name: 'vulnerability_id', label: 'CVE', type: 'string' },
  { name: 'package_id', label: 'Package', type: 'string' },
  { name: 'workload_name', label: 'Workload', type: 'string' },
  { name: 'workload_type', label: 'Workload type', type: 'string' },
  { name: 'namespace', label: 'Namespace', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
];

const AUTOPILOT_TASK_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'auto_pilot_category', label: 'Category', type: 'string' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'count', label: 'Tasks', type: 'number' },
  { name: 'scheduled_time', label: 'Scheduled for', type: 'datetime' },
  { name: 'auto_pilot_id', label: 'Autopilot id', type: 'string' },
  { name: 'auto_pilot_account_id', label: 'Autopilot account', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
];

const APPROVAL_COLUMNS: EntityColumn[] = [
  { name: 'created_at', label: 'Requested at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'auto_pilot_type', label: 'Action type', type: 'string' },
  { name: 'approval_status_description', label: 'Decision', type: 'string' },
  { name: 'reviewer_display_name', label: 'Reviewer', type: 'string' },
  { name: 'reviewer_comments', label: 'Comments', type: 'string' },
  { name: 'autopilot_id', label: 'Autopilot id', type: 'string' },
  { name: 'policy_id', label: 'Policy', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
];

const AUDIT_COLUMNS: EntityColumn[] = [
  { name: 'event_time', label: 'When', type: 'datetime' },
  { name: 'username', label: 'User', type: 'string' },
  { name: 'event_category', label: 'Category', type: 'string' },
  { name: 'event_type', label: 'Type', type: 'string' },
  { name: 'event_action', label: 'Action', type: 'string' },
  { name: 'event_status', label: 'Status', type: 'string' },
  { name: 'event_target', label: 'Target', type: 'string' },
  { name: 'event_actor', label: 'Actor', type: 'string' },
  { name: 'transaction_id', label: 'Transaction', type: 'string' },
  { name: 'account_id', label: 'Account id', type: 'string' },
  { name: 'event_attr', label: 'Attributes', type: 'json' },
];

const AGENT_HEALTH_COLUMNS: EntityColumn[] = [
  { name: 'type', label: 'Agent type', type: 'string' },
  { name: 'version', label: 'Version', type: 'string' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'status_message', label: 'Status message', type: 'string' },
  { name: 'last_connected_at', label: 'Last seen', type: 'datetime' },
  { name: 'created_at', label: 'Registered at', type: 'datetime' },
  { name: 'k8s_version', label: 'K8s version', type: 'string' },
  { name: 'k8s_provider', label: 'K8s provider', type: 'string' },
  { name: 'cloud_account_id', label: 'Account id', type: 'string' },
  { name: 'connection_status', label: 'Connection status', type: 'json' },
];

const AI_CONVERSATION_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'source', label: 'Source', type: 'string' },
  { name: 'status', label: 'Status', type: 'string' },
  { name: 'title', label: 'Title', type: 'string' },
  { name: 'count', label: 'Investigations', type: 'number' },
  { name: 'created_at', label: 'Started at', type: 'datetime' },
  { name: 'updated_at', label: 'Updated at', type: 'datetime' },
  { name: 'account_id', label: 'Account id', type: 'string' },
];

const TRACE_COLUMNS: EntityColumn[] = [
  { name: 'timestamp', label: 'Timestamp', type: 'datetime' },
  { name: 'trace_id', label: 'Trace id', type: 'string', filterable: true },
  { name: 'span_id', label: 'Span id', type: 'string' },
  { name: 'parent_span_id', label: 'Parent span id', type: 'string' },
  { name: 'span_name', label: 'Span name', type: 'string', filterable: true },
  { name: 'workload_name', label: 'Workload', type: 'string', filterable: true },
  { name: 'workload_namespace', label: 'Namespace', type: 'string', filterable: true },
  { name: 'destination_name', label: 'Destination', type: 'string', filterable: true },
  { name: 'destination_workload_name', label: 'Destination workload', type: 'string', filterable: true },
  { name: 'destination_workload_namespace', label: 'Destination namespace', type: 'string', filterable: true },
  { name: 'duration_ns', label: 'Duration (ns)', type: 'number', filterable: true },
  { name: 'status_code', label: 'Status code', type: 'string', filterable: true },
  { name: 'http_status_code', label: 'HTTP status', type: 'string', filterable: true },
  { name: 'resource', label: 'Resource', type: 'string', filterable: true },
  { name: 'trace_source', label: 'Trace source', type: 'string', filterable: true },
];

// `traces_grouping_v3` returns a FIXED field list — unlike spans, the selection
// is not ours to choose, so every column here must appear in that response.
const TRACE_GROUPING_COLUMNS: EntityColumn[] = [
  { name: 'workload_name', label: 'Workload', type: 'string', filterable: true },
  { name: 'workload_namespace', label: 'Namespace', type: 'string', filterable: true },
  { name: 'span_name', label: 'Span name', type: 'string' },
  { name: 'resource', label: 'Resource', type: 'string', filterable: true },
  { name: 'destination_workload_name', label: 'Destination workload', type: 'string', filterable: true },
  { name: 'destination_workload_namespace', label: 'Destination namespace', type: 'string', filterable: true },
  { name: 'destination_workload_zone', label: 'Destination zone', type: 'string' },
  { name: 'http_status_code', label: 'HTTP status', type: 'string', filterable: true },
  { name: 'count', label: 'Requests', type: 'number' },
  { name: 'error_count', label: 'Errors', type: 'number' },
  { name: 'duration_ns', label: 'Duration (ns)', type: 'number' },
  { name: 'p95_latency', label: 'p95 latency', type: 'number' },
  { name: 'p99_latency', label: 'p99 latency', type: 'number' },
  { name: 'max_latency', label: 'Max latency', type: 'number' },
];

/**
 * Events go through the query engine (whose registry holds ~111 tables, most of
 * which have no business in a dashboard — the server enforces the same
 * allowlist). Traces go through the traces service instead: see traceQuery.ts.
 */
export const ENTITY_TABLES: EntityTable[] = [
  {
    value: 'events_v2',
    datasource: 'nudgebee',
    label: 'Events',
    description: 'One row per event occurrence.',
    detail:
      'Every event Nudgebee has recorded — alerts, K8s findings and detections — one row each, newest first. Each row names what it happened to ' +
      '(subject type, name, namespace, node), how it was rated (priority, computed score, status) and when it started and ended. Use this to read ' +
      'the individual events behind an incident; use Event groups to count them.',
    columns: EVENT_COLUMNS,
    timeColumns: ['starts_at', 'created_at', 'ends_at'],
    defaultColumns: ['starts_at', 'title', 'priority', 'subject_type', 'subject_name', 'status'],
    defaultSort: 'starts_at',
  },
  {
    value: 'event_groupings_v2',
    datasource: 'nudgebee',
    label: 'Event groups',
    // events_v2 is a row table — the engine will not take an arbitrary GROUP BY
    // on it, so counts come from this pre-aggregated twin instead.
    description: 'One row per recurring issue, with counts.',
    detail:
      'Repeat occurrences of the same issue collapsed into one row, carrying the counts: how many events in total, how many at each priority, and ' +
      'how many are new. Also when the issue was first and last seen. Use this for "how many" and "what is noisiest" — the engine cannot group the ' +
      'Events table on the fly, so counting there is not possible.',
    columns: EVENT_GROUPING_COLUMNS,
    timeColumns: ['starts_at', 'created_at', 'max_created_at'],
    defaultColumns: ['title', 'subject_name', 'subject_namespace', 'priority', 'event_count'],
    defaultSort: 'event_count',
  },
  {
    value: 'recommendations_v2',
    datasource: 'nudgebee',
    label: 'Recommendations',
    description: 'One row per recommendation.',
    detail:
      'Every recommendation Nudgebee has raised — right-sizing, idle resources, configuration and security findings — one row each. Each row names ' +
      'the resource it is about (name, type, cloud service, region), how it was rated (category, severity, status, safety band) and what it is ' +
      'worth (estimated savings). Filter on Is primary to count a finding once rather than once per revision.',
    columns: RECOMMENDATION_COLUMNS,
    timeColumns: ['created_at', 'updated_at'],
    defaultColumns: ['created_at', 'rule_name', 'category', 'resource_name', 'estimated_savings', 'status'],
    defaultSort: 'created_at',
  },
  {
    value: 'recommendation_groupings_v2',
    datasource: 'nudgebee',
    label: 'Recommendation groups',
    // Same reason as Event groups: recommendations_v2 is a row table and the
    // engine will not take an arbitrary GROUP BY on it.
    description: 'One row per rule, with counts and savings.',
    detail:
      'Recommendations collapsed by rule, carrying how many there are and what they add up to. This is the table for "where is the money" and ' +
      '"which rule fires most" — the engine cannot group the Recommendations table on the fly, so totals are not possible there.',
    columns: RECOMMENDATION_GROUPING_COLUMNS,
    timeColumns: ['timestamp', 'updated_at'],
    defaultColumns: ['rule_name', 'category', 'count', 'sum_estimated_savings', 'account_name'],
    defaultSort: 'sum_estimated_savings',
  },
  {
    value: 'spend_groupings_v2',
    datasource: 'nudgebee',
    label: 'Cloud spend',
    description: 'One row per spend dimension, with the amount billed.',
    detail:
      'What the cloud actually charged, grouped by whichever of service, resource type, region, resource or day you select, with the amount, the ' +
      'number of resources behind it and the currency. Recommendations say what COULD be saved; this says what was spent. Filter Excluded from ' +
      'total to false — credits and tax lines are stored alongside real charges and every spend figure in the product leaves them out.',
    columns: SPEND_GROUPING_COLUMNS,
    timeColumns: ['spend_date'],
    defaultColumns: ['resource_service_name', 'spend_amount', 'resource_count', 'currency_type'],
    defaultSort: 'spend_amount',
  },
  {
    value: 'k8s_cluster_groupings_v2',
    datasource: 'nudgebee',
    label: 'Clusters',
    description: 'One row per cluster: capacity, workloads and pod states.',
    detail:
      'Every connected cluster with its node count, spot share, CPU and memory capacity and allocatable, plus workload counts by kind and pod ' +
      'counts by status. The fleet-level answer to "how big is this and is it healthy" — the nodes behind it are in Nodes.',
    columns: CLUSTER_COLUMNS,
    // A snapshot of what is running right now: it carries no timestamp, so a
    // dashboard's time picker has nothing to filter it on.
    timeColumns: [],
    defaultColumns: ['account_id', 'node_count', 'node_spot_count', 'node_cpu_capacity', 'node_memory_capacity', 'pod_status_counts'],
    defaultSort: 'node_count',
  },
  {
    value: 'k8s_nodes_v2',
    datasource: 'nudgebee',
    label: 'Nodes',
    description: 'One row per node.',
    detail:
      'Individual nodes: instance type, region and zone, spot or on-demand, CPU and memory capacity against what is reserved, how many pods are on ' +
      'it and what it costs. Use this for capacity and node right-sizing; use Clusters for the fleet summary.',
    columns: NODE_COLUMNS,
    timeColumns: ['node_creation_time', 'updated_at'],
    defaultColumns: ['name', 'node_type', 'node_flavor', 'cpu_capacity', 'memory_capacity', 'pod_count'],
    defaultSort: 'pod_count',
  },
  {
    value: 'ticket_groupings_v2',
    datasource: 'nudgebee',
    label: 'Ticket groups',
    description: 'Ticket counts by status, severity or assignee.',
    detail:
      'Tickets raised from Nudgebee collapsed by whichever of status, severity, assignee, platform or type you select. This is the backlog and ' +
      'inflow view — how much work is open, who holds it and where it was filed.',
    columns: TICKET_GROUPING_COLUMNS,
    timeColumns: ['created_at'],
    defaultColumns: ['status', 'severity', 'assignee', 'count'],
    defaultSort: 'count',
  },
  {
    value: 'anomaly_grouping_v2',
    datasource: 'nudgebee',
    label: 'Anomaly groups',
    description: 'Anomaly counts by type, subject or namespace.',
    detail:
      'Detected anomalies — metric and spend — collapsed by type, subject or namespace, with how many times each fired. The ranked feed for "what ' +
      'is behaving unusually across the estate"; the individual detections are in Anomalies.',
    columns: ANOMALY_GROUPING_COLUMNS,
    timeColumns: ['evaluated_at', 'created_at', 'updated_at'],
    defaultColumns: ['anomaly_type', 'namespace', 'name', 'count'],
    defaultSort: 'count',
  },
  {
    value: 'anomaly_v2',
    datasource: 'nudgebee',
    label: 'Anomalies',
    description: 'One row per detection, with observed vs expected.',
    detail:
      'Each anomaly as detected: what it is about, which detector fired, what the value was and what was expected. Use this to read the detections ' +
      'behind a spike; use Anomaly groups to rank them.',
    columns: ANOMALY_COLUMNS,
    timeColumns: ['evaluated_at', 'created_at', 'updated_at'],
    defaultColumns: ['evaluated_at', 'anomaly_type', 'namespace', 'name', 'current_value', 'reference_value'],
    defaultSort: 'evaluated_at',
  },
  {
    value: 'recommendation_security_cis_groupings_v2',
    datasource: 'nudgebee',
    label: 'CIS compliance',
    description: 'One row per benchmark rule, with how many resources fail it.',
    detail:
      'CIS benchmark results grouped by rule: what the rule checks, how severe it is, whether it passes and how many resources are behind that ' +
      'verdict. The compliance-posture table; container CVEs are in Vulnerabilities.',
    columns: CIS_GROUPING_COLUMNS,
    // `Last checked` is a max() over the group, so the engine cannot filter on
    // it — it is readable as a column, not as a time range.
    timeColumns: [],
    defaultColumns: ['rule_name', 'severity', 'status', 'count'],
    defaultSort: 'count',
  },
  {
    value: 'recommendation_security_v2',
    datasource: 'nudgebee',
    label: 'Vulnerabilities',
    description: 'One row per CVE found in a running image.',
    detail:
      'Container image vulnerabilities as scanned: the CVE, the package, the image and the workload and namespace running it. Filter Is active to ' +
      'true to exclude findings on images that are no longer deployed.',
    columns: VULNERABILITY_COLUMNS,
    timeColumns: ['created_at', 'updated_at'],
    defaultColumns: ['created_at', 'severity', 'workload_name', 'namespace', 'image', 'vulnerability_id'],
    defaultSort: 'created_at',
  },
  {
    value: 'auto_pilot_task_groupings_v2',
    datasource: 'nudgebee',
    label: 'Autopilot task groups',
    description: 'Autopilot task counts by state and category.',
    detail:
      'What automation actually did, collapsed by category and state — how many tasks ran, how many are queued and how many failed. The coverage ' +
      'view behind "how much of this is automated".',
    columns: AUTOPILOT_TASK_GROUPING_COLUMNS,
    timeColumns: ['scheduled_time'],
    defaultColumns: ['auto_pilot_category', 'status', 'count'],
    defaultSort: 'count',
  },
  {
    value: 'auto_pilot_approvals_v2',
    datasource: 'nudgebee',
    label: 'Autopilot approvals',
    description: 'One row per action waiting on, or decided by, a human.',
    detail:
      'The human-in-the-loop queue: which automated action was proposed, under which policy, who reviewed it and what they decided. A panel shows ' +
      'the queue and its age — approving is still done on the Autopilot page.',
    columns: APPROVAL_COLUMNS,
    timeColumns: ['created_at', 'updated_at'],
    defaultColumns: ['created_at', 'auto_pilot_type', 'status', 'reviewer_display_name'],
    defaultSort: 'created_at',
  },
  {
    value: 'audits_v2',
    datasource: 'nudgebee',
    label: 'Audit log',
    description: 'One row per configuration or access change.',
    detail:
      'Who changed what, when, and whether it succeeded — integrations, users, roles, policies. Readable only by a tenant admin or a role holding ' +
      'the audits grant, which the engine enforces per viewer rather than per dashboard.',
    columns: AUDIT_COLUMNS,
    timeColumns: ['event_time'],
    defaultColumns: ['event_time', 'username', 'event_category', 'event_action', 'event_target', 'event_status'],
    defaultSort: 'event_time',
  },
  {
    value: 'get_agent_health_v2',
    datasource: 'nudgebee',
    label: 'Agent health',
    description: 'One row per collector agent, with when it last checked in.',
    detail:
      'The agents feeding Nudgebee: their type, version, cluster version and when each last connected. A stale Last seen is why a dashboard has ' +
      'gone quiet, and it is the first thing to check before believing an empty panel.',
    columns: AGENT_HEALTH_COLUMNS,
    timeColumns: ['last_connected_at', 'created_at'],
    defaultColumns: ['type', 'version', 'status', 'k8s_version', 'last_connected_at'],
    defaultSort: 'last_connected_at',
  },
  {
    value: 'llm_conversation_groupings_v2',
    datasource: 'nudgebee',
    label: 'AI investigations',
    description: 'Investigation counts by source and status.',
    detail:
      'AI investigations and RCAs collapsed by where they came from (an alert, a user, a workflow) and how they ended. The throughput view for ' +
      '"how much is the AI actually doing, and does it finish".',
    columns: AI_CONVERSATION_GROUPING_COLUMNS,
    timeColumns: ['created_at', 'updated_at'],
    defaultColumns: ['source', 'status', 'count'],
    defaultSort: 'count',
  },
  {
    value: 'traces_groupings_v2',
    datasource: 'traces',
    label: 'Trace groups',
    description: 'One row per service call, with latency.',
    detail:
      'Spans collapsed by caller and destination, carrying request count, error count and p50 / p95 / p99 latency. This is the table for "what is ' +
      'slow" and "what is failing" — the individual spans are in Spans, but latency percentiles only exist here.',
    columns: TRACE_GROUPING_COLUMNS,
    timeColumns: ['timestamp'],
    defaultColumns: ['workload_name', 'span_name', 'count', 'error_count', 'p99_latency'],
    defaultSort: 'count',
  },
  {
    value: 'traces_v2',
    datasource: 'traces',
    label: 'Spans',
    description: 'One row per span.',
    detail:
      'Individual spans, newest first: which service and workload emitted it, what it called, how long it took and how it ended. Use this to read ' +
      'the calls behind a slow endpoint; use Trace groups to rank them.',
    columns: TRACE_COLUMNS,
    timeColumns: ['timestamp'],
    defaultColumns: ['timestamp', 'workload_name', 'span_name', 'duration_ns', 'status_code'],
    defaultSort: 'timestamp',
  },
];

/** The tables a panel of this datasource may query. */
export function tablesFor(datasource: string): EntityTable[] {
  return ENTITY_TABLES.filter((t) => t.datasource === datasource);
}

/**
 * Operators the SQL generator actually implements, per column type.
 *
 * Deliberately NOT the full `BinaryWhereClauseType` list: `_icontains`,
 * `_regex` and friends exist only in the operator CATALOG, which serves the log
 * providers (Loki / Elasticsearch chips). The entity path has no case for them
 * and fails the query with "binary clause type not supported", so offering them
 * here would build panels that only break at render.
 */
const OPERATORS_BY_TYPE: Record<EntityColumnType, { value: string; label: string }[]> = {
  string: [
    { value: '_eq', label: 'is' },
    { value: '_neq', label: 'is not' },
    { value: '_in', label: 'is one of' },
    { value: '_not_in', label: 'is none of' },
    { value: '_ilike', label: 'matches pattern (%…%)' },
    { value: '_nlike', label: 'does not match pattern' },
    { value: '_is_null', label: 'is empty' },
  ],
  number: [
    { value: '_eq', label: '=' },
    { value: '_neq', label: '≠' },
    { value: '_gt', label: '>' },
    { value: '_gte', label: '≥' },
    { value: '_lt', label: '<' },
    { value: '_lte', label: '≤' },
  ],
  datetime: [
    { value: '_gte', label: 'after' },
    { value: '_lte', label: 'before' },
    { value: '_is_null', label: 'is empty' },
  ],
  boolean: [{ value: '_eq', label: 'is' }],
  json: [
    { value: '_has_key', label: 'has key' },
    { value: '_contains', label: 'contains' },
  ],
};

/** Operators that take a comma-separated list rather than a single value. */
const LIST_OPERATORS = new Set(['_in', '_not_in']);
/** Operators that take no value at all. */
const NO_VALUE_OPERATORS = new Set(['_is_null']);

export interface EntityFilter {
  column: string;
  operator: string;
  value: string;
}

export interface EntityQueryDraft {
  table: string;
  columns: string[];
  filters: EntityFilter[];
  timeColumn: string;
  /** False leaves the panel unfiltered by the dashboard's time picker. */
  applyTimeRange: boolean;
  sortColumn: string;
  sortDesc: boolean;
  limit: number;
}

/** The stored shape — a query-engine QueryRequest, minus account and time. */
export interface EntityQuery {
  table: string;
  columns: { name: string }[];
  where?: Record<string, unknown>;
  order_by?: { column: string; order: string }[];
  limit?: number;
}

export function findTable(value: string): EntityTable {
  return ENTITY_TABLES.find((t) => t.value === value) || ENTITY_TABLES[0];
}

export function findColumn(table: EntityTable, name: string): EntityColumn | undefined {
  return table.columns.find((c) => c.name === name);
}

/**
 * Columns a filter row may name. Everything is filterable on the query-engine
 * tables; the traces tables mark only what their named parameters cover.
 */
export function filterableColumns(table: EntityTable): EntityColumn[] {
  const anyDeclared = table.columns.some((c) => c.filterable);
  return anyDeclared ? table.columns.filter((c) => c.filterable) : table.columns;
}

export function operatorsFor(table: EntityTable, columnName: string) {
  const column = findColumn(table, columnName);
  return OPERATORS_BY_TYPE[column?.type || 'string'];
}

export function operatorTakesValue(operator: string): boolean {
  return !NO_VALUE_OPERATORS.has(operator);
}

export function operatorTakesList(operator: string): boolean {
  return LIST_OPERATORS.has(operator);
}

/**
 * A working query for a table, or for a datasource's first table when given
 * one — `defaultDraft('traces')` is how a new traces panel starts.
 */
export function defaultDraft(tableOrDatasource = ENTITY_TABLES[0].value): EntityQueryDraft {
  const forDatasource = tablesFor(tableOrDatasource)[0];
  const table = forDatasource || findTable(tableOrDatasource);
  return {
    table: table.value,
    columns: [...table.defaultColumns],
    filters: [],
    // Some tables carry no filterable timestamp at all — a current-state
    // snapshot, or a grouping whose only date column is a max() the engine
    // cannot put in a WHERE. Those open with the time range OFF rather than
    // with a switch that is on and filtering against nothing.
    timeColumn: table.timeColumns[0] || '',
    applyTimeRange: table.timeColumns.length > 0,
    sortColumn: table.defaultSort,
    sortDesc: true,
    limit: 100,
  };
}

/**
 * Compiles the editor's draft into the stored query.
 *
 * The account filter and the time range are deliberately NOT included: the
 * server appends both from the panel's scope and the dashboard's picker, so a
 * saved query stays portable across accounts and time ranges. Only the time
 * COLUMN travels, on the panel target.
 */
export function buildEntityQuery(draft: EntityQueryDraft): EntityQuery {
  const table = findTable(draft.table);
  const columns = draft.columns.filter((name) => findColumn(table, name));
  const query: EntityQuery = {
    table: table.value,
    columns: columns.map((name) => ({ name })),
    limit: draft.limit > 0 ? draft.limit : 100,
  };

  const clauses = draft.filters
    .filter((f) => f.column && f.operator && (operatorTakesValue(f.operator) ? f.value.trim() !== '' : true))
    .map((f) => ({ _binary: { [f.column]: { [f.operator]: filterValue(table, f) } } }));
  if (clauses.length > 0) query.where = { _and: clauses };

  if (draft.sortColumn) {
    query.order_by = [{ column: draft.sortColumn, order: draft.sortDesc ? 'desc' : 'asc' }];
  }
  return query;
}

/**
 * Coerces the typed-in value to what the operator expects. `_in` takes a list,
 * numeric columns take numbers, `_is_null` takes a boolean — the engine
 * compares against the column's real type, so a string "5" would not match an
 * integer column.
 */
function filterValue(table: EntityTable, filter: EntityFilter): unknown {
  if (NO_VALUE_OPERATORS.has(filter.operator)) return true;
  const column = findColumn(table, filter.column);
  const raw = filter.value.trim();

  if (LIST_OPERATORS.has(filter.operator)) {
    const parts = raw
      .split(',')
      .map((p) => p.trim())
      .filter(Boolean);
    return column?.type === 'number' ? parts.map(Number) : parts;
  }
  if (column?.type === 'number') {
    const n = Number(raw);
    return Number.isFinite(n) ? n : raw;
  }
  if (column?.type === 'boolean') return raw.toLowerCase() === 'true';
  return raw;
}

/**
 * Reads a stored query back into the editor's draft, so reopening a panel shows
 * the filters that were saved rather than an empty builder.
 */
export function draftFromQuery(stored: unknown): EntityQueryDraft {
  const query = (stored || {}) as EntityQuery;
  const table = findTable(query.table || ENTITY_TABLES[0].value);
  const base = defaultDraft(table.value);
  if (!query.table) return base;

  const columns = (query.columns || []).map((c) => c.name).filter((name) => findColumn(table, name));
  const clauses = ((query.where as any)?._and || []) as { _binary?: Record<string, Record<string, unknown>> }[];
  const filters: EntityFilter[] = [];
  for (const clause of clauses) {
    for (const [column, byOperator] of Object.entries(clause._binary || {})) {
      for (const [operator, value] of Object.entries(byOperator)) {
        filters.push({ column, operator, value: Array.isArray(value) ? value.join(', ') : String(value) });
      }
    }
  }

  return {
    ...base,
    table: table.value,
    columns: columns.length > 0 ? columns : base.columns,
    filters,
    sortColumn: query.order_by?.[0]?.column || base.sortColumn,
    sortDesc: (query.order_by?.[0]?.order || 'desc') === 'desc',
    limit: query.limit || base.limit,
  };
}
