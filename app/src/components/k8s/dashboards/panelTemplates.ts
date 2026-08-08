import type { AccountOption, Panel, PanelTarget, PanelType } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft, findTable, type EntityQueryDraft } from './entityQuery';
import { panelScopeFromTypes, type PanelScope } from './panelAccounts';
import { nextPanelId } from './panelDefaults';

/**
 * The widget library — one authored panel each, ready to drop onto a dashboard.
 *
 * Everything here is a starting point, not a fixed asset: a widget is copied
 * into the dashboard the moment it is picked, and from then on it is an
 * ordinary panel the author owns. Nothing links back, so editing a copy never
 * changes the library and a library change never rewrites someone's dashboard.
 *
 * A widget carries no account scope of its own, because accounts are per panel
 * and belong to the tenant, not the catalogue. It does carry enough to derive
 * one: `accountKind` says what the query is about, and `defaultWidgetScope`
 * turns that into the account TYPES it can run against, so a copy opens
 * pre-scoped rather than on an empty account field. Types, never a pinned list
 * of somebody's accounts — that is a choice the author makes, not the library.
 *
 * Two rules the whole catalogue keeps:
 *
 *  - ONE target per panel. The panel editor rewrites target A in place, so a
 *    two-target widget would silently lose its second query the first time
 *    someone opened it.
 *  - NO template variables. A query that narrows to a namespace or a workload
 *    writes the matcher as `=~".*"` and the author edits it in the panel editor.
 *    `$name` tokens are only substituted when a dashboard is opened from a page
 *    that supplies them, so anywhere else they run as literal text — a panel
 *    querying for a namespace called `$namespace` finds nothing and looks like
 *    an empty chart rather than a mistake.
 */

/**
 * Who a widget is for.
 *
 * A persona, NOT an RBAC role: it decides which templates a picker offers, and
 * nothing else. Access is still decided per query by the engine, so seeding a
 * finance persona with cost widgets does not hand anyone cost data — a viewer
 * without the grant sees the panel refuse rather than the number.
 *
 * `cfo` and `cloudops` exist because the cost side of the org splits in two
 * that the engineering roles do not cover: someone who owns the bill and
 * someone who works the resources it comes from.
 */
export type TemplateRole = 'cto' | 'cfo' | 'manager' | 'sre' | 'devops' | 'cloudops' | 'developer';

/** Roles in seniority order, which is also the order the filters read in. */
export const TEMPLATE_ROLES: { value: TemplateRole; label: string }[] = [
  { value: 'cto', label: 'CTO' },
  { value: 'cfo', label: 'CFO' },
  { value: 'manager', label: 'Manager' },
  { value: 'sre', label: 'SRE' },
  { value: 'devops', label: 'DevOps' },
  { value: 'cloudops', label: 'Cloud Ops' },
  { value: 'developer', label: 'Developer' },
];

export function roleLabel(role: TemplateRole): string {
  return TEMPLATE_ROLES.find((r) => r.value === role)?.label || role;
}

export type WidgetCategory =
  | 'Cost'
  | 'Issues'
  | 'Reliability'
  | 'Capacity'
  | 'Performance'
  | 'Workload'
  | 'Security'
  | 'Automation'
  | 'AI'
  | 'Governance';

/**
 * Categories in the order the picker groups them: money, then what is broken,
 * then how it is running, then what is watching it.
 *
 * The picker iterates THIS rather than the widgets, so a category missing here
 * hides every widget in it — the list and the union have to be maintained
 * together, which is why they live side by side and a test compares them.
 */
export const WIDGET_CATEGORIES: WidgetCategory[] = [
  'Cost',
  'Issues',
  'Reliability',
  'Capacity',
  'Performance',
  'Workload',
  'Security',
  'Automation',
  'AI',
  'Governance',
];

/** A widget's panel body — everything except the identity a dashboard gives it. */
export type TemplatePanel = Pick<Panel, 'title' | 'description' | 'type' | 'datasource' | 'targets' | 'unit' | 'grid_pos'>;

/**
 * The kind of account a widget's query belongs on.
 *
 * Not derivable from the datasource, which is why it is declared. PromQL and
 * spans reach a cluster, so those are `cluster`. The query engine takes any
 * account, so what decides a findings widget is what it is ABOUT: savings and
 * recommendations live against cloud accounts, K8s events against the cluster.
 * A CloudWatch-native metrics widget, when one is written, declares `cloud` for
 * the same reason.
 *
 * `any` is for the tables where the split does not exist — audits, anomalies,
 * approvals — whose rows belong to whichever account the change or detection
 * touched, cluster or cloud. Narrowing those to one kind hides half the answer,
 * so they open on every connected account. Only the query engine can be scoped
 * that way: every other datasource resolves ONE provider's integration.
 */
export type WidgetAccountKind = 'cluster' | 'cloud' | 'any';

export interface PanelTemplate {
  id: string;
  category: WidgetCategory;
  roles: TemplateRole[];
  /** The question this panel answers — the line under its title in the picker. */
  summary: string;
  /** Defaults to `cluster`, which is what all but the cost widgets want. */
  accountKind?: WidgetAccountKind;
  panel: TemplatePanel;
}

/** A chart or stat over a provider metric. */
function metricPanel(args: { title: string; description: string; expr: string; unit?: string; type?: PanelType; width?: number }): TemplatePanel {
  return {
    title: args.title,
    description: args.description,
    type: args.type || 'timeseries',
    datasource: 'metrics',
    targets: [{ ref_id: 'A', expr: args.expr }],
    unit: args.unit || '',
    grid_pos: { x: 0, y: 0, w: args.width ?? 6, h: 8 },
  };
}

/**
 * A table over the internal query engine (events, recommendations) or the
 * traces service.
 *
 * The query is compiled through the same `buildEntityQuery` the panel editor
 * uses, so a widget round-trips through the builder unchanged — a hand-written
 * query object would drift from whatever the builder produces the moment either
 * side gained a field.
 */
function entityPanel(args: {
  title: string;
  description: string;
  draft: Partial<EntityQueryDraft> & { table: string };
  width?: number;
}): TemplatePanel {
  const table = findTable(args.draft.table);
  const full: EntityQueryDraft = { ...defaultDraft(table.value), ...args.draft };
  const target: PanelTarget = {
    ref_id: 'A',
    query: buildEntityQuery(full) as unknown as Record<string, unknown>,
    time_column: full.applyTimeRange ? full.timeColumn : '',
  };
  return {
    title: args.title,
    description: args.description,
    type: 'table',
    datasource: table.datasource,
    targets: [target],
    unit: '',
    // Taller than a chart: a table showing five rows of a ten-row query reads as
    // if that were all there was.
    grid_pos: { x: 0, y: 0, w: args.width ?? 6, h: 10 },
  };
}

export const PANEL_TEMPLATES: PanelTemplate[] = [
  // ── Cost ────────────────────────────────────────────────────────────────
  {
    id: 'savings-by-rule',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'manager', 'devops'],
    summary: 'Which kind of waste is worth the most, ranked by total savings.',
    panel: entityPanel({
      title: 'Savings opportunity by rule',
      description: 'Open recommendations grouped by the rule that raised them, ranked by what they add up to.',
      draft: {
        table: 'recommendation_groupings_v2',
        columns: ['rule_name', 'category', 'count', 'sum_estimated_savings'],
        sortColumn: 'sum_estimated_savings',
        limit: 15,
      },
    }),
  },
  {
    id: 'savings-by-account',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'manager'],
    summary: 'Where the money is, by cloud account.',
    panel: entityPanel({
      title: 'Savings opportunity by account',
      description: 'The same recommendations grouped by account, so the spend conversation has an owner.',
      draft: {
        table: 'recommendation_groupings_v2',
        columns: ['account_name', 'account_cloud_provider', 'count', 'sum_estimated_savings'],
        sortColumn: 'sum_estimated_savings',
        limit: 15,
      },
    }),
  },
  {
    id: 'savings-by-service',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'manager', 'devops'],
    summary: 'Which cloud service the waste sits in — compute, storage, database.',
    panel: entityPanel({
      title: 'Savings opportunity by service',
      description: 'Recommendations grouped by the cloud service and resource type they were raised against.',
      draft: {
        table: 'recommendation_groupings_v2',
        columns: ['resource_cloud_service', 'resource_type', 'count', 'sum_estimated_savings'],
        sortColumn: 'sum_estimated_savings',
        limit: 15,
      },
    }),
  },
  {
    id: 'top-savings-resources',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['manager', 'devops', 'sre'],
    summary: 'The individual resources to act on first.',
    panel: entityPanel({
      title: 'Top resources to right-size',
      description:
        'Open recommendations, biggest saving first. Filtered to primary revisions so one finding is counted once rather than once per revision.',
      draft: {
        table: 'recommendations_v2',
        columns: ['resource_name', 'resource_type', 'rule_name', 'estimated_savings', 'severity', 'status'],
        filters: [
          { column: 'status', operator: '_eq', value: 'Open' },
          { column: 'is_primary_recommendation', operator: '_eq', value: 'true' },
        ],
        sortColumn: 'estimated_savings',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'critical-recommendations',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['sre', 'devops', 'manager'],
    summary: 'The findings rated Critical or High, whatever they are worth.',
    panel: entityPanel({
      title: 'Critical & high findings',
      description: 'Open recommendations at the top two severities — risk rather than spend, so savings are not the sort order.',
      draft: {
        table: 'recommendations_v2',
        columns: ['created_at', 'rule_name', 'category', 'severity', 'resource_name', 'resource_type'],
        filters: [
          { column: 'status', operator: '_eq', value: 'Open' },
          { column: 'is_primary_recommendation', operator: '_eq', value: 'true' },
          { column: 'severity', operator: '_in', value: 'Critical, High' },
        ],
        sortColumn: 'created_at',
        limit: 20,
      },
      width: 12,
    }),
  },

  {
    id: 'cost-by-service',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'cfo', 'manager', 'cloudops'],
    summary: 'What the cloud actually billed, by service.',
    panel: entityPanel({
      title: 'Spend by cloud service',
      description:
        'Billed spend grouped by cloud service. Credits, refunds and tax lines are excluded, so this matches the invoice rather than the raw line items.',
      draft: {
        table: 'spend_groupings_v2',
        columns: ['resource_service_name', 'spend_amount', 'resource_count', 'currency_type'],
        filters: [{ column: 'exclude_aggregate', operator: '_eq', value: 'false' }],
        sortColumn: 'spend_amount',
        limit: 15,
      },
    }),
  },
  {
    id: 'cost-by-resource',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cfo', 'cloudops', 'manager'],
    summary: 'The individual resources the bill is made of — the chargeback view.',
    panel: entityPanel({
      title: 'Top spending resources',
      description: 'Billed spend per resource, biggest first. The row-level detail behind a service total, for showback and chargeback.',
      draft: {
        table: 'spend_groupings_v2',
        columns: ['resource_id', 'resource_service_name', 'resource_type', 'resource_region', 'spend_amount'],
        filters: [{ column: 'exclude_aggregate', operator: '_eq', value: 'false' }],
        sortColumn: 'spend_amount',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'cost-by-region',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cfo', 'cloudops'],
    summary: 'Where the money is being spent geographically.',
    panel: entityPanel({
      title: 'Spend by region',
      description: 'Billed spend per region. A region nobody remembers deploying to is usually the cheapest saving on the page.',
      draft: {
        table: 'spend_groupings_v2',
        columns: ['resource_region', 'spend_amount', 'resource_count'],
        filters: [{ column: 'exclude_aggregate', operator: '_eq', value: 'false' }],
        sortColumn: 'spend_amount',
        limit: 15,
      },
    }),
  },
  {
    id: 'daily-spend-trend',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'cfo', 'manager'],
    summary: 'Spend day by day — is the bill going up, and when did it turn.',
    panel: entityPanel({
      title: 'Daily spend',
      description: 'Billed spend per day over the dashboard’s window, oldest first, so a step change is visible as a step rather than as a total.',
      draft: {
        table: 'spend_groupings_v2',
        columns: ['spend_date', 'spend_amount', 'resource_count'],
        filters: [{ column: 'exclude_aggregate', operator: '_eq', value: 'false' }],
        sortColumn: 'spend_date',
        sortDesc: false,
        limit: 60,
      },
    }),
  },
  {
    id: 'rightsizing-recommendations',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cloudops', 'devops', 'manager'],
    summary: 'Pods, replicas and volumes proposed for a smaller size.',
    panel: entityPanel({
      title: 'Right-sizing proposals',
      description:
        'Open right-sizing recommendations with what each is worth. Filtered to primary revisions so one proposal is counted once rather than once per refresh.',
      draft: {
        table: 'recommendations_v2',
        columns: ['resource_name', 'resource_type', 'rule_name', 'estimated_savings', 'severity', 'safety_band'],
        filters: [
          { column: 'category', operator: '_eq', value: 'RightSizing' },
          { column: 'status', operator: '_in', value: 'Open, InProgress' },
          { column: 'is_primary_recommendation', operator: '_eq', value: 'true' },
        ],
        sortColumn: 'estimated_savings',
        limit: 20,
      },
      width: 12,
    }),
  },

  // ── Issues ──────────────────────────────────────────────────────────────
  {
    id: 'noisiest-issues',
    category: 'Issues',
    roles: ['cto', 'manager', 'sre'],
    summary: 'What is firing most often — the backlog behind the on-call load.',
    panel: entityPanel({
      title: 'Noisiest issues',
      description: 'Repeat occurrences of the same issue collapsed into one row, most frequent first.',
      draft: {
        table: 'event_groupings_v2',
        columns: ['title', 'subject_namespace', 'subject_name', 'priority', 'event_count', 'max_created_at'],
        sortColumn: 'event_count',
        limit: 15,
      },
      width: 12,
    }),
  },
  {
    id: 'issues-by-namespace',
    category: 'Issues',
    roles: ['manager', 'sre', 'devops'],
    summary: 'Which team’s namespace is generating the alerts.',
    panel: entityPanel({
      title: 'Issues by namespace',
      description: 'Event volume per namespace, split by priority and by what the issue was about.',
      draft: {
        table: 'event_groupings_v2',
        columns: ['subject_namespace', 'event_count', 'count_priority_p0', 'count_priority_p1', 'count_new_issues'],
        sortColumn: 'event_count',
        limit: 15,
      },
    }),
  },
  {
    id: 'p0-p1-events',
    category: 'Issues',
    roles: ['manager', 'sre', 'devops'],
    summary: 'The events that actually warranted waking someone.',
    panel: entityPanel({
      title: 'P0 & P1 events',
      description: 'Individual events at the top two priorities in the selected time range.',
      draft: {
        table: 'events_v2',
        columns: ['starts_at', 'title', 'priority', 'subject_type', 'subject_name', 'subject_namespace', 'status'],
        filters: [{ column: 'priority', operator: '_in', value: 'P0, P1' }],
        sortColumn: 'starts_at',
        limit: 25,
      },
      width: 12,
    }),
  },
  {
    id: 'new-issues',
    category: 'Issues',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Problems seen for the first time — usually the last deploy.',
    panel: entityPanel({
      title: 'New issues in this window',
      description: 'Events flagged as a first occurrence rather than a recurrence of something already known.',
      draft: {
        table: 'events_v2',
        columns: ['starts_at', 'title', 'priority', 'subject_type', 'subject_name', 'subject_namespace'],
        filters: [{ column: 'is_new_issue', operator: '_eq', value: 'true' }],
        sortColumn: 'starts_at',
        limit: 25,
      },
      width: 12,
    }),
  },

  {
    id: 'alert-triage-summary',
    category: 'Issues',
    roles: ['cto', 'manager', 'sre'],
    summary: 'Alert volume split by priority and status — the triage picture in one table.',
    panel: entityPanel({
      title: 'Alert triage summary',
      description:
        'Issues grouped by priority, status and category, with how many events sit behind each and how many are new. Read it top-down: the biggest count that is still open is the queue.',
      draft: {
        table: 'event_groupings_v2',
        columns: ['priority', 'status', 'category', 'event_count', 'count_new_issues'],
        sortColumn: 'event_count',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'active-incident-queue',
    category: 'Issues',
    roles: ['sre', 'devops', 'manager'],
    summary: 'What is open right now and still needs someone.',
    panel: entityPanel({
      title: 'Active incident queue',
      description: 'Events Nudgebee still considers open or needing action, newest first — the on-call working list rather than a history.',
      draft: {
        table: 'events_v2',
        columns: ['starts_at', 'title', 'priority', 'subject_type', 'subject_name', 'subject_namespace', 'nb_status'],
        filters: [{ column: 'nb_status', operator: '_in', value: 'OPEN, ACTION_REQUIRED' }],
        sortColumn: 'starts_at',
        limit: 25,
      },
      width: 12,
    }),
  },
  {
    id: 'issues-by-cluster',
    category: 'Issues',
    roles: ['cto', 'manager', 'sre'],
    summary: 'Which cluster is generating the noise — the cross-estate view.',
    panel: entityPanel({
      title: 'Issues by cluster',
      description: 'Event volume per cluster and namespace, with the P0 and P1 split. The answer to "is this everywhere, or is it one account".',
      draft: {
        table: 'event_groupings_v2',
        columns: ['cluster', 'subject_namespace', 'event_count', 'count_priority_p0', 'count_priority_p1'],
        sortColumn: 'event_count',
        limit: 15,
      },
      width: 12,
    }),
  },
  {
    id: 'anomalies-by-type',
    category: 'Issues',
    accountKind: 'any',
    roles: ['cto', 'cfo', 'manager', 'sre'],
    summary: 'Unusual behaviour across every account — metric and spend together.',
    panel: entityPanel({
      title: 'Anomalies by type',
      description:
        'Detections grouped by type and subject, most frequent first. Spans cloud and cluster accounts, because an anomaly belongs to whatever it was detected on.',
      draft: {
        table: 'anomaly_grouping_v2',
        columns: ['anomaly_type', 'namespace', 'name', 'count'],
        filters: [{ column: 'is_anomaly', operator: '_eq', value: 'true' }],
        sortColumn: 'count',
        limit: 15,
      },
    }),
  },
  {
    id: 'recent-anomalies',
    category: 'Issues',
    accountKind: 'any',
    roles: ['sre', 'cloudops', 'devops'],
    summary: 'The detections themselves, with observed against expected.',
    panel: entityPanel({
      title: 'Recent anomalies',
      description: 'Each detection as it fired: what it is about, what the value was, and what was expected. The rows behind the grouped feed.',
      draft: {
        table: 'anomaly_v2',
        columns: ['evaluated_at', 'anomaly_type', 'namespace', 'name', 'current_value', 'reference_value'],
        filters: [{ column: 'is_anomaly', operator: '_eq', value: 'true' }],
        sortColumn: 'evaluated_at',
        limit: 25,
      },
      width: 12,
    }),
  },
  {
    id: 'ticket-volume-by-status',
    category: 'Issues',
    roles: ['manager', 'devops', 'cloudops'],
    summary: 'The ticket backlog, by status and severity.',
    panel: entityPanel({
      title: 'Ticket volume',
      description: 'Tickets raised from Nudgebee grouped by status and severity — how much work this is creating, and how much of it is still open.',
      draft: {
        table: 'ticket_groupings_v2',
        columns: ['status', 'severity', 'count'],
        sortColumn: 'count',
        limit: 15,
      },
    }),
  },
  {
    id: 'ticket-load-by-assignee',
    category: 'Issues',
    roles: ['manager', 'cto', 'sre'],
    summary: 'Who is carrying the work — the closest thing to on-call load.',
    panel: entityPanel({
      title: 'Ticket load by assignee',
      description:
        'Open ticket count per assignee, with the status split. A stand-in for responder load: it measures the work that got filed, not every page.',
      draft: {
        table: 'ticket_groupings_v2',
        columns: ['assignee', 'status', 'count'],
        sortColumn: 'count',
        limit: 15,
      },
    }),
  },

  // ── Capacity ────────────────────────────────────────────────────────────
  {
    id: 'cluster-cpu-utilisation',
    category: 'Capacity',
    roles: ['cto', 'manager', 'sre', 'devops'],
    summary: 'How much of the CPU you pay for is actually being burned.',
    panel: metricPanel({
      title: 'Cluster CPU utilisation',
      description: 'Non-idle CPU across every node, as a share of the cores the cluster has.',
      expr: '100 * sum(rate(node_cpu_seconds_total{mode!="idle"}[5m])) / sum(machine_cpu_cores)',
      unit: '%',
    }),
  },
  {
    id: 'cluster-memory-utilisation',
    category: 'Capacity',
    roles: ['cto', 'manager', 'sre', 'devops'],
    summary: 'The same question for memory.',
    panel: metricPanel({
      title: 'Cluster memory utilisation',
      description: 'Memory in use across every node, as a share of what is installed.',
      expr: '100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))',
      unit: '%',
    }),
  },
  {
    id: 'cpu-requests-vs-allocatable',
    category: 'Capacity',
    roles: ['sre', 'devops', 'manager'],
    summary: 'How much CPU is reserved but idle — the gap that costs money.',
    panel: metricPanel({
      title: 'CPU requests vs allocatable',
      description:
        'What workloads have reserved, against what the nodes can hand out. Read next to actual utilisation: a high number here with a low number there is over-request.',
      expr: '100 * sum(kube_pod_container_resource_requests{resource="cpu"}) / sum(kube_node_status_allocatable{resource="cpu"})',
      unit: '%',
    }),
  },
  {
    id: 'memory-requests-vs-allocatable',
    category: 'Capacity',
    roles: ['sre', 'devops', 'manager'],
    summary: 'The same reservation gap for memory.',
    panel: metricPanel({
      title: 'Memory requests vs allocatable',
      description: 'Reserved memory against what the nodes can hand out.',
      expr: '100 * sum(kube_pod_container_resource_requests{resource="memory"}) / sum(kube_node_status_allocatable{resource="memory"})',
      unit: '%',
    }),
  },
  {
    id: 'node-cpu-utilisation',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'The ten hottest nodes — where a hotspot hides behind a healthy average.',
    panel: metricPanel({
      title: 'Busiest nodes by CPU',
      description: 'Per-node CPU utilisation, top ten. A cluster averaging 40% can still have a node at 95%.',
      expr: 'topk(10, 100 * sum by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[5m])) / count by (instance) (node_cpu_seconds_total{mode="idle"}))',
      unit: '%',
    }),
  },
  {
    id: 'node-filesystem-utilisation',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Root disks filling up, before the kubelet starts evicting.',
    panel: metricPanel({
      title: 'Node root filesystem used',
      description: 'Used share of the root filesystem, per node.',
      expr: '100 * (1 - sum by (instance) (node_filesystem_avail_bytes{mountpoint="/"}) / sum by (instance) (node_filesystem_size_bytes{mountpoint="/"}))',
      unit: '%',
    }),
  },
  {
    id: 'pvc-utilisation',
    category: 'Capacity',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Volumes about to run out — the failure nobody sees coming.',
    panel: metricPanel({
      title: 'Fullest persistent volumes',
      description: 'Used share of each PersistentVolumeClaim, top ten.',
      expr: 'topk(10, 100 * sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_used_bytes) / sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_capacity_bytes))',
      unit: '%',
    }),
  },
  {
    id: 'ready-nodes',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Nodes currently Ready.',
    panel: metricPanel({
      title: 'Ready nodes',
      description: 'Nodes reporting the Ready condition.',
      expr: 'sum(kube_node_status_condition{condition="Ready",status="true"})',
      unit: 'nodes',
      type: 'stat',
      width: 3,
    }),
  },
  {
    id: 'unschedulable-nodes',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Nodes cordoned off — deliberately, or by a stuck drain.',
    panel: metricPanel({
      title: 'Unschedulable nodes',
      description: 'Nodes marked unschedulable. A number that stays above zero is usually a drain nobody finished.',
      expr: 'sum(kube_node_spec_unschedulable)',
      unit: 'nodes',
      type: 'stat',
      width: 3,
    }),
  },
  {
    id: 'pods-per-node',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Scheduling skew — pods piled onto a few nodes.',
    panel: metricPanel({
      title: 'Pods per node',
      description: 'Scheduled pods per node, top ten.',
      expr: 'topk(10, count by (node) (kube_pod_info))',
      unit: 'pods',
    }),
  },

  {
    id: 'cluster-health-overview',
    category: 'Capacity',
    roles: ['cto', 'manager', 'devops', 'sre'],
    summary: 'Every cluster on one row: size, spot share and what its pods are doing.',
    panel: entityPanel({
      title: 'Cluster overview',
      description:
        'One row per connected cluster — nodes, how many are spot, CPU and memory capacity, and the pod count by status. The fleet view a PromQL panel cannot give, because it spans clusters.',
      draft: {
        table: 'k8s_cluster_groupings_v2',
        columns: ['account_id', 'node_count', 'node_spot_count', 'node_cpu_capacity', 'node_memory_capacity', 'pod_status_counts'],
        sortColumn: 'node_count',
        limit: 25,
      },
      width: 12,
    }),
  },
  {
    id: 'node-inventory',
    category: 'Capacity',
    roles: ['devops', 'sre', 'cloudops'],
    summary: 'Every node with its size, its pods and what it costs.',
    panel: entityPanel({
      title: 'Node inventory',
      description:
        'Active nodes with instance type, spot or on-demand, capacity, pod count and cost — the table to read before cordoning one or changing a node group.',
      draft: {
        table: 'k8s_nodes_v2',
        columns: ['name', 'node_type', 'node_flavor', 'node_region', 'cpu_capacity', 'memory_capacity', 'pod_count', 'cost'],
        filters: [{ column: 'is_active', operator: '_eq', value: 'true' }],
        sortColumn: 'pod_count',
        // Nodes are current state, not events. Filtering them by the
        // dashboard's window would hide every node created before it — which is
        // all of them, on a 24h dashboard.
        applyTimeRange: false,
        limit: 25,
      },
      width: 12,
    }),
  },

  // ── Reliability ─────────────────────────────────────────────────────────
  {
    id: 'pending-pods',
    category: 'Reliability',
    roles: ['sre', 'devops'],
    summary: 'Pods that cannot be scheduled — capacity or affinity, never both.',
    panel: metricPanel({
      title: 'Pending pods',
      description: 'Pods stuck in Pending. Sustained above zero means the scheduler cannot place them.',
      expr: 'sum(kube_pod_status_phase{phase="Pending"})',
      unit: 'pods',
    }),
  },
  {
    id: 'failed-pods',
    category: 'Reliability',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Pods that gave up entirely.',
    panel: metricPanel({
      title: 'Failed pods',
      description: 'Pods in the Failed phase.',
      expr: 'sum(kube_pod_status_phase{phase="Failed"})',
      unit: 'pods',
    }),
  },
  {
    id: 'pod-restarts-by-namespace',
    category: 'Reliability',
    roles: ['manager', 'sre', 'devops', 'developer'],
    summary: 'Restart churn, and whose namespace it is in.',
    panel: metricPanel({
      title: 'Container restarts by namespace',
      description: 'Restarts in the last hour, top ten namespaces. The single best proxy for "something is quietly broken".',
      expr: 'topk(10, sum by (namespace) (increase(kube_pod_container_status_restarts_total[1h])))',
      unit: 'restarts',
    }),
  },
  {
    id: 'container-waiting-reasons',
    category: 'Reliability',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Why containers are not starting — CrashLoop, ImagePull, config.',
    panel: metricPanel({
      title: 'Containers waiting, by reason',
      description: 'Containers stuck in a waiting state, grouped by the reason the kubelet reported.',
      expr: 'sum by (reason) (kube_pod_container_status_waiting_reason) > 0',
      unit: 'containers',
    }),
  },
  {
    id: 'oomkilled-containers',
    category: 'Reliability',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Containers killed for exceeding their memory limit.',
    panel: metricPanel({
      title: 'OOMKilled containers',
      description: 'Containers whose last termination was an out-of-memory kill, by namespace. Either the limit is too low or the service leaks.',
      expr: 'sum by (namespace) (kube_pod_container_status_terminated_reason{reason="OOMKilled"})',
      unit: 'containers',
    }),
  },
  {
    id: 'deployments-unavailable',
    category: 'Reliability',
    roles: ['manager', 'sre', 'devops'],
    summary: 'Deployments not running the replicas they were asked for.',
    panel: metricPanel({
      title: 'Deployments with unavailable replicas',
      description: 'Replicas short of the desired count, per deployment. During a rollout this spikes and recovers; a flat line is a stuck rollout.',
      expr: 'sum by (namespace, deployment) (kube_deployment_status_replicas_unavailable) > 0',
      unit: 'replicas',
    }),
  },
  {
    id: 'daemonsets-unavailable',
    category: 'Reliability',
    roles: ['sre', 'devops'],
    summary: 'Node agents missing from nodes they should be on.',
    panel: metricPanel({
      title: 'DaemonSets with unavailable pods',
      description: 'DaemonSet pods missing on nodes that should be running them — often the reason a node looks unmonitored.',
      expr: 'sum by (namespace, daemonset) (kube_daemonset_status_number_unavailable) > 0',
      unit: 'pods',
    }),
  },
  {
    id: 'failed-jobs',
    category: 'Reliability',
    roles: ['devops', 'developer'],
    summary: 'Batch jobs and CronJobs that failed.',
    panel: metricPanel({
      title: 'Failed jobs',
      description: 'Jobs reporting failed pods. Nothing pages on these, which is exactly why they need a panel.',
      expr: 'sum by (namespace, job_name) (kube_job_status_failed) > 0',
      unit: 'jobs',
    }),
  },
  {
    id: 'pvc-not-bound',
    category: 'Reliability',
    roles: ['sre', 'devops'],
    summary: 'Volume claims that never got a volume.',
    panel: metricPanel({
      title: 'PVCs not bound',
      description: 'PersistentVolumeClaims in any phase other than Bound — a pod waiting on one of these will never start.',
      expr: 'sum by (namespace, persistentvolumeclaim) (kube_persistentvolumeclaim_status_phase{phase!="Bound"}) > 0',
      unit: 'claims',
    }),
  },

  // ── Performance ─────────────────────────────────────────────────────────
  {
    id: 'request-rate-by-service',
    category: 'Performance',
    roles: ['sre', 'developer', 'manager'],
    summary: 'Traffic, per service. The first of the four golden signals.',
    panel: metricPanel({
      title: 'Request rate by service',
      description: 'HTTP requests per second reaching each workload.',
      expr: 'sum by (destination_workload_name) (rate(container_http_requests_total{destination_workload_namespace=~".*"}[5m]))',
      unit: 'req/s',
    }),
  },
  {
    id: 'error-rate-percent',
    category: 'Performance',
    roles: ['cto', 'manager', 'sre', 'developer'],
    summary: 'The share of requests failing — the number an SLO is written against.',
    panel: metricPanel({
      title: 'Error rate',
      description: '5xx responses as a share of all requests.',
      expr: '100 * sum(rate(container_http_requests_total{status=~"5..", destination_workload_namespace=~".*"}[5m])) / sum(rate(container_http_requests_total{destination_workload_namespace=~".*"}[5m]))',
      unit: '%',
    }),
  },
  {
    id: 'p99-latency-by-service',
    category: 'Performance',
    roles: ['sre', 'developer', 'manager'],
    summary: 'Tail latency, per service — what your slowest users actually feel.',
    panel: metricPanel({
      title: 'p99 latency by service',
      description: '99th percentile request duration per workload.',
      expr: 'histogram_quantile(0.99, sum by (le, destination_workload_name) (rate(container_http_requests_duration_seconds_total_bucket{destination_workload_namespace=~".*"}[5m])))',
      unit: 's',
    }),
  },
  {
    id: 'p50-latency-by-service',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'Median latency, to tell a slow tail from a slow service.',
    panel: metricPanel({
      title: 'p50 latency by service',
      description: 'Median request duration per workload. Read beside p99: both rising is a slow service, only p99 rising is a tail problem.',
      expr: 'histogram_quantile(0.50, sum by (le, destination_workload_name) (rate(container_http_requests_duration_seconds_total_bucket{destination_workload_namespace=~".*"}[5m])))',
      unit: 's',
    }),
  },
  {
    id: 'top-endpoints-by-traffic',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'The busiest routes, which is where optimisation pays.',
    panel: metricPanel({
      title: 'Busiest endpoints',
      description: 'Request rate per path, top ten.',
      expr: 'topk(10, sum by (path, destination_workload_name) (rate(container_http_requests_total{destination_workload_namespace=~".*"}[5m])))',
      unit: 'req/s',
    }),
  },
  {
    id: 'error-log-volume',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'Which containers are shouting — errors nobody turned into an alert.',
    panel: metricPanel({
      title: 'Error log volume by container',
      description: 'Error and critical log lines per container, top ten.',
      expr: 'topk(10, sum by (container_id) (increase(container_log_messages_total{level=~"critical|error"}[5m])))',
      unit: 'lines',
    }),
  },
  {
    id: 'network-receive-by-namespace',
    category: 'Performance',
    roles: ['sre', 'devops'],
    summary: 'Inbound traffic per namespace — cross-zone bills start here.',
    panel: metricPanel({
      title: 'Network received by namespace',
      description: 'Bytes per second received by containers, per namespace.',
      expr: 'sum by (namespace) (rate(container_network_receive_bytes_total[5m]))',
      unit: 'B/s',
    }),
  },
  {
    id: 'slowest-service-calls',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'The slowest calls between services, from traces rather than metrics.',
    panel: entityPanel({
      title: 'Slowest service calls',
      description: 'Spans grouped by caller and operation, worst p99 first. Latency percentiles exist only on this table.',
      draft: {
        table: 'traces_groupings_v2',
        columns: ['workload_name', 'workload_namespace', 'span_name', 'count', 'error_count', 'p99_latency'],
        sortColumn: 'p99_latency',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'failing-service-calls',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'Which call between which two services is failing.',
    panel: entityPanel({
      title: 'Failing service calls',
      description: 'The same grouping ranked by error count — the caller/callee pair behind an error-rate spike.',
      draft: {
        table: 'traces_groupings_v2',
        columns: ['workload_name', 'span_name', 'destination_workload_name', 'http_status_code', 'count', 'error_count'],
        sortColumn: 'error_count',
        limit: 20,
      },
      width: 12,
    }),
  },

  // ── Workload ────────────────────────────────────────────────────────────
  {
    id: 'workload-cpu-usage',
    category: 'Workload',
    roles: ['developer', 'sre'],
    summary: 'CPU burned by one service’s pods.',
    panel: metricPanel({
      title: 'CPU usage by pod',
      description: 'CPU cores used per pod of the selected workload.',
      expr: 'sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=~".*", pod=~".*"}[5m]))',
      unit: 'cores',
    }),
  },
  {
    id: 'workload-memory-usage',
    category: 'Workload',
    roles: ['developer', 'sre'],
    summary: 'Working-set memory per pod — the number the OOM killer reads.',
    panel: metricPanel({
      title: 'Memory usage by pod',
      description: 'Working-set bytes per pod of the selected workload.',
      expr: 'sum by (pod) (container_memory_working_set_bytes{namespace=~".*", pod=~".*"})',
      unit: 'bytes',
    }),
  },
  {
    id: 'workload-restarts',
    category: 'Workload',
    roles: ['developer', 'sre'],
    summary: 'Restarts of one service, pod by pod.',
    panel: metricPanel({
      title: 'Restarts by pod',
      description: 'Container restarts in the last hour, per pod of the selected workload.',
      expr: 'sum by (pod) (increase(kube_pod_container_status_restarts_total{namespace=~".*", pod=~".*"}[1h]))',
      unit: 'restarts',
    }),
  },
  {
    id: 'workload-replicas-available',
    category: 'Workload',
    roles: ['developer', 'devops'],
    summary: 'Replicas actually serving, through a deploy and after it.',
    panel: metricPanel({
      title: 'Available replicas',
      description: 'Replicas reporting available for the selected deployment.',
      expr: 'sum by (deployment) (kube_deployment_status_replicas_available{namespace=~".*", deployment=~".*"})',
      unit: 'replicas',
    }),
  },

  // ── Security ────────────────────────────────────────────────────────────
  {
    id: 'cis-compliance-findings',
    category: 'Security',
    roles: ['devops', 'manager', 'cloudops'],
    summary: 'Which benchmark rules the cluster fails, and how widely.',
    panel: entityPanel({
      title: 'CIS compliance findings',
      description:
        'CIS benchmark results by rule, with how many resources are behind each verdict. The posture list to work down, worst-covered first.',
      draft: {
        table: 'recommendation_security_cis_groupings_v2',
        columns: ['rule_name', 'severity', 'status', 'count'],
        sortColumn: 'count',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'image-vulnerabilities',
    category: 'Security',
    roles: ['devops', 'developer', 'sre'],
    summary: 'CVEs in images that are actually running.',
    panel: entityPanel({
      title: 'Image vulnerabilities',
      description:
        'Vulnerabilities found in deployed images, newest first, with the workload running each. Restricted to active findings — an image nobody runs is not a risk.',
      draft: {
        table: 'recommendation_security_v2',
        columns: ['created_at', 'severity', 'workload_name', 'namespace', 'image', 'vulnerability_id'],
        filters: [{ column: 'is_active', operator: '_eq', value: 'true' }],
        sortColumn: 'created_at',
        limit: 25,
      },
      width: 12,
    }),
  },

  // ── Automation ──────────────────────────────────────────────────────────
  {
    id: 'automation-task-health',
    category: 'Automation',
    roles: ['cto', 'manager', 'devops', 'cloudops'],
    summary: 'How much autopilot ran, and how much of it worked.',
    panel: entityPanel({
      title: 'Autopilot task health',
      description:
        'Autopilot tasks grouped by category and state — what ran, what is queued and what failed. The coverage number behind "is this automated yet".',
      draft: {
        table: 'auto_pilot_task_groupings_v2',
        columns: ['auto_pilot_category', 'status', 'count'],
        sortColumn: 'count',
        limit: 20,
      },
    }),
  },
  {
    id: 'pending-approvals',
    category: 'Automation',
    accountKind: 'any',
    roles: ['manager', 'sre', 'cloudops', 'devops'],
    summary: 'Automated actions waiting on a human decision.',
    panel: entityPanel({
      title: 'Approval queue',
      description:
        'Proposed automated actions and what was decided, oldest at the bottom. A panel shows the queue and its age; approving is still done on the Autopilot page.',
      draft: {
        table: 'auto_pilot_approvals_v2',
        columns: ['created_at', 'auto_pilot_type', 'status', 'reviewer_display_name', 'approval_status_description'],
        sortColumn: 'created_at',
        limit: 25,
      },
      width: 12,
    }),
  },

  // ── AI ──────────────────────────────────────────────────────────────────
  {
    id: 'ai-investigation-activity',
    category: 'AI',
    roles: ['cto', 'manager', 'sre'],
    summary: 'How many investigations the AI ran, and how they ended.',
    panel: entityPanel({
      title: 'AI investigation activity',
      description:
        'Investigations grouped by where they came from — an alert, a person, a workflow — and how they finished. Throughput, and whether they complete.',
      draft: {
        table: 'llm_conversation_groupings_v2',
        columns: ['source', 'status', 'count'],
        sortColumn: 'count',
        limit: 15,
      },
    }),
  },
  {
    id: 'recent-ai-investigations',
    category: 'AI',
    roles: ['sre', 'manager', 'developer'],
    summary: 'The most recent RCAs, to open one.',
    panel: entityPanel({
      title: 'Recent AI investigations',
      description: 'Investigations started in the window, newest first, with their source and state.',
      draft: {
        table: 'llm_conversation_groupings_v2',
        columns: ['created_at', 'title', 'source', 'status'],
        sortColumn: 'created_at',
        limit: 25,
      },
      width: 12,
    }),
  },

  // ── Governance ──────────────────────────────────────────────────────────
  {
    id: 'agent-health',
    category: 'Governance',
    roles: ['devops', 'manager', 'cloudops'],
    summary: 'Whether the collectors are still talking to us.',
    panel: entityPanel({
      title: 'Agent health',
      description:
        'Every collector agent with its version, cluster version and when it last checked in. A stale Last seen is why a dashboard went quiet — check here before believing an empty panel.',
      draft: {
        table: 'get_agent_health_v2',
        columns: ['type', 'version', 'status', 'k8s_version', 'last_connected_at'],
        // The point of this panel is the agent that STOPPED reporting. Filtering
        // by last-seen inside the dashboard's window would hide exactly those.
        applyTimeRange: false,
        sortColumn: 'last_connected_at',
        limit: 25,
      },
      width: 12,
    }),
  },
  {
    id: 'audit-log',
    category: 'Governance',
    accountKind: 'any',
    roles: ['cto', 'manager'],
    summary: 'Who changed what, and whether it worked.',
    panel: entityPanel({
      title: 'Audit log',
      description:
        'Configuration and access changes in the window, newest first. Readable only by a tenant admin or a role holding the audits grant — the engine decides that per viewer, not per dashboard.',
      draft: {
        table: 'audits_v2',
        columns: ['event_time', 'username', 'event_category', 'event_action', 'event_target', 'event_status'],
        sortColumn: 'event_time',
        limit: 25,
      },
      width: 12,
    }),
  },
];

export function findPanelTemplate(id: string): PanelTemplate | undefined {
  return PANEL_TEMPLATES.find((t) => t.id === id);
}

export function widgetAccountKind(widget: PanelTemplate): WidgetAccountKind {
  return widget.accountKind || 'cluster';
}

/**
 * The scope to open a widget on, given the accounts the viewer can see.
 *
 * Always an account TYPE, never a list of accounts. A type means "every account
 * of this provider" and is resolved at render, so the panel widens on its own as
 * accounts are connected; pinning ids at authoring time freezes it, and a
 * pre-ticked list of somebody's accounts is a choice we have no business making
 * for them. Leaving Accounts empty in the editor says the same thing and reads
 * as a default rather than a decision.
 *
 * The provider is read off a connected account by `kind` rather than compared
 * against the literal 'K8S' / 'AWS' — those are backend vocabulary, and `kind`
 * is the field that actually means "cluster" or "cloud".
 *
 * Empty when nothing fits. A tenant with no cluster cannot scope a PromQL
 * widget, and the red "No account" chip in the editor is the honest answer —
 * better than pre-selecting an account that will not answer.
 */
export function defaultWidgetScope(widget: PanelTemplate, accounts: AccountOption[]): PanelScope {
  const kind = widgetAccountKind(widget);
  const matching = kind === 'any' ? accounts : accounts.filter((a) => a.kind === (kind === 'cloud' ? 'cloud' : 'kubernetes'));
  const providers = [...new Set(matching.map((a) => a.cloud_provider))].filter(Boolean);

  // A findings widget reads the query engine, which is the one datasource that
  // spans providers — so a cost widget opens on EVERY cloud provider rather than
  // whichever happened to be connected first. Everything else resolves one
  // provider's integration and takes the first match.
  if (widget.panel.datasource === 'nudgebee') return panelScopeFromTypes(providers, [], accounts);
  return { account_type: providers[0] || undefined, account_ids: [] };
}

/**
 * Copies a widget into a dashboard's panel list.
 *
 * The copy opens on the scope its datasource implies — a PromQL widget on the
 * cluster accounts, a findings widget on all of them — rather than on an empty
 * account field. We know where the query has to run; making the author work
 * that out for each of 41 widgets is asking them to re-derive something the
 * datasource already settled.
 *
 * It is a starting point, not a decision: the copy goes to the panel editor
 * with the scope pre-filled and visible, where it can be changed before
 * anything is saved. Passing no accounts leaves the scope empty, which is the
 * honest answer for a caller that has none to offer.
 */
export function panelFromTemplate(template: PanelTemplate, existing: Panel[], accounts: AccountOption[] = []): Panel {
  return {
    ...template.panel,
    id: nextPanelId(existing),
    ...defaultWidgetScope(template, accounts),
    // Deep-copied: the catalogue is a module-level singleton, and a panel edited
    // in place would otherwise rewrite the library for the rest of the session.
    targets: (template.panel.targets || []).map((t) => ({ ...t, ...(t.query ? { query: JSON.parse(JSON.stringify(t.query)) } : {}) })),
    grid_pos: { ...template.panel.grid_pos },
  };
}
